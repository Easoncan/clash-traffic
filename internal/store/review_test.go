package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Queries read a day the collector is adding to (review #1).
func TestQueryWhileAdding(t *testing.T) {
	s, _ := Open(t.TempDir(), DeviceInfo{ID: "a", Name: "A"})
	h := time.Now().Unix()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 2000 {
			s.Add(Key{Hour: h, App: fmt.Sprint("app", i)}, "/path", Counter{Down: 1})
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			s.Query(Query{From: (h - 3600) * 1000, To: (h + 3600) * 1000})
		}
	}()
	wg.Wait()
}

// A day file that cannot be read is kept, not overwritten (review #5).
func TestUnreadableDayIsKept(t *testing.T) {
	root := t.TempDir()
	h := hourOf(time.Now())
	path := filepath.Join(root, "a", DayOf(time.Unix(h, 0))+DayExt)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 5})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	aside, _ := filepath.Glob(path + ".unreadable-*")
	if len(aside) != 1 {
		t.Fatalf("the unreadable file was not kept: %v", aside)
	}
	if b, _ := os.ReadFile(aside[0]); string(b) != "not gzip" {
		t.Fatalf("kept %q", b)
	}
	if r := s.Query(Query{From: h * 1000, To: (h + 3600) * 1000}); r.Down != 5 {
		t.Fatalf("down %d", r.Down)
	}
}

// Generations order copies of a file (reviews #2 and #6).
func TestGenerationsAndReplace(t *testing.T) {
	root := t.TempDir()
	h := hourOf(time.Now())
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	if s.Gen() != 0 {
		t.Fatalf("gen %d", s.Gen())
	}
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
	s.Flush()
	day := DayOf(time.Unix(h, 0)) + DayExt
	path := filepath.Join(root, "a", day)
	older, _ := os.ReadFile(path)
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
	s.Flush()
	newer, _ := os.ReadFile(path)
	if s.Gen() != 2 {
		t.Fatalf("gen %d", s.Gen())
	}
	// Nothing to write: the generation stays.
	s.Flush()
	if s.Gen() != 2 {
		t.Fatalf("gen moved without writes: %d", s.Gen())
	}
	// A new run goes on from the files' generation.
	if s2, _ := Open(root, DeviceInfo{ID: "a", Name: "A"}); s2.Gen() != 2 {
		t.Fatalf("reopened gen %d", s2.Gen())
	}

	copyPath := filepath.Join(t.TempDir(), day)
	for _, c := range []struct {
		have, in []byte
		want     bool
	}{{nil, older, true}, {older, newer, true}, {newer, older, false}, {newer, newer, false}} {
		os.Remove(copyPath)
		if c.have != nil {
			os.WriteFile(copyPath, c.have, 0o644)
		}
		got, err := ShouldReplace(copyPath, day, c.in)
		if err != nil || got != c.want {
			t.Fatalf("replace = %v, %v; want %v", got, err, c.want)
		}
	}
	if _, err := ShouldReplace(copyPath, day, []byte("garbage")); err == nil {
		t.Fatal("garbage accepted")
	}
}

// Device ids and file names are strict (review #4).
func TestNames(t *testing.T) {
	for id, ok := range map[string]bool{
		"b21be4da6fd8": true, "b21be4da6fd8-dev": true, "B21BE4DA6FD8": false, "b21be4da6fd8.": false,
		"b21be4da6fd": false, "../b21be4da6fd8": false, "": false,
	} {
		if ValidDeviceID(id) != ok {
			t.Errorf("ValidDeviceID(%q) = %v", id, !ok)
		}
	}
	for name, ok := range map[string]bool{
		"device.json": true, "2026-10-13.json.gz": true, "2026-10-13.json.gz.unreadable-1": false,
		"settings.json": false, "../2026-10-13.json.gz": false, "2026-10-13.JSON.GZ": false,
	} {
		if ValidFileName(name) != ok {
			t.Errorf("ValidFileName(%q) = %v", name, !ok)
		}
	}
}

// In a time zone half an hour off UTC, consecutive days do not share a
// bucket (review #15).
func TestHalfHourZone(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("IST", 5*3600+1800)
	defer func() { time.Local = saved }()

	s, _ := Open(t.TempDir(), DeviceInfo{ID: "a", Name: "A"})
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local) // 2026-10-12 18:30 UTC
	for h := day.Add(-3 * time.Hour); h.Before(day.Add(3 * time.Hour)); h = h.Add(time.Hour) {
		s.Add(Key{Hour: h.Unix() - h.Unix()%3600, App: "x"}, "", Counter{Down: 1})
	}
	before := s.Query(Query{From: day.AddDate(0, 0, -1).UnixMilli(), To: day.UnixMilli()})
	after := s.Query(Query{From: day.UnixMilli(), To: day.AddDate(0, 0, 1).UnixMilli()})
	all := s.Query(Query{From: day.AddDate(0, 0, -1).UnixMilli(), To: day.AddDate(0, 0, 1).UnixMilli()})
	if before.Down+after.Down != all.Down || all.Down != 6 {
		t.Fatalf("before %d + after %d != all %d", before.Down, after.Down, all.Down)
	}
	week := s.Query(Query{From: day.AddDate(0, 0, -3).UnixMilli(), To: day.AddDate(0, 0, 3).UnixMilli()})
	var series int64
	for _, p := range week.Series {
		series += p.Down
	}
	if week.Bucket != "day" || series != 6 {
		t.Fatalf("daily series %d", series)
	}
}

// Shares are of the whole list, which ignores its own filter (review #19).
func TestListTotals(t *testing.T) {
	s, _ := Open(t.TempDir(), DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	s.Add(Key{Hour: h, App: "A", Host: "a.com", Chain: "HK"}, "", Counter{Down: 20})
	s.Add(Key{Hour: h, App: "B", Host: "b.com", Chain: "HK"}, "", Counter{Down: 80})
	r := s.Query(Query{From: h * 1000, To: (h + 3600) * 1000, App: "A", Limit: 1})
	if r.Down != 20 || r.AppsTotal != 100 || r.HostsTotal != 20 || len(r.Apps) != 1 {
		t.Fatalf("down %d apps total %d hosts total %d", r.Down, r.AppsTotal, r.HostsTotal)
	}
}

// Copies arriving side by side end with the newest (review B3).
func TestReplaceIfNewerConcurrently(t *testing.T) {
	src := t.TempDir()
	s, _ := Open(src, DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	day := DayOf(time.Unix(h, 0)) + DayExt
	var versions [][]byte
	for range 30 {
		s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
		s.Flush()
		b, _ := os.ReadFile(filepath.Join(src, "a", day))
		versions = append(versions, b)
	}
	for round := range 20 {
		dst := filepath.Join(t.TempDir(), day)
		var wg sync.WaitGroup
		for i := range versions {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := ReplaceIfNewer(dst, day, versions[(i*7+round)%len(versions)]); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		f, err := readDayFile(dst)
		if err != nil || f.Gen != 30 {
			t.Fatalf("round %d ended with gen %d (%v), want 30", round, f.Gen, err)
		}
	}
}

// The generation survives days dated out of order, as after the clock was
// set ahead and back (review B7).
func TestGenAfterClockWentBack(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	for d := 5; d <= 8; d++ { // written while the clock was days ahead
		h := hourOf(time.Now().AddDate(0, 0, d))
		s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
		s.Flush()
	}
	h := hourOf(time.Now())
	for range 10 { // then today, after the clock was set right
		s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
		s.Flush()
	}
	if s.Gen() != 14 {
		t.Fatalf("gen %d", s.Gen())
	}
	if s2, _ := Open(root, DeviceInfo{ID: "a", Name: "A"}); s2.Gen() != 14 {
		t.Fatalf("reopened at gen %d, want 14", s2.Gen())
	}
}

// Renaming moves the device file's time on, and a copy with the same time
// is the same file (review B8).
func TestDeviceFileVersions(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "Old"})
	path := filepath.Join(root, "a", DeviceFile)
	old, _ := os.ReadFile(path)
	if err := s.Rename("New"); err != nil {
		t.Fatal(err)
	}
	renamed, _ := os.ReadFile(path)
	if err := s.SetSubs([]SubInfo{{Name: "A"}}); err != nil {
		t.Fatal(err)
	}
	withSubs, _ := os.ReadFile(path)

	copyPath := filepath.Join(t.TempDir(), DeviceFile)
	os.WriteFile(copyPath, withSubs, 0o644)
	for _, b := range [][]byte{old, renamed} {
		if ok, _ := ShouldReplace(copyPath, DeviceFile, b); ok {
			t.Fatalf("an older device file replaces a newer: %s", b)
		}
	}
	// Same time, other content: not a newer copy.
	var d DeviceInfo
	json.Unmarshal(withSubs, &d)
	d.Name = "Forged"
	forged, _ := json.Marshal(d)
	if ok, _ := ShouldReplace(copyPath, DeviceFile, forged); ok {
		t.Fatal("a device file with the same time replaced it")
	}
}

// A file cut after its JSON fails its checksum, and a small file that
// decompresses past the cap is refused (reviews C2, C3).
func TestDayFileIntegrity(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
	s.Flush()
	day := DayOf(time.Unix(h, 0)) + DayExt
	whole, _ := os.ReadFile(filepath.Join(root, "a", day))
	if _, err := decodeDay(bytes.NewReader(whole), RemoteLimits); err != nil {
		t.Fatal(err)
	}
	// The gzip trailer: checksum and size.
	if _, err := decodeDay(bytes.NewReader(whole[:len(whole)-8]), RemoteLimits); err == nil {
		t.Fatal("a file without its checksum passed")
	}
	bad := append([]byte(nil), whole...)
	bad[len(bad)-6] ^= 0xff
	if _, err := decodeDay(bytes.NewReader(bad), RemoteLimits); err == nil {
		t.Fatal("a wrong checksum passed")
	}

	saved := RemoteLimits
	RemoteLimits.Bytes = 1 << 20
	defer func() { RemoteLimits = saved }()
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	zw.Write([]byte(`{"v":2,"device":"a","day":"2026-10-13","strings":["`))
	zw.Write(bytes.Repeat([]byte("a"), 4<<20))
	zw.Write([]byte(`"],"rows":[]}`))
	zw.Close()
	if bomb.Len() > 64<<10 {
		t.Fatalf("the bomb is %d bytes compressed", bomb.Len())
	}
	if _, err := ReplaceIfNewer(filepath.Join(t.TempDir(), day), day, bomb.Bytes()); err == nil {
		t.Fatal("a file decompressing past the cap was accepted")
	}
}

// A crash after the device file and before a day file leaves the store a
// generation ahead of the checkpoint, which then is not used (review R1).
func TestCrashBetweenDeviceAndDay(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	checkpoint := s.Gen() // what the collector saved with
	// The next day write fails, as a crash right after the device file would.
	path := filepath.Join(root, "a", DayOf(time.Unix(h, 0))+DayExt)
	os.Remove(path)
	os.MkdirAll(filepath.Join(path, "x"), 0o755)
	s.Add(Key{Hour: h, App: "x"}, "", Counter{Down: 1})
	if err := s.Flush(); err == nil {
		t.Fatal("the day write did not fail")
	}
	s2, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	if s2.Gen() == checkpoint {
		t.Fatalf("store gen %d equals the checkpoint's: it would be used", s2.Gen())
	}
}

// This device's own files are not held to the limits of others' (review R6).
func TestLocalFilesHaveNoLimits(t *testing.T) {
	saved := RemoteLimits
	RemoteLimits.Bytes = 2 << 10
	defer func() { RemoteLimits = saved }()
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	for i := range 500 {
		s.Add(Key{Hour: h, App: fmt.Sprint("app", i), Host: fmt.Sprint("host", i, ".example.com")}, "", Counter{Down: 1})
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	if r := s2.Query(Query{From: h * 1000, To: (h + 3600) * 1000}); r.Down != 500 {
		t.Fatalf("reopened: down %d", r.Down)
	}
	day := DayOf(time.Unix(h, 0)) + DayExt
	b, _ := os.ReadFile(filepath.Join(root, "a", day))
	if _, err := ReplaceIfNewer(filepath.Join(t.TempDir(), day), day, b); err == nil {
		t.Fatal("over the limit, yet accepted from another device")
	}
}

func gz(t *testing.T, parts ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	for _, p := range parts {
		zw.Write([]byte(p))
	}
	zw.Close()
	return b.Bytes()
}

// Rows and strings are counted as they decode, before memory is taken
// (review R7).
func TestStructureLimits(t *testing.T) {
	saved := RemoteLimits
	RemoteLimits.Rows, RemoteLimits.Strings = 1000, 1000
	defer func() { RemoteLimits = saved }()
	head := `{"v":2,"device":"a","day":"2026-10-13","gen":1,`
	empty := strings.Repeat("[],", 50_000)
	for name, data := range map[string][]byte{
		"rows":    gz(t, head, `"strings":[],"rows":[`, empty, `[]]}`),
		"strings": gz(t, head, `"strings":[`, strings.Repeat(`"x",`, 50_000), `"x"],"rows":[]}`),
		"wide":    gz(t, head, `"strings":[],"rows":[[1,2,3,4,5,6,7,8,9,10]]}`),
		// Each list under the limit, many of them over it (round four, B2).
		"repeated": gz(t, head, `"strings":[],`+strings.Repeat(`"rows":[`+strings.Repeat("[0,0,0,0,0,0,0,0,1],", 999)+`[0,0,0,0,0,0,0,0,1]],`, 3)+`"paths":{}}`),
	} {
		if _, err := decodeDay(bytes.NewReader(data), RemoteLimits); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := gz(t, head, `"strings":["x"],"rows":[[0,0,0,0,0,0,1,2,3]]}`)
	if f, err := decodeDay(bytes.NewReader(ok), RemoteLimits); err != nil || len(f.Rows) != 1 || f.total() != 6 {
		t.Fatalf("a small file: %+v %v", f, err)
	}
}

// Nothing may follow the one gzip member a day file is (review R9).
func TestNothingAfterTheMember(t *testing.T) {
	file := gz(t, `{"v":2,"device":"a","day":"2026-10-13","gen":1,"strings":[],"rows":[]}`)
	if _, err := decodeDay(bytes.NewReader(file), RemoteLimits); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"member":  append(append([]byte(nil), file...), gz(t, `{"evil":true}`)...),
		"garbage": append(append([]byte(nil), file...), 0xde, 0xad),
	} {
		if _, err := decodeDay(bytes.NewReader(data), RemoteLimits); err == nil {
			t.Errorf("%s after the member: accepted", name)
		}
	}
}
