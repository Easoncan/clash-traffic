package collector

import (
	"path/filepath"
	"testing"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/store"
)

func TestClearDoesNotRestoreOldTraffic(t *testing.T) {
	s := &sink{}
	c := New(s)
	x := conn("open", "browser", 100, 1000)
	snap := &clash.Snapshot{UploadTotal: 100, DownloadTotal: 1000, Connections: []clash.Conn{x}}
	c.Tick(t0, snap)
	oldRun := c.run
	*s = sink{}
	at := t0.Add(time.Second)
	live := c.Clear(at, snap)
	if c.run == oldRun || s.total != (store.Counter{}) || live.Conns[0].Down != 0 || live.Conns[0].Start != at.UnixMilli() || c.OpenSessions()[0].Down != 0 {
		t.Fatalf("old traffic reappeared: total=%+v live=%+v sessions=%+v", s.total, live, c.OpenSessions())
	}
	x.Upload, x.Download = 105, 1050
	snap.UploadTotal, snap.DownloadTotal, snap.Connections[0] = 105, 1050, x
	live = c.Tick(at.Add(time.Second), snap)
	if s.total != (store.Counter{Up: 5, Down: 50}) || live.Conns[0].Down != 50 {
		t.Fatalf("post-clear traffic: %+v, live=%+v", s.total, live)
	}
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := c.Save(path, 1); err != nil {
		t.Fatal(err)
	}
	restored := New(s)
	if err := restored.Load(path, 1); err != nil {
		t.Fatal(err)
	}
	x.Download = 1060
	snap.DownloadTotal, snap.Connections[0] = 1060, x
	live = restored.Tick(at.Add(2*time.Second), snap)
	if s.total.Down != 60 || live.Conns[0].Down != 60 || restored.OpenSessions()[0].Start != at.UnixMilli() {
		t.Fatalf("restart restored pre-clear counters: total=%+v live=%+v", s.total, live)
	}
}

func TestClearWhileDisconnected(t *testing.T) {
	s := &sink{}
	c := New(s)
	at := t0.Add(time.Second)
	c.Clear(at, nil)
	x := conn("old", "browser", 100, 1000)
	live := c.Tick(at.Add(time.Second), &clash.Snapshot{UploadTotal: 100, DownloadTotal: 1000, Connections: []clash.Conn{x}})
	if s.total != (store.Counter{}) || live.Conns[0].Down != 0 {
		t.Fatalf("disconnected clear counted the past: %+v, live=%+v", s.total, live)
	}
}
