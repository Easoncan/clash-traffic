package syncer

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"clashtraffic/internal/store"
)

const (
	macID = "00000000a11e"
	winID = "00000000b0b0"
)

type computer struct {
	store  *store.Store
	syncer *Syncer
}

func newComputer(t *testing.T, id string) *computer {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "data"), store.DeviceInfo{ID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	return &computer{store: s, syncer: New(s.Root(), id, filepath.Join(dir, "sync-state.json"))}
}

func (c *computer) sync(t *testing.T, b Backend) Result {
	t.Helper()
	if err := c.store.Flush(); err != nil {
		t.Fatal(err)
	}
	res, err := c.syncer.Run(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func testBackend(t *testing.T, newBackend func() Backend) {
	h := time.Now().Unix() - time.Now().Unix()%3600
	mac, win := newComputer(t, macID), newComputer(t, winID)
	mac.store.Add(store.Key{Hour: h, App: "Chrome", Proc: "chrome", Host: "google.com", Chain: "HK"}, "", store.Counter{Down: 100, Conns: 1})
	win.store.Add(store.Key{Hour: h, App: "Edge", Proc: "msedge.exe", Host: "bing.com", Chain: "DIRECT"}, "", store.Counter{Down: 40, Conns: 1})

	if err := Check(context.Background(), newBackend(), macID); err != nil {
		t.Fatal(err)
	}
	if r := mac.sync(t, newBackend()); r.Uploaded != 2 || r.Downloaded != 0 {
		t.Fatalf("mac first sync: %+v", r)
	}
	if r := win.sync(t, newBackend()); r.Uploaded != 2 || r.Downloaded != 2 || r.Devices != 1 {
		t.Fatalf("win sync: %+v", r)
	}
	if r := mac.sync(t, newBackend()); r.Uploaded != 0 || r.Downloaded != 2 {
		t.Fatalf("mac second sync: %+v", r)
	}
	// Nothing changed: nothing moves.
	if r := mac.sync(t, newBackend()); r.Uploaded != 0 || r.Downloaded != 0 {
		t.Fatalf("idle sync: %+v", r)
	}

	q := store.Query{From: h * 1000, To: (h + 3600) * 1000}
	for _, c := range []*computer{mac, win} {
		r := c.store.Query(q)
		if r.Down != 140 || len(r.Devices) != 2 || len(r.Apps) != 2 {
			t.Fatalf("%s sees down %d, %d devices, %d apps", c.store.Self().ID, r.Down, len(r.Devices), len(r.Apps))
		}
	}

	// More traffic on Windows reaches the Mac.
	win.store.Add(store.Key{Hour: h, App: "Edge", Proc: "msedge.exe", Host: "bing.com", Chain: "DIRECT"}, "", store.Counter{Down: 60})
	time.Sleep(10 * time.Millisecond)
	win.sync(t, newBackend())
	if r := mac.sync(t, newBackend()); r.Downloaded == 0 {
		t.Fatalf("mac missed the update: %+v", r)
	}
	if r := mac.store.Query(q); r.Down != 200 {
		t.Fatalf("down = %d, want 200", r.Down)
	}

	// Forgotten, the device comes back at the next sync (review #14).
	if err := mac.store.Forget(winID); err != nil {
		t.Fatal(err)
	}
	if r := mac.sync(t, newBackend()); r.Downloaded == 0 {
		t.Fatalf("not fetched again after forgetting: %+v", r)
	}
	if r := mac.store.Query(q); r.Down != 200 {
		t.Fatalf("after forgetting: down = %d, want 200", r.Down)
	}
}

// A copy that arrived another way and is newer is kept (review #6).
func TestOlderCopyDoesNotReplace(t *testing.T) {
	h := time.Now().Unix() - time.Now().Unix()%3600
	dir := t.TempDir()
	mac, win := newComputer(t, macID), newComputer(t, winID)
	k := store.Key{Hour: h, App: "Edge", Host: "bing.com", Chain: "DIRECT"}
	win.store.Add(k, "", store.Counter{Down: 150})
	win.sync(t, &Folder{Path: dir}) // the folder has the older copy
	win.store.Add(k, "", store.Counter{Down: 50})
	win.store.Flush()
	// The newer one reaches the Mac by Tailscale, written straight in.
	day := store.DayOf(time.Unix(h, 0)) + store.DayExt
	newer, _ := os.ReadFile(filepath.Join(win.store.Root(), winID, day))
	if err := store.WriteFileAtomic(filepath.Join(mac.store.Root(), winID, day), newer); err != nil {
		t.Fatal(err)
	}
	mac.sync(t, &Folder{Path: dir})
	q := store.Query{From: h * 1000, To: (h + 3600) * 1000}
	if r := mac.store.Query(q); r.Down != 200 {
		t.Fatalf("the folder's older copy replaced the newer: down %d", r.Down)
	}
}

// Names that are not a device's are not synced (review #4).
func TestStrangeNames(t *testing.T) {
	dir := t.TempDir()
	mac := newComputer(t, macID)
	for _, p := range []string{strings.ToUpper(macID) + "/device.json", "not-a-device/device.json", winID + "/settings.json"} {
		path := filepath.Join(dir, "x", p)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(`{"id":"x","updated":99999999999999}`), 0o644)
	}
	if r := mac.sync(t, &Folder{Path: filepath.Join(dir, "x")}); r.Downloaded != 0 {
		t.Fatalf("downloaded %+v", r)
	}
}

func TestFolder(t *testing.T) {
	dir := t.TempDir()
	testBackend(t, func() Backend { return &Folder{Path: dir} })
}

func TestWebDAV(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(&webdav.Handler{FileSystem: webdav.Dir(dir), LockSystem: webdav.NewMemLS()})
	defer srv.Close()
	testBackend(t, func() Backend { return NewWebDAV(srv.URL+"/", "", "", "ClashTraffic") })
}

// A remote cleared, or put back to an older copy, gets this device's files
// again (review N1), even before a sync learned the tags of its uploads
// (round four, B3).
func TestRemoteLosesFiles(t *testing.T) {
	for _, kind := range []string{"folder", "webdav"} {
		t.Run(kind, func(t *testing.T) {
			h := time.Now().Unix() - time.Now().Unix()%3600
			dir := t.TempDir()
			remote := filepath.Join(dir, "ClashTraffic") // where both keep the files
			var b Backend = &Folder{Path: remote}
			if kind == "webdav" {
				srv := httptest.NewServer(&webdav.Handler{FileSystem: webdav.Dir(dir), LockSystem: webdav.NewMemLS()})
				defer srv.Close()
				b = NewWebDAV(srv.URL+"/", "", "", "ClashTraffic")
			}
			mac := newComputer(t, macID)
			mac.store.Add(store.Key{Hour: h, App: "x"}, "", store.Counter{Down: 1})
			if r := mac.sync(t, b); r.Uploaded != 2 {
				t.Fatalf("first sync %+v", r)
			}

			// Right after the upload, someone puts an older copy back.
			day := store.DayOf(time.Unix(h, 0)) + store.DayExt
			time.Sleep(1100 * time.Millisecond) // past the remote's time resolution
			os.WriteFile(filepath.Join(remote, macID, day), []byte("an older copy"), 0o644)
			if r := mac.sync(t, b); r.Uploaded != 1 {
				t.Fatalf("after the remote went back: %+v", r)
			}
			got, _ := os.ReadFile(filepath.Join(remote, macID, day))
			mine, _ := os.ReadFile(filepath.Join(mac.store.Root(), macID, day))
			if string(got) != string(mine) {
				t.Fatal("the remote kept the older copy")
			}
			if r := mac.sync(t, b); r.Uploaded != 0 {
				t.Fatalf("nothing changed, yet uploaded %+v", r)
			}

			// The shared folder is emptied, and made again.
			os.RemoveAll(filepath.Join(remote, macID))
			if r := mac.sync(t, b); r.Uploaded != 2 {
				t.Fatalf("after the remote was emptied: %+v", r)
			}
		})
	}
}

// A folder not mounted is no empty remote.
func TestFolderNotThere(t *testing.T) {
	if _, err := (&Folder{Path: filepath.Join(t.TempDir(), "unmounted", "ClashTraffic")}).List(context.Background()); err == nil {
		t.Fatal("listed a folder whose parent is not there")
	}
	if files, err := (&Folder{Path: filepath.Join(t.TempDir(), "ClashTraffic")}).List(context.Background()); err != nil || len(files) != 0 {
		t.Fatalf("first sync: %v %v", files, err)
	}
}

// noTag is a backend that never learns the tag of what it uploads, as a
// WebDAV server whose PROPFIND failed right after the PUT; gets counts
// the downloads.
type noTag struct {
	*Folder
	gets int
}

func (n *noTag) Put(ctx context.Context, device, name string, data []byte) (string, error) {
	_, err := n.Folder.Put(ctx, device, name, data)
	return "", err
}

func (n *noTag) Get(ctx context.Context, device, name string) ([]byte, error) {
	n.gets++
	return n.Folder.Get(ctx, device, name)
}

// Without the tag of an upload, the remote's copy is ours only if it holds
// what was sent (round five, B2).
func TestUploadWithoutTag(t *testing.T) {
	h := time.Now().Unix() - time.Now().Unix()%3600
	remote := filepath.Join(t.TempDir(), "ClashTraffic")
	b := &noTag{Folder: &Folder{Path: remote}}
	mac := newComputer(t, macID)
	mac.store.Add(store.Key{Hour: h, App: "x"}, "", store.Counter{Down: 1})
	if r := mac.sync(t, b); r.Uploaded != 2 {
		t.Fatalf("first sync %+v", r)
	}
	// Untouched: compared once, then known.
	if r := mac.sync(t, b); r.Uploaded != 0 || b.gets != 2 {
		t.Fatalf("untouched: %+v, %d downloads", r, b.gets)
	}
	if r := mac.sync(t, b); r.Uploaded != 0 || b.gets != 2 {
		t.Fatalf("known: %+v, %d downloads", r, b.gets)
	}

	// Uploaded again without a tag, then put back to an older copy before
	// the next sync.
	mac.store.Add(store.Key{Hour: h, App: "x"}, "", store.Counter{Down: 1})
	mac.sync(t, b)
	day := store.DayOf(time.Unix(h, 0)) + store.DayExt
	os.WriteFile(filepath.Join(remote, macID, day), []byte("an older copy"), 0o644)
	if r := mac.sync(t, b); r.Uploaded != 1 {
		t.Fatalf("the older copy was taken for ours: %+v", r)
	}
}

// A file past the limit is not read whole, and the copy kept stays
// (round five, C1).
func TestFolderFileLimit(t *testing.T) {
	saved := store.MaxFileBytes
	store.MaxFileBytes = 1 << 10
	defer func() { store.MaxFileBytes = saved }()
	remote := t.TempDir()
	os.MkdirAll(filepath.Join(remote, winID), 0o755)
	os.WriteFile(filepath.Join(remote, winID, "2026-10-13.json.gz"), make([]byte, 4<<10), 0o644)
	if _, err := (&Folder{Path: remote}).Get(context.Background(), winID, "2026-10-13.json.gz"); err == nil {
		t.Fatal("read a file past the limit")
	}
	mac := newComputer(t, macID)
	mac.store.Flush()
	kept := filepath.Join(mac.store.Root(), winID, "2026-10-13.json.gz")
	if _, err := mac.syncer.Run(context.Background(), &Folder{Path: remote}); err == nil {
		t.Fatal("the oversized file was not reported")
	}
	if _, err := os.Stat(kept); err == nil {
		t.Fatal("the oversized file was stored")
	}
	// A copy already there stays as it is.
	os.MkdirAll(filepath.Dir(kept), 0o755)
	os.WriteFile(kept, []byte("the copy kept"), 0o644)
	mac.syncer.Run(context.Background(), &Folder{Path: remote})
	if b, _ := os.ReadFile(kept); string(b) != "the copy kept" {
		t.Fatal("the copy kept was replaced")
	}
}
