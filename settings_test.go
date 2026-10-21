package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A settings file that cannot be read is moved aside, not written over:
// it holds the WebDAV password and the sync settings.
func TestUnreadableSettingsAreKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	bad := []byte(`{"deviceId":"0123456789ab","webdavPassword":"secret"}}`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.DeviceID == "" || f.webdavPassword() != "" {
		t.Fatalf("got %+v, want the defaults with a device id", f)
	}
	aside, _ := filepath.Glob(path + ".unreadable-*")
	if len(aside) != 1 {
		t.Fatalf("moved aside: %v", aside)
	}
	if b, _ := os.ReadFile(aside[0]); string(b) != string(bad) {
		t.Fatalf("the file moved aside holds %q", b)
	}
	if g, err := loadSettings(path); err != nil || g.DeviceID != f.DeviceID {
		t.Fatalf("reloaded %q, %v; want %q", g.DeviceID, err, f.DeviceID)
	}
}
