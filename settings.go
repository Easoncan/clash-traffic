package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/egoist/mygo"

	"clashtraffic/internal/store"
)

// ClashMode is how the app finds the Clash core.
type ClashMode string

const (
	ClashAuto   ClashMode = "auto"   // detect Clash Verge's controller
	ClashManual ClashMode = "manual" // the address in the settings
)

// SyncKind is where devices exchange their traffic.
type SyncKind string

const (
	SyncOff    SyncKind = "off"
	SyncFolder SyncKind = "folder" // a folder synced by iCloud, OneDrive, Dropbox, Syncthing…
	SyncWebDAV SyncKind = "webdav" // a WebDAV server, e.g. Nutstore (坚果云) or Nextcloud
)

// Settings are the user's preferences.
type Settings struct {
	DeviceID     string    `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	ClashMode    ClashMode `json:"clashMode"`
	ClashAddress string    `json:"clashAddress"` // unix:///path, \\.\pipe\name or http://host:port
	ClashSecret  string    `json:"clashSecret"`
	// ForceProcess keeps find-process-mode of the core at "always", without
	// which the core leaves the process of most connections unknown.
	ForceProcess bool     `json:"forceProcess"`
	SyncKind     SyncKind `json:"syncKind"`
	SyncFolder   string   `json:"syncFolder"`
	WebDAVURL    string   `json:"webdavUrl"`
	WebDAVUser   string   `json:"webdavUser"`
	WebDAVDir    string   `json:"webdavDir"`
	SyncMinutes  int      `json:"syncMinutes"`
	// TraySpeed shows the current speed next to the menu bar icon (macOS).
	TraySpeed bool `json:"traySpeed"`
	// SessionDays is how many days the access log keeps; 0 keeps it all.
	SessionDays int `json:"sessionDays"`
	// Tailscale shares the traffic with the other devices of the tailnet
	// that run the app, found on their own.
	Tailscale bool `json:"tailscale"`

	// Not stored in the settings file.
	OpenAtLogin       bool `json:"openAtLogin"`
	HasWebDAVPassword bool `json:"hasWebdavPassword"`
}

// settingsFile is what the settings file holds.
type settingsFile struct {
	Settings
	// WebDAVPassword is never sent to the page.
	WebDAVPassword string `json:"webdavPassword,omitempty"`
}

func defaultSettings() Settings {
	return Settings{
		ClashMode:    ClashAuto,
		ForceProcess: true,
		SyncKind:     SyncOff,
		WebDAVURL:    "https://dav.jianguoyun.com/dav/",
		WebDAVDir:    "ClashTraffic",
		SyncMinutes:  10,
		SessionDays:  30,
		Tailscale:    true,
	}
}

func loadSettings(path string) (settingsFile, error) {
	f := settingsFile{Settings: defaultSettings()}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return f, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &f); err != nil {
			f = settingsFile{Settings: defaultSettings()}
		}
	}
	s := &f.Settings
	changed := false
	if s.DeviceID == "" {
		s.DeviceID = newDeviceID()
		changed = true
	}
	if s.DeviceName == "" {
		s.DeviceName = computerName()
		changed = true
	}
	if s.SyncMinutes <= 0 {
		s.SyncMinutes = 10
	}
	if changed {
		err = saveSettings(path, f)
	}
	return f, err
}

func saveSettings(path string, f settingsFile) error {
	f.OpenAtLogin, f.HasWebDAVPassword = false, false
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(path, b); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// newDeviceID names this computer in the synced data. It derives from the
// machine's own identifier, so that reinstalling the app keeps counting
// under the same device; development builds count apart.
func newDeviceID() string {
	seed := machineID()
	if seed == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		seed = hex.EncodeToString(b)
	}
	sum := sha256.Sum256([]byte("clash-traffic:" + seed))
	id := hex.EncodeToString(sum[:6])
	if mygo.IsDev() {
		id += "-dev"
	}
	return id
}

// computerName is the name users gave this computer.
func computerName() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("scutil", "--get", "ComputerName").Output(); err == nil {
			if n := strings.TrimSpace(string(out)); n != "" {
				return n
			}
		}
	}
	if n := os.Getenv("COMPUTERNAME"); n != "" {
		return n
	}
	n, _ := os.Hostname()
	n = strings.TrimSuffix(n, ".local")
	if n == "" {
		n = runtime.GOOS
	}
	return n
}

// webdavPassword returns the WebDAV password. MyGo v0.4.8 has no access
// to the system's credential store, so it lives in the settings file, which
// only the user can read.
func (f *settingsFile) webdavPassword() string { return f.WebDAVPassword }

func (f *settingsFile) setWebDAVPassword(pass string) { f.WebDAVPassword = pass }
