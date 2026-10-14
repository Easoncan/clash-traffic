// Package syncer exchanges the data directories of the store between
// computers through a shared folder or a WebDAV server. Each device uploads
// only its own directory and downloads the others', so no two devices ever
// write the same file.
package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"clashtraffic/internal/i18n"
	"clashtraffic/internal/store"
)

// RemoteFile is a file of a device's directory on the remote.
type RemoteFile struct {
	Device string
	Name   string
	Tag    string // changes when the file does: an ETag, or size and time
}

// Backend stores the devices' directories.
type Backend interface {
	// ID identifies the remote, so that a new one syncs from scratch.
	ID() string
	// List returns the files of every device.
	List(ctx context.Context) ([]RemoteFile, error)
	Get(ctx context.Context, device, name string) ([]byte, error)
	// Put writes a file, and returns the tag the remote's listing will
	// show for it; "" when it cannot tell.
	Put(ctx context.Context, device, name string, data []byte) (string, error)
}

// Result tells what a sync did.
type Result struct {
	Uploaded   int `json:"uploaded"`
	Downloaded int `json:"downloaded"`
	Devices    int `json:"devices"` // other devices on the remote
}

type state struct {
	Backend    string            `json:"backend"`
	Uploaded   map[string]string `json:"uploaded"`   // name → size:mtime of the local file sent
	Downloaded map[string]string `json:"downloaded"` // device/name → remote tag
	// Remote is the remote's tag of each file uploaded, as listed after
	// the upload: another tag there means someone else changed the file.
	Remote map[string]string `json:"remote"`
}

// Syncer syncs the store's root with remotes.
type Syncer struct {
	mu        sync.Mutex // one sync at a time
	root      string
	self      string
	statePath string
}

// New returns a syncer of the store's root for the local device self,
// which keeps what it synced in statePath.
func New(root, self, statePath string) *Syncer {
	return &Syncer{root: root, self: self, statePath: statePath}
}

func (s *Syncer) load(backend string) *state {
	st := &state{}
	if b, err := os.ReadFile(s.statePath); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Backend != backend {
		st = &state{Backend: backend}
	}
	if st.Uploaded == nil {
		st.Uploaded = map[string]string{}
	}
	if st.Remote == nil {
		st.Remote = map[string]string{}
	}
	if st.Downloaded == nil {
		st.Downloaded = map[string]string{}
	}
	return st
}

func (s *Syncer) save(st *state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(s.statePath, b)
}

// remote reports whether a file of the remote is another device's that the
// store keeps: names are strict, so that none reaches outside the device's
// directory, nor, on file systems that ignore case, into this device's.
func (s *Syncer) remote(f RemoteFile) bool {
	return store.ValidDeviceID(f.Device) && !strings.EqualFold(f.Device, s.self) && store.ValidFileName(f.Name)
}

// Run uploads the local device's changed files, then downloads the other
// devices' changed files. The caller flushes the store first.
func (s *Syncer) Run(ctx context.Context, b Backend) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load(b.ID())
	var res Result
	var errs []error

	// What the remote holds, of this device too: files removed there, or
	// put back to older copies, are uploaded again.
	files, listErr := b.List(ctx)
	if listErr != nil {
		errs = append(errs, i18n.Errorf("listing the remote files: %w", listErr))
	}
	remoteTags := map[string]string{}
	for _, f := range files {
		if f.Device == s.self {
			remoteTags[f.Name] = f.Tag
		}
	}

	// Upload what changed of our own directory, the device file last so
	// that its time tells when the data was complete.
	dir := filepath.Join(s.root, s.self)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && store.ValidFileName(e.Name()) && e.Name() != store.DeviceFile {
			names = append(names, e.Name())
		}
	}
	names = append(names, store.DeviceFile)
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		tag := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if st.Uploaded[name] == tag {
			remote, there := remoteTags[name]
			switch {
			case listErr != nil:
				continue // the remote is unknown: as last time
			case there && st.Remote[name] == remote:
				continue // our upload, as the remote tags it
			case there && st.Remote[name] == "":
				// Uploaded without learning the remote's tag: the copy there
				// is ours if it holds what we sent.
				if got, err := b.Get(ctx, s.self, name); err == nil && bytes.Equal(got, data) {
					st.Remote[name] = remote
					continue
				}
			}
			// Gone from the remote, or changed there by someone else.
		}
		remoteTag, err := b.Put(ctx, s.self, name, data)
		if err != nil {
			errs = append(errs, i18n.Errorf("uploading %s: %w", name, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		st.Uploaded[name] = tag
		// What the remote tags our upload with: another tag there later is
		// someone else's change. Unknown, the next sync compares contents.
		st.Remote[name] = remoteTag
		res.Uploaded++
	}
	if err := s.save(st); err != nil {
		errs = append(errs, err)
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	// Download what changed of the other devices'.
	if listErr != nil {
		return res, errors.Join(errs...)
	}
	devices := map[string]bool{}
	for _, f := range files {
		if !s.remote(f) {
			continue
		}
		devices[f.Device] = true
		key := f.Device + "/" + f.Name
		local := filepath.Join(s.root, f.Device, f.Name)
		if f.Tag != "" && st.Downloaded[key] == f.Tag {
			if _, err := os.Stat(local); err == nil {
				continue // and fetched again after the device was forgotten
			}
		}
		data, err := b.Get(ctx, f.Device, f.Name)
		if err != nil {
			errs = append(errs, i18n.Errorf("downloading %s: %w", key, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		// A copy that came another way (Tailscale) may be newer.
		wrote, err := store.ReplaceIfNewer(local, f.Name, data)
		if err != nil {
			errs = append(errs, i18n.Errorf("downloading %s: %w", key, err))
			continue
		}
		if wrote {
			res.Downloaded++
		}
		st.Downloaded[key] = f.Tag
	}
	res.Devices = len(devices)
	if err := s.save(st); err != nil {
		errs = append(errs, err)
	}
	return res, errors.Join(errs...)
}

// Folder is a directory shared between the computers by another program:
// iCloud Drive, OneDrive, Dropbox, Syncthing, a network share.
type Folder struct {
	Path string
}

func (f *Folder) ID() string { return "folder:" + filepath.Clean(f.Path) }

func (f *Folder) List(ctx context.Context) ([]RemoteFile, error) {
	if st, err := os.Stat(f.Path); errors.Is(err, os.ErrNotExist) {
		// Nothing synced yet, if the folder it goes in is there: a folder
		// missing that one too, as a network share not mounted, is an error.
		if _, perr := os.Stat(filepath.Dir(f.Path)); perr != nil {
			return nil, perr
		}
		return nil, nil
	} else if err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, i18n.Errorf("%s is not a folder", f.Path)
	}
	devs, err := os.ReadDir(f.Path)
	if err != nil {
		return nil, err
	}
	var out []RemoteFile
	for _, d := range devs {
		if !d.IsDir() || !store.ValidDeviceID(d.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(f.Path, d.Name()))
		if err != nil {
			continue
		}
		for _, e := range files {
			if !e.Type().IsRegular() || !store.ValidFileName(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, RemoteFile{Device: d.Name(), Name: e.Name(), Tag: fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())})
		}
	}
	return out, nil
}

func (f *Folder) Get(_ context.Context, device, name string) ([]byte, error) {
	fh, err := os.Open(filepath.Join(f.Path, device, name))
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return store.ReadLimited(fh, store.MaxFileBytes)
}

func (f *Folder) Put(_ context.Context, device, name string, data []byte) (string, error) {
	path := filepath.Join(f.Path, device, name)
	if err := store.WriteFileAtomic(path, data); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()), nil
}

// Check reports whether the backend works, by listing it and writing then
// deleting a file.
func Check(ctx context.Context, b Backend, self string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := b.Put(ctx, self, ".probe", []byte("ok")); err != nil {
		return i18n.Errorf("cannot write: %w", err)
	}
	if d, ok := b.(interface {
		Delete(ctx context.Context, device, name string) error
	}); ok {
		_ = d.Delete(ctx, self, ".probe")
	}
	if _, err := b.List(ctx); err != nil {
		return i18n.Errorf("cannot read: %w", err)
	}
	return nil
}

// Delete removes a file.
func (f *Folder) Delete(_ context.Context, device, name string) error {
	return os.Remove(filepath.Join(f.Path, device, name))
}
