package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clashtraffic/internal/collector"
	"clashtraffic/internal/sessions"
	"clashtraffic/internal/store"
)

func TestClearStorageKeepsSettingsAndStartsEmpty(t *testing.T) {
	c := &Core{}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"deviceId":"0123456789ab","deviceName":"test","language":"en","tailscale":false,"forceProcess":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Init(dir, nil); err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(c.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"updater.json", "window-state.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	c.store.Add(store.Key{Hour: now.Unix(), App: "browser", Host: "example.com"}, "", store.Counter{Down: 12345, Conns: 1})
	c.sessions.Add(sessions.Session{ID: "old", Start: now.UnixMilli(), End: now.UnixMilli(), App: "browser", Down: 12345})
	c.flush()
	q := store.Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli()}
	_ = c.store.Query(q) // warm the caches that also have to be cleared
	sq := sessions.SessionQuery{From: q.From, To: q.To}
	_ = c.Sessions(sq)
	before, err := c.StorageUsage()
	if err != nil || before.Traffic == 0 || before.AccessLog == 0 {
		t.Fatalf("usage %+v: %v", before, err)
	}
	oldGen := c.store.Gen()
	// Tray callbacks query the store; they must run outside the clear lock.
	c.onTick = func(_ collector.Live) {
		if _, err := c.StorageUsage(); err != nil {
			t.Error(err)
		}
	}
	if err := c.ClearStorage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := c.store.Query(q); r.Down != 0 || len(r.Apps) != 0 {
		t.Fatalf("cached traffic returned: %+v", r)
	}
	if p := c.Sessions(sq); p.Total != 0 {
		t.Fatalf("old sessions returned: %+v", p)
	}
	after, err := c.StorageUsage()
	if err != nil || after.AccessLog != 0 || after.Bytes >= before.Bytes {
		t.Fatalf("usage after clear %+v, before %+v: %v", after, before, err)
	}
	if c.store.Gen() <= oldGen {
		t.Fatal("store generation went backwards")
	}
	if got, _ := os.ReadFile(c.settingsPath); string(got) != string(settings) {
		t.Fatal("settings changed")
	}
	for _, name := range []string{"updater.json", "window-state.json"} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != "keep" {
			t.Fatalf("%s changed", name)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".clear-*")); len(leftovers) != 0 {
		t.Fatalf("temporary backups remain: %v", leftovers)
	}
	c.store.Add(store.Key{Hour: now.Unix(), App: "new"}, "", store.Counter{Down: 10})
	c.flush()
	if r := c.store.Query(q); r.Down != 10 {
		t.Fatalf("counting did not resume: %+v", r)
	}
	if _, err := store.Open(c.store.Root(), c.store.Self()); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledClearKeepsStoredData(t *testing.T) {
	c := &Core{}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"deviceId":"0123456789ab","deviceName":"test","language":"en","tailscale":false,"forceProcess":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Init(dir, nil); err != nil {
		t.Fatal(err)
	}
	c.store.Add(store.Key{Hour: time.Now().Unix(), App: "keep"}, "", store.Counter{Down: 42})
	c.flush()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.ClearStorage(ctx); err == nil {
		t.Fatal("canceled clear succeeded")
	}
	now := time.Now()
	if r := c.store.Query(store.Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli()}); r.Down != 42 {
		t.Fatalf("canceled clear deleted traffic: %+v", r)
	}
}

func TestClearWaitsForWriterAndRestartsCounting(t *testing.T) {
	c := &Core{}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"deviceId":"0123456789ab","deviceName":"test","language":"en","tailscale":false,"forceProcess":false,"clashMode":"manual","clashAddress":"http://127.0.0.1:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Init(dir, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		<-ctx.Done()
		c.store.Add(store.Key{Hour: time.Now().Unix(), App: "last flush"}, "", store.Counter{Down: 99999})
		c.flush()
	}()
	if err := c.ClearStorage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.cancel == nil {
		t.Fatal("background counting was not restarted")
	}
	c.Stop() // before its first tick: no controller or user config is used
	now := time.Now()
	r := c.store.Query(store.Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli()})
	if r.Down != 0 {
		t.Fatalf("a background flush resurrected old traffic: %+v", r)
	}
}
