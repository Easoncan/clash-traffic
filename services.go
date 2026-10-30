package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/collector"
	"clashtraffic/internal/i18n"
	"clashtraffic/internal/mesh"
	"clashtraffic/internal/sessions"
	"clashtraffic/internal/store"
)

// Events the pages listen to.
var (
	// LiveEvent carries the core's open connections, every second.
	LiveEvent = mygo.NewEvent[collector.Live]("live")
	// StatusEvent is sent when the connection to the core changes.
	StatusEvent = mygo.NewEvent[ClashStatus]("status")
	// SyncEvent is sent when a sync starts and ends.
	SyncEvent = mygo.NewEvent[SyncStatus]("sync")
	// DataEvent is sent when data of other devices arrived, or a device
	// was renamed or forgotten.
	DataEvent = mygo.NewEvent[DataChange]("data")
	// MeshEvent is sent when sharing over Tailscale changes.
	MeshEvent = mygo.NewEvent[mesh.MeshStatus]("mesh")
)

// DataChange tells pages to query again.
type DataChange struct {
	At     int64  `json:"at"`
	Reason string `json:"reason"`
}

// Traffic answers questions about the traffic counted on every device.
type Traffic struct{ core *Core }

// Report summarizes the traffic a query selects.
func (t *Traffic) Report(q store.Query) store.Report {
	t.core.dataMu.RLock()
	defer t.core.dataMu.RUnlock()
	return *t.core.store.Query(q)
}

// Sessions returns a page of this computer's access log: the connections
// that overlap the query's range, newest first.
func (t *Traffic) Sessions(q sessions.SessionQuery) sessions.SessionPage {
	return t.core.Sessions(q)
}

// Devices lists the devices with data, this one first.
func (t *Traffic) Devices() []store.DeviceInfo {
	t.core.dataMu.RLock()
	defer t.core.dataMu.RUnlock()
	return t.core.store.Devices()
}

// Export saves the traffic a query selects as CSV. It returns the path, or
// "" when the user canceled.
func (t *Traffic) Export(ctx context.Context, q store.Query) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       i18n.T("Export the traffic"),
		DefaultPath: "clash-traffic-" + time.Now().Format("20060102-1504") + ".csv",
		Filters:     []mygo.FileFilter{{Name: "CSV", Extensions: []string{"csv"}}},
	})
	if err != nil || path == "" {
		return "", err
	}
	t.core.dataMu.RLock()
	defer t.core.dataMu.RUnlock()
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	// A byte order mark, so that Excel reads the names as UTF-8.
	if _, err := f.WriteString("\xef\xbb\xbf"); err != nil {
		f.Close()
		return "", err
	}
	if err := t.core.store.Export(q, f); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	mygo.Shell.ShowItemInFolder(path)
	return path, nil
}

// ForgetDevice deletes another device's data from this computer, after
// asking. It returns whether it did.
func (t *Traffic) ForgetDevice(ctx context.Context, id string) (bool, error) {
	name := id
	for _, d := range t.core.store.Devices() {
		if d.ID == id {
			name = d.Name
		}
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:  mygo.CallerWindow(ctx),
		Type:    mygo.MessageWarning,
		Message: i18n.T("Delete this computer's copy of the data of “%s”?", name),
		Detail:  i18n.T("Only the copy on this computer is deleted. If the device still syncs, its data comes back at the next sync."),
		Buttons: []string{i18n.T("Delete"), i18n.T("Cancel")},
	})
	if err != nil || res.Button != 0 {
		return false, err
	}
	t.core.dataMu.RLock()
	defer t.core.dataMu.RUnlock()
	if err := t.core.store.Forget(id); err != nil {
		return false, err
	}
	_ = DataEvent.Broadcast(DataChange{At: time.Now().UnixMilli(), Reason: "forget"})
	return true, nil
}

// Monitor watches the Clash core.
type Monitor struct{ core *Core }

// Status returns the state of the connection to the core.
func (m *Monitor) Status() ClashStatus { return m.core.Status() }

// CheckProcessIdentification reads whether the current core has process
// identification enabled; it does not apply or change settings.
func (m *Monitor) CheckProcessIdentification(ctx context.Context) ClashStatus {
	return m.core.CheckProcessIdentification(ctx)
}

// Live returns the latest snapshot of every open connection.
func (m *Monitor) Live() collector.Live { return m.core.Live() }

// Rates returns the speed of the last five minutes.
func (m *Monitor) Rates() []RatePoint { return m.core.Rates() }

// Reconnect finds the core again.
func (m *Monitor) Reconnect() { m.core.Reconnect() }

// CloseConnection closes an open connection.
func (m *Monitor) CloseConnection(id string) error { return m.core.CloseConnection(id) }

// Mesh returns the state of sharing over Tailscale.
func (m *Monitor) Mesh() mesh.MeshStatus { return m.core.mesh.Status() }

// ScanPeers looks for the other devices on Tailscale now.
func (m *Monitor) ScanPeers() { m.core.mesh.Trigger() }

// Candidates lists the controllers automatic detection tries.
func (m *Monitor) Candidates() []clash.Endpoint { return clash.Candidates() }

// AboutInfo describes the app and where it keeps its data.
type AboutInfo struct {
	Version  string     `json:"version"`
	Platform string     `json:"platform"`
	DataDir  string     `json:"dataDir"`
	DeviceID string     `json:"deviceId"`
	Dev      bool       `json:"dev"`
	Updates  UpdateInfo `json:"updates"`
}

// Preferences reads and changes the settings.
type Preferences struct{ core *Core }

// Get returns the settings.
func (p *Preferences) Get() Settings { return p.core.Settings() }

// Save applies and saves the settings. A null password keeps the saved
// one; "" deletes it.
func (p *Preferences) Save(s Settings, webdavPassword *string) (Settings, error) {
	return p.core.SaveSettings(s, webdavPassword)
}

// TestClash tries to reach a core as the given settings would.
func (p *Preferences) TestClash(ctx context.Context, mode ClashMode, address, secret string) ClashStatus {
	ep, v, err := connectWith(ctx, mode, address, secret)
	if err != nil {
		return ClashStatus{Error: err.Error()}
	}
	return ClashStatus{Connected: true, Endpoint: ep.String(), Source: ep.Source, Version: v}
}

// TestSync checks that the given sync settings can read and write.
func (p *Preferences) TestSync(s Settings, webdavPassword *string) error {
	return p.core.TestSync(s, webdavPassword)
}

// ChooseFolder asks for the folder to sync through. It returns "" when the
// user canceled.
func (p *Preferences) ChooseFolder(ctx context.Context) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:            mygo.CallerWindow(ctx),
		Title:             i18n.T("Choose a sync folder"),
		Message:           i18n.T("Choose a folder that iCloud, OneDrive, Dropbox, Syncthing or the like syncs: a ClashTraffic folder is made in it"),
		Directory:         true,
		CreateDirectories: true,
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// OpenDataFolder shows the data directory in the file manager.
func (p *Preferences) OpenDataFolder() error {
	return mygo.Shell.OpenPath(p.core.store.Root())
}

// About describes the app.
func (p *Preferences) About() AboutInfo {
	p.core.dataMu.RLock()
	defer p.core.dataMu.RUnlock()
	return AboutInfo{
		Version:  mygo.App.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		DataDir:  filepath.Dir(p.core.store.Root()),
		DeviceID: p.core.store.Self().ID,
		Dev:      mygo.IsDev(),
		Updates:  updateInfo(),
	}
}

// Storage reports the disk space occupied by clearable local data.
func (p *Preferences) Storage() (StorageUsage, error) { return p.core.StorageUsage() }

// ClearStorage asks again before deleting any traffic data. Cancel is the
// default for both Enter and Escape.
func (p *Preferences) ClearStorage(ctx context.Context) (bool, error) {
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:        mygo.CallerWindow(ctx),
		Type:          mygo.MessageWarning,
		Message:       i18n.T("Clear stored traffic data?"),
		Detail:        i18n.T("This deletes traffic statistics and access records stored on this computer. Settings are kept and counting continues. Synced data from other devices may return. This cannot be undone."),
		Buttons:       []string{i18n.T("Clear data"), i18n.T("Cancel")},
		DefaultButton: 1, CancelButton: 1,
	})
	if err != nil || res.Button != 0 {
		return false, err
	}
	err = p.core.ClearStorage(ctx)
	return err == nil, err
}

// Language returns the language the app shows, a tag of i18n.Languages.
func (p *Preferences) Language() string { return i18n.Current() }

// CheckForUpdates checks for a new version now, in the update window.
func (p *Preferences) CheckForUpdates() { checkForUpdates() }

// Sync exchanges the traffic with the other devices.
type Sync struct{ core *Core }

// Now syncs now and returns how it went.
func (s *Sync) Now(ctx context.Context) (SyncStatus, error) {
	return s.core.RunSync(ctx)
}

// Status returns how the last sync went.
func (s *Sync) Status() SyncStatus { return s.core.SyncStatus() }
