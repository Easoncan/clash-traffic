package collector

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/sessions"
	"clashtraffic/internal/store"
)

type sink struct {
	byApp map[string]store.Counter
	bySub map[string]store.Counter
	total store.Counter
}

func (s *sink) Add(k store.Key, _ string, c store.Counter) {
	if s.byApp == nil {
		s.byApp = map[string]store.Counter{}
		s.bySub = map[string]store.Counter{}
	}
	a := s.byApp[k.App]
	a.Add(c)
	s.byApp[k.App] = a
	b := s.bySub[k.Sub]
	b.Add(c)
	s.bySub[k.Sub] = b
	s.total.Add(c)
}

// The tests run at t0, in the future: their connections open after the
// collectors are made.
var t0 = time.Unix(1_900_000_000, 0)

func conn(id, proc string, up, down int64) clash.Conn {
	return clash.Conn{ID: id, Upload: up, Download: down, Start: t0, Chains: []string{"HK", "Proxy"},
		Metadata: clash.Metadata{Process: proc, Host: proc + ".example.com", SourceIP: "127.0.0.1"}}
}

func TestDeltasAndResidual(t *testing.T) {
	s := &sink{}
	c := New(s)

	// The first snapshot counts the bytes of the connections open already,
	// but no difference to the core's totals, which include the past.
	c.Tick(t0, &clash.Snapshot{UploadTotal: 5000, DownloadTotal: 50000, Connections: []clash.Conn{
		conn("a", "chrome", 100, 1000),
		conn("b", "curl", 10, 100),
	}})
	if s.total != (store.Counter{Up: 110, Down: 1100, Conns: 2}) {
		t.Fatalf("first tick: %+v", s.total)
	}

	// a carries 50/500 more; b closes after 20/2000 more, unseen; c opens.
	live := c.Tick(t0.Add(time.Second), &clash.Snapshot{UploadTotal: 5000 + 50 + 20 + 5, DownloadTotal: 50000 + 500 + 2000 + 7, Connections: []clash.Conn{
		conn("a", "chrome", 150, 1500),
		conn("c", "git", 5, 7),
	}})
	if live.DownRate != 2507 || live.Conns[0].App != "chrome" || live.Conns[0].DownRate != 500 || live.Total != 2 {
		t.Fatalf("live %+v", live)
	}
	// The difference is handed out once a second snapshot confirms it.
	if got := s.byApp["curl"]; got.Down != 100 {
		t.Fatalf("handed out before confirmed: %+v", got)
	}
	c.Tick(t0.Add(2*time.Second), &clash.Snapshot{UploadTotal: 5075, DownloadTotal: 52507, Connections: []clash.Conn{
		conn("a", "chrome", 150, 1500),
		conn("c", "git", 5, 7),
	}})
	if got := s.byApp["curl"]; got.Up != 10+20 || got.Down != 100+2000 {
		t.Fatalf("curl got %+v, want the bytes it closed with", got)
	}
	if s.total.Up != 110+75 || s.total.Down != 1100+2507 {
		t.Fatalf("totals %+v do not match the core's", s.total)
	}

	// Bytes nobody can be blamed for go to the short connections.
	c.Tick(t0.Add(3*time.Second), &clash.Snapshot{UploadTotal: 5075 + 1, DownloadTotal: 52507 + 900, Connections: []clash.Conn{
		conn("a", "chrome", 151, 1500),
		conn("c", "git", 5, 7),
	}})
	c.Tick(t0.Add(4*time.Second), &clash.Snapshot{UploadTotal: 5076, DownloadTotal: 53407, Connections: []clash.Conn{
		conn("a", "chrome", 151, 1500),
		conn("c", "git", 5, 7),
	}})
	if got := s.byApp[AppShortConn]; got.Down != 900 {
		t.Fatalf("short connections got %+v", got)
	}
}

// The core's totals and its connections' counters are read at different
// moments: a skew one snapshot takes back is not counted (review #8).
func TestSkewIsNotCounted(t *testing.T) {
	s := &sink{}
	c := New(s)
	c.Tick(t0, &clash.Snapshot{UploadTotal: 0, DownloadTotal: 1000, Connections: []clash.Conn{conn("a", "x", 0, 100)}})
	c.Tick(t0.Add(time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{conn("a", "x", 0, 200)}})
	c.Tick(t0.Add(2*time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{conn("a", "x", 0, 300)}})
	c.Tick(t0.Add(3*time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{conn("a", "x", 0, 300)}})
	// 100 counted at first sight, then exactly the core's 200.
	if s.total.Down != 300 {
		t.Fatalf("counted %d, want 300", s.total.Down)
	}
}

func TestCoreRestart(t *testing.T) {
	s := &sink{}
	c := New(s)
	c.Tick(t0, &clash.Snapshot{UploadTotal: 1000, DownloadTotal: 1000, Connections: []clash.Conn{conn("a", "x", 100, 100)}})
	// Totals went down: a new core. Its connections count in full.
	c.Tick(t0.Add(time.Second), &clash.Snapshot{UploadTotal: 30, DownloadTotal: 30, Connections: []clash.Conn{conn("z", "x", 30, 30)}})
	if s.total.Up != 130 || s.total.Conns != 2 {
		t.Fatalf("%+v", s.total)
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := &sink{}
	c := New(s)
	c.Tick(t0, &clash.Snapshot{UploadTotal: 1000, DownloadTotal: 1000, Connections: []clash.Conn{conn("a", "x", 100, 100)}})
	if err := c.Save(path, 7); err != nil {
		t.Fatal(err)
	}
	// The next run counts only what a carried since.
	s2 := &sink{}
	c2 := New(s2)
	if err := c2.Load(path, 7); err != nil {
		t.Fatal(err)
	}
	c2.Tick(t0.Add(time.Minute), &clash.Snapshot{UploadTotal: 2000, DownloadTotal: 2000, Connections: []clash.Conn{conn("a", "x", 150, 400), conn("b", "y", 1, 1)}})
	if s2.byApp["x"] != (store.Counter{Up: 50, Down: 300}) || s2.byApp["y"].Conns != 1 {
		t.Fatalf("%+v", s2.byApp)
	}
}

func TestLAN(t *testing.T) {
	c := New(&sink{})
	c.refreshLocals(time.Now())
	if c.isLAN("127.0.0.1") || c.isLAN("198.18.0.1") {
		t.Fatal("loopback or tun counted as LAN")
	}
	if !c.isLAN("10.255.255.254") {
		t.Fatal("private address not LAN")
	}
}

func TestOpenBeforeCounting(t *testing.T) {
	s := &sink{}
	c := New(s)
	old := conn("old", "steam", 7<<30, 7<<30)
	old.Start = time.Now().Add(-5 * time.Hour)
	now := time.Now()
	c.Tick(now, &clash.Snapshot{UploadTotal: 20 << 30, DownloadTotal: 20 << 30, Connections: []clash.Conn{old}})
	if s.total != (store.Counter{}) {
		t.Fatalf("counted the past of an old connection: %+v", s.total)
	}
	old.Download += 100
	c.Tick(now.Add(time.Second), &clash.Snapshot{UploadTotal: 20 << 30, DownloadTotal: 20<<30 + 100, Connections: []clash.Conn{old}})
	if s.total != (store.Counter{Down: 100}) {
		t.Fatalf("%+v", s.total)
	}
}

func TestSessions(t *testing.T) {
	var got []sessions.Session
	c := New(&sink{})
	c.OnClose = func(s sessions.Session) { got = append(got, s) }
	a := conn("a", "chrome", 10, 100)
	a.Rule, a.RulePayload = "DomainSuffix", "example.com"
	a.Metadata.DestinationIP, a.Metadata.DestinationPort = "1.2.3.4", "443"
	c.Tick(t0, &clash.Snapshot{UploadTotal: 110, DownloadTotal: 1100, Connections: []clash.Conn{a, conn("b", "curl", 1, 1)}})
	a.Upload, a.Download = 20, 300
	c.Tick(t0.Add(time.Second), &clash.Snapshot{UploadTotal: 121, DownloadTotal: 1301, Connections: []clash.Conn{a}})
	if len(got) != 1 || got[0].ID != "b" || got[0].Open {
		t.Fatalf("closed: %+v", got)
	}
	open := c.OpenSessions()
	if len(open) != 1 {
		t.Fatalf("open: %+v", open)
	}
	s := open[0]
	if !s.Open || s.Up != 20 || s.Down != 300 || s.Dest != "1.2.3.4:443" || s.Rule != "DomainSuffix(example.com)" ||
		s.Chains != "Proxy → HK" || s.Route != store.RouteProxy || s.End != t0.Add(time.Second).UnixMilli() || s.Start != t0.UnixMilli() {
		t.Fatalf("open session %+v", s)
	}
}

func TestSubscriptions(t *testing.T) {
	s := &sink{}
	c := New(s)
	c.CurrentSub = func() string { return "蜂鸟云" }
	direct := conn("d", "steam", 0, 50)
	direct.Chains = []string{"DIRECT"}
	provided := conn("p", "git", 0, 20)
	provided.ProviderChains = []string{"机场B", ""}
	c.Tick(t0, &clash.Snapshot{UploadTotal: 0, DownloadTotal: 170, Connections: []clash.Conn{conn("a", "chrome", 0, 100), direct, provided}})
	if s.bySub["蜂鸟云"].Down != 100 || s.bySub[""].Down != 50 || s.bySub["机场B"].Down != 20 {
		t.Fatalf("by subscription: %+v", s.bySub)
	}
}

// A checkpoint the store moved past is not used (review #2), and its time
// is the snapshot's, not the save's (review #9).
func TestCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	c := New(&sink{})
	snapAt := time.Now().Add(-10 * time.Second)
	c.Tick(snapAt, &clash.Snapshot{UploadTotal: 100, DownloadTotal: 100, Connections: []clash.Conn{conn("a", "x", 100, 100)}})
	if err := c.Save(path, 3); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var sv saved
	json.Unmarshal(b, &sv)
	if sv.Saved != snapAt.UnixMilli() || sv.Gen != 3 {
		t.Fatalf("saved %+v", sv)
	}

	stale := New(&sink{})
	if err := stale.Load(path, 4); !errors.Is(err, ErrStaleCheckpoint) {
		t.Fatalf("stale checkpoint loaded: %v", err)
	}
	// Without it, connections open before the run count from now on.
	s := &sink{}
	stale = New(s)
	stale.Load(path, 4)
	old := conn("a", "x", 500, 500)
	old.Start = time.Now().Add(-time.Hour)
	stale.Tick(time.Now(), &clash.Snapshot{UploadTotal: 600, DownloadTotal: 600, Connections: []clash.Conn{old}})
	if s.total != (store.Counter{}) {
		t.Fatalf("counted twice: %+v", s.total)
	}
}

// restart saves a collector's state and loads it into a new one, as a quit
// and a start of the app do.
func restart(t *testing.T, c *Collector, sink Sink) *Collector {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := c.Save(path, 1); err != nil {
		t.Fatal(err)
	}
	n := New(sink)
	if err := n.Load(path, 1); err != nil {
		t.Fatal(err)
	}
	return n
}

// A skew waiting for a second snapshot at quit is not counted twice: the
// state saved goes on after a quick restart (review R2).
func TestSkewAcrossRestart(t *testing.T) {
	s := &sink{}
	c := New(s)
	at := time.Now()
	a := conn("a", "x", 0, 100)
	c.Tick(at, &clash.Snapshot{DownloadTotal: 1000, Connections: []clash.Conn{a}})
	// The core counted 100 more than the connection showed yet.
	a.Download = 200
	c.Tick(at.Add(time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{a}})
	c = restart(t, c, s)
	a.Download = 300
	c.Tick(at.Add(5*time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{a}})
	c.Tick(at.Add(6*time.Second), &clash.Snapshot{DownloadTotal: 1200, Connections: []clash.Conn{a}})
	// 100 at first sight, then exactly the core's 200.
	if s.total.Down != 300 {
		t.Fatalf("counted %d, want 300", s.total.Down)
	}
}

// What a closed connection carried after the last snapshot before a quit
// still goes to it after a quick restart (review B4, R2).
func TestClosedTailAcrossRestart(t *testing.T) {
	s := &sink{}
	c := New(s)
	at := time.Now()
	a, b := conn("a", "x", 0, 100), conn("b", "y", 0, 50)
	c.Tick(at, &clash.Snapshot{DownloadTotal: 1000, Connections: []clash.Conn{a, b}})
	// b closed with 500 more the snapshots never saw.
	c.Tick(at.Add(time.Second), &clash.Snapshot{DownloadTotal: 1500, Connections: []clash.Conn{a}})
	c = restart(t, c, s)
	c.Tick(at.Add(10*time.Second), &clash.Snapshot{DownloadTotal: 1500, Connections: []clash.Conn{a}})
	if got := s.byApp["y"].Down; got != 550 {
		t.Fatalf("b got %d, want 550", got)
	}
	// After a long restart the saved difference is dropped, not guessed at.
	s2 := &sink{}
	c2 := New(s2)
	c2.Tick(at, &clash.Snapshot{DownloadTotal: 1000, Connections: []clash.Conn{a, b}})
	c2.Tick(at.Add(time.Second), &clash.Snapshot{DownloadTotal: 1500, Connections: []clash.Conn{a}})
	c2 = restart(t, c2, s2)
	c2.Tick(at.Add(2*time.Hour), &clash.Snapshot{DownloadTotal: 9000, Connections: []clash.Conn{a}})
	c2.Tick(at.Add(2*time.Hour+time.Second), &clash.Snapshot{DownloadTotal: 9000, Connections: []clash.Conn{a}})
	if s2.total.Down != 150 {
		t.Fatalf("after a long restart counted %d, want only the 150 seen", s2.total.Down)
	}
}

// After a long gap, a connection still open shows the core is the same one,
// whose totals then count; without one they do not (review B4).
func TestResume(t *testing.T) {
	for _, same := range []bool{true, false} {
		s := &sink{}
		c := New(s)
		c.Connected("unix:///core.sock")
		c.Tick(t0, &clash.Snapshot{DownloadTotal: 1000, Connections: []clash.Conn{conn("a", "x", 0, 100), conn("b", "y", 0, 50)}})
		c.Connected("unix:///core.sock") // five minutes without the controller
		after := t0.Add(5 * time.Minute)
		open := conn("z", "w", 0, 10)
		if same {
			open = conn("a", "x", 0, 110)
		}
		c.Tick(after, &clash.Snapshot{DownloadTotal: 1000 + 10 + 2000, Connections: []clash.Conn{open}})
		c.Tick(after.Add(time.Second), &clash.Snapshot{DownloadTotal: 3010, Connections: []clash.Conn{open}})
		got := s.byApp["y"].Down
		if same && got != 50+2000 {
			t.Errorf("same core: b got %d, want the 2000 it closed with", got)
		}
		if !same && got != 50 {
			t.Errorf("another core: b got %d from totals that are not its", got)
		}
	}
}

// Snapshots are numbered, whatever the clock does (review R8).
func TestSnapshotOrder(t *testing.T) {
	c := New(&sink{})
	a := c.Tick(t0, &clash.Snapshot{})
	b := c.Tick(t0.Add(-time.Hour), &clash.Snapshot{}) // the clock went back
	if b.Seq <= a.Seq || a.Run == "" || a.Run != b.Run || New(&sink{}).run == c.run {
		t.Fatalf("a %d %q, b %d %q", a.Seq, a.Run, b.Seq, b.Run)
	}
}

// The app's own order: load the checkpoint, connect, then snapshot. A quick
// restart against the same controller goes on with what was saved; another
// controller starts over (review B1 of round four).
func TestFirstConnectionAfterRestart(t *testing.T) {
	for _, ep := range []string{"unix:///core.sock", "http://127.0.0.1:9090"} {
		s := &sink{}
		c := New(s)
		c.Connected("unix:///core.sock")
		at := time.Now()
		a, b := conn("a", "x", 0, 100), conn("b", "y", 0, 50)
		c.Tick(at, &clash.Snapshot{DownloadTotal: 1000, Connections: []clash.Conn{a, b}})
		// b closed with 500 more the snapshots never saw; the app quits.
		c.Tick(at.Add(time.Second), &clash.Snapshot{DownloadTotal: 1500, Connections: []clash.Conn{a}})
		path := filepath.Join(t.TempDir(), "state.json")
		if err := c.Save(path, 1); err != nil {
			t.Fatal(err)
		}

		n := New(s) // Core.Init
		if err := n.Load(path, 1); err != nil {
			t.Fatal(err)
		}
		n.Connected(ep) // the poll loop's first connection
		n.Tick(at.Add(10*time.Second), &clash.Snapshot{DownloadTotal: 1500, Connections: []clash.Conn{a}})
		got := s.byApp["y"].Down
		if ep == "unix:///core.sock" && got != 550 {
			t.Errorf("same controller: b got %d, want 550", got)
		}
		if ep != "unix:///core.sock" && got != 50 {
			t.Errorf("another controller: b got %d, want 50", got)
		}
	}
}
