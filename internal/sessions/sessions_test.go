package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"clashtraffic/internal/store"
)

func TestLog(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	min := int64(60_000)
	l.Add(Session{ID: "1", Start: now - 10*min, End: now - 9*min, App: "Chrome", Host: "www.google.com", Chain: "HK", Route: store.RouteProxy, Down: 100})
	l.Add(Session{ID: "2", Start: now - 8*min, End: now - 7*min, App: "Steam", Host: "cdn.steam.com", Chain: "DIRECT", Route: store.RouteDirect, Down: 900})
	// Unwritten sessions are found too.
	if p := l.Query(SessionQuery{From: now - time.Hour.Milliseconds(), To: now}, nil); p.Total != 2 {
		t.Fatalf("pending: %+v", p)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	// A second flush appends another gzip member to the same day.
	l.Add(Session{ID: "3", Start: now - 6*min, End: now - 5*min, App: "Chrome", Host: "mail.google.com", Chain: "HK", Route: store.RouteProxy, Up: 5, Down: 50, Open: true})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	// The app restarted and saw 3 close later: the later record wins.
	l.Add(Session{ID: "3", Start: now - 6*min, End: now - 1*min, App: "Chrome", Host: "mail.google.com", Chain: "HK", Route: store.RouteProxy, Up: 9, Down: 70})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}

	l2, _ := Open(dir)
	q := SessionQuery{From: now - time.Hour.Milliseconds(), To: now}
	open := []Session{{ID: "4", Start: now - min, End: now, Open: true, App: "curl", Host: "example.com", Chain: "DIRECT", Route: store.RouteDirect}}
	p := l2.Query(q, open)
	if p.Total != 4 || p.Items[0].ID != "4" || p.Items[3].ID != "1" || p.Down != 1070 {
		t.Fatalf("all: total %d down %d items %+v", p.Total, p.Down, p.Items)
	}
	for _, s := range p.Items {
		if s.ID == "3" && (s.Open || s.Down != 70) {
			t.Fatalf("kept the stale record of 3: %+v", s)
		}
	}

	q.Route = store.RouteProxy
	q.Host, q.GroupDomain = "google.com", true
	if p = l2.Query(q, open); p.Total != 2 {
		t.Fatalf("proxy google.com: %+v", p)
	}
	q = SessionQuery{From: now - time.Hour.Milliseconds(), To: now, Search: "STEAM"}
	if p = l2.Query(q, open); p.Total != 1 || p.Items[0].App != "Steam" {
		t.Fatalf("search: %+v", p)
	}
	q = SessionQuery{From: now - time.Hour.Milliseconds(), To: now, Offset: 3, Limit: 2}
	if p = l2.Query(q, open); p.Total != 4 || len(p.Items) != 1 {
		t.Fatalf("paging: %+v", p)
	}
	// Ranges that end before a session started do not see it.
	q = SessionQuery{From: now - time.Hour.Milliseconds(), To: now - 9*min - 1}
	if p = l2.Query(q, open); p.Total != 1 {
		t.Fatalf("range: %+v", p)
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	old := time.Now().AddDate(0, 0, -40)
	l.Add(Session{ID: "old", Start: old.UnixMilli(), End: old.UnixMilli()})
	l.Add(Session{ID: "new", Start: time.Now().UnixMilli(), End: time.Now().UnixMilli()})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := l.Cleanup(30); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
	if len(files) != 1 {
		t.Fatalf("files left: %v", files)
	}
	// A file cut short by a crash still reads up to the cut.
	b, _ := os.ReadFile(files[0])
	if err := os.WriteFile(files[0], append(b, b[:len(b)/2]...), 0o600); err != nil {
		t.Fatal(err)
	}
	l2, _ := Open(dir)
	if p := l2.Query(SessionQuery{From: 0, To: time.Now().UnixMilli() + 1}, nil); p.Total != 1 {
		t.Fatalf("after a torn write: %+v", p)
	}
}

// A crash in the middle of a write cuts a member short; what is appended
// after it must still be read (review #10).
func TestAppendAfterTornWrite(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	l, _ := Open(dir)
	l.Add(Session{ID: "1", Start: now, End: now})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
	b, _ := os.ReadFile(files[0])
	os.WriteFile(files[0], append(b, b[:len(b)/2]...), 0o600) // the torn member

	// The next run appends to the same day.
	l2, _ := Open(dir)
	l2.Add(Session{ID: "2", Start: now, End: now})
	if err := l2.Flush(); err != nil {
		t.Fatal(err)
	}
	l3, _ := Open(dir)
	if p := l3.Query(SessionQuery{From: now - 1000, To: now + 1000}, nil); p.Total != 2 {
		t.Fatalf("after the torn write: %+v", p)
	}
}

// A connection that lasted days is in the file of the day it ended, and
// still shows on the days it was open (review #11).
func TestLongSession(t *testing.T) {
	l, _ := Open(t.TempDir())
	end := time.Now()
	start := end.AddDate(0, 0, -4)
	l.Add(Session{ID: "long", Start: start.UnixMilli(), End: end.UnixMilli()})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	q := SessionQuery{From: start.UnixMilli(), To: start.Add(time.Hour).UnixMilli()}
	if p := l.Query(q, nil); p.Total != 1 {
		t.Fatalf("the long session is missing: %+v", p)
	}
}

// The page shows some names in the user's language and finds them by it:
// SearchAlso spells them as the data does (review 9, P2-1).
func TestSearchAlso(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	l.Add(Session{ID: "1", Start: now - 120_000, End: now - 60_000, App: "未识别进程", Host: "a.example.com", Route: store.RouteDirect})
	l.Add(Session{ID: "2", Start: now - 120_000, End: now - 60_000, App: "Chrome", Host: "局域网 192.168.1.5", Route: store.RouteDirect})
	l.Add(Session{ID: "3", Start: now - 120_000, End: now - 60_000, App: "Chrome", Host: "b.example.com", Route: store.RouteDirect})
	q := SessionQuery{From: now - time.Hour.Milliseconds(), To: now, Search: "unidentified"}
	if p := l.Query(q, nil); p.Total != 0 {
		t.Fatalf("without the alias: %+v", p)
	}
	q.SearchAlso = []string{"未识别进程"}
	if p := l.Query(q, nil); p.Total != 1 || p.Items[0].ID != "1" {
		t.Fatalf("with the alias: %+v", p)
	}
	q.Search, q.SearchAlso = "lan 192.168", []string{"局域网 192.168"}
	if p := l.Query(q, nil); p.Total != 1 || p.Items[0].ID != "2" {
		t.Fatalf("LAN: %+v", p)
	}
}
