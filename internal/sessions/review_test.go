package sessions

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"clashtraffic/internal/store"
)

type failingReader struct {
	r     io.Reader
	after int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("input/output error")
	}
	if len(p) > f.after {
		p = p[:f.after]
	}
	n, err := f.r.Read(p)
	f.after -= n
	return n, err
}

// A read that fails is no damage: the file is left alone, and the append
// waits (review B2). Damage is repaired, keeping the damaged file.
func TestRepairOnlyDamage(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	l, _ := Open(dir)
	for i := range 50 {
		l.Add(Session{ID: fmt.Sprint("s", i), Start: now, End: now, Host: "example.com"})
		l.Flush()
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
	before, _ := os.ReadFile(files[0])

	saved := openFile
	openFile = func(path string) (io.ReadCloser, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{&failingReader{r: f, after: 100}, f}, nil
	}
	l2, _ := Open(dir)
	l2.Add(Session{ID: "new", Start: now, End: now})
	err := l2.Flush()
	openFile = saved
	if err == nil {
		t.Fatal("appended past a file it could not read")
	}
	after, _ := os.ReadFile(files[0])
	if string(before) != string(after) {
		t.Fatal("a read error rewrote the file")
	}
	// Readable again: the record waiting is written.
	if err := l2.Flush(); err != nil {
		t.Fatal(err)
	}
	if p := l2.Query(SessionQuery{From: now - 1000, To: now + 1000}, nil); p.Total != 51 {
		t.Fatalf("total %d", p.Total)
	}

	// Damage: repaired, the damaged file kept.
	os.WriteFile(files[0], before[:len(before)-20], 0o600)
	l3, _ := Open(dir)
	l3.Add(Session{ID: "later", Start: now, End: now})
	if err := l3.Flush(); err != nil {
		t.Fatal(err)
	}
	if kept, _ := filepath.Glob(files[0] + ".damaged-*"); len(kept) != 1 {
		t.Fatalf("damaged file not kept: %v", kept)
	}
}

// A write that fails half way is taken back off, so what is appended next
// reads (review B6).
func TestFailedAppendIsTakenBack(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	l, _ := Open(dir)
	l.Add(Session{ID: "1", Start: now, End: now})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	saved := writeMember
	writeMember = func(w io.Writer, list []Session) error {
		w.Write([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00}) // half a member: the disk filled up
		return errors.New("no space left on device")
	}
	l.Add(Session{ID: "2", Start: now, End: now})
	err := l.Flush()
	writeMember = saved
	if err == nil {
		t.Fatal("the failed write was not reported")
	}
	l.Add(Session{ID: "3", Start: now, End: now})
	if err := l.Flush(); err != nil { // retried in the same run
		t.Fatal(err)
	}
	l2, _ := Open(dir)
	if p := l2.Query(SessionQuery{From: now - 1000, To: now + 1000}, nil); p.Total != 3 {
		t.Fatalf("total %d, want 3", p.Total)
	}
}

// Recording never waits for a query, days after the range are skipped
// unless a session of theirs started in time, and the cache keeps to its
// cap (review B9).
func TestQueryCost(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir)
	day := 24 * time.Hour
	base := time.Now().Add(-10 * day)
	for i := range 10 {
		at := base.Add(time.Duration(i) * day).UnixMilli()
		for j := range 30 {
			l.Add(Session{ID: string(rune('A'+i)) + string(rune('a'+j)), Start: at, End: at})
		}
	}
	long := Session{ID: "long", Start: base.UnixMilli(), End: base.Add(5 * day).UnixMilli()}
	l.Add(long)
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}

	l.mu.Lock() // a query reading files
	done := make(chan bool)
	go func() {
		l.Add(Session{ID: "x", Start: 1, End: 1})
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("recording waited for a query")
	}
	l.mu.Unlock()

	q := SessionQuery{From: base.UnixMilli(), To: base.Add(time.Hour).UnixMilli()}
	if p := l.Query(q, nil); p.Total != 31 {
		t.Fatalf("total %d, want the day's 30 and the long one", p.Total)
	}
	l.mu.Lock()
	decoded := len(l.cache)
	l.mu.Unlock()
	if decoded != 2 { // the day itself, and the day the long session ended
		t.Fatalf("decoded %d days for one", decoded)
	}

	saved := MaxCached
	MaxCached = 70
	defer func() { MaxCached = saved }()
	l2, _ := Open(dir)
	l2.Query(SessionQuery{From: base.UnixMilli(), To: time.Now().UnixMilli()}, nil)
	if l2.cached > MaxCached {
		t.Fatalf("%d sessions cached, cap %d", l2.cached, MaxCached)
	}
}

// A connection open when the app quit, and not open now, is not shown as
// open (review C1).
func TestOpenAtQuit(t *testing.T) {
	l, _ := Open(t.TempDir())
	now := time.Now().UnixMilli()
	l.Add(Session{ID: "q", Start: now - 1000, End: now, Open: true})
	l.Flush()
	q := SessionQuery{From: now - 5000, To: now + 5000}
	p := l.Query(q, nil)
	if p.Total != 1 || p.Items[0].Open || !p.Items[0].Cut {
		t.Fatalf("%+v", p.Items)
	}
	// Still open now: the live one shows.
	p = l.Query(q, []Session{{ID: "q", Start: now - 1000, End: now + 3000, Open: true}})
	if p.Total != 1 || !p.Items[0].Open || p.Items[0].Cut {
		t.Fatalf("%+v", p.Items)
	}
}

// A repair that fails half way leaves the file as it was, and is done
// again (review R3).
func TestRepairFailingHalfWay(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	l, _ := Open(dir)
	for i := range 20 {
		l.Add(Session{ID: fmt.Sprint("s", i), Start: now, End: now})
		l.Flush()
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
	whole, _ := os.ReadFile(files[0])
	torn := whole[:len(whole)-10]
	os.WriteFile(files[0], torn, 0o600)

	saved := replaceFile
	replaceFile = func(string, []byte) error { return errors.New("no space left on device") }
	l2, _ := Open(dir)
	l2.Add(Session{ID: "new", Start: now, End: now})
	err := l2.Flush()
	replaceFile = saved
	if err == nil {
		t.Fatal("the failed repair was not reported")
	}
	if b, _ := os.ReadFile(files[0]); string(b) != string(torn) {
		t.Fatal("the file changed although the repair failed")
	}
	if err := l2.Flush(); err != nil { // space again
		t.Fatal(err)
	}
	if p := l2.Query(SessionQuery{From: now - 1000, To: now + 1000}, nil); p.Total < 20 {
		t.Fatalf("history lost: %d sessions", p.Total)
	}
}

// A batch tried again after closing the file failed is kept once (review R4).
func TestBatchWrittenTwice(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	l, _ := Open(dir)
	saved := closeFile
	closeFile = func(f *os.File) error { f.Close(); return errors.New("input/output error") }
	l.Add(Session{ID: "a", Start: now, End: now})
	l.Add(Session{ID: "b", Start: now, End: now})
	err := l.Flush()
	closeFile = saved
	if err == nil {
		t.Fatal("the failed close was not reported")
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	q := SessionQuery{From: now - 1000, To: now + 1000}
	if p := l.Query(q, nil); p.Total != 2 {
		t.Fatalf("total %d, want 2", p.Total)
	}
	// Even a batch that did reach the file twice reads once.
	day := store.DayOf(time.UnixMilli(now))
	l.appendDay(day, []Session{{ID: "a", Start: now, End: now}, {ID: "b", Start: now, End: now}})
	l2, _ := Open(dir)
	if p := l2.Query(q, nil); p.Total != 2 {
		t.Fatalf("after a double write: total %d, want 2", p.Total)
	}
}

// A query and a flush side by side see each session once (review R5).
func TestQueryDuringFlush(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	q := SessionQuery{From: now - 1000, To: now + 1000}

	// The flush writes after the query took the sessions in memory.
	l, _ := Open(dir)
	l.Add(Session{ID: "s", Start: now, End: now})
	queryHook = func() { queryHook = nil; l.Flush() }
	if p := l.Query(q, nil); p.Total != 1 {
		t.Fatalf("counted %d times", p.Total)
	}

	// The query runs while the flush writes.
	l2, _ := Open(t.TempDir())
	l2.Add(Session{ID: "t", Start: now, End: now})
	saved := writeMember
	var seen int
	writeMember = func(w io.Writer, list []Session) error {
		seen = l2.Query(q, nil).Total
		return saved(w, list)
	}
	err := l2.Flush()
	writeMember = saved
	if err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("a query during the write saw %d", seen)
	}
}

// The clock set back across days puts a connection's closing record in a
// file dated before the record of it open at a quit: it is still one
// closed connection (round four, C1).
func TestCloseFiledBeforeQuit(t *testing.T) {
	l, _ := Open(t.TempDir())
	day := 24 * time.Hour
	start := time.Now().Add(-3 * day)
	// The app quit with the clock a day ahead...
	l.Add(Session{ID: "c", Start: start.UnixMilli(), End: start.Add(2 * day).UnixMilli(), Open: true, Down: 10})
	// ...then, the clock set right, saw it close.
	l.Add(Session{ID: "c", Start: start.UnixMilli(), End: start.Add(day).UnixMilli(), Down: 30})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	p := l.Query(SessionQuery{From: start.Add(-time.Hour).UnixMilli(), To: time.Now().UnixMilli()}, nil)
	if p.Total != 1 || p.Items[0].Cut || p.Down != 30 {
		t.Fatalf("total %d down %d items %+v", p.Total, p.Down, p.Items)
	}
}

// A query over a long range holds the cache and one day, not the range
// (round five, B1).
func TestQueryMemory(t *testing.T) {
	saved := MaxCached
	MaxCached = 1000
	defer func() { MaxCached = saved }()
	dir := t.TempDir()
	l, _ := Open(dir)
	day := 24 * time.Hour
	base := time.Now().Add(-21 * day)
	for d := range 20 {
		at := base.Add(time.Duration(d) * day).UnixMilli()
		for i := range 4000 {
			l.Add(Session{ID: fmt.Sprint(d, "-", i), Start: at, End: at, App: "Google Chrome", Process: "Google Chrome Helper",
				Host: fmt.Sprint("host-", i, ".googleusercontent.com"), Dest: "142.250.0.1:443", Chains: "Proxy → HK 01", Rule: "Match"})
		}
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	l2, _ := Open(dir)
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	baseline := heap()
	var peak uint64
	queryDayHook = func() { peak = max(peak, heap()) }
	defer func() { queryDayHook = nil }()
	p := l2.Query(SessionQuery{From: base.UnixMilli(), To: time.Now().UnixMilli(), Search: "host-1."}, nil)
	if p.Total != 20 {
		t.Fatalf("total %d", p.Total)
	}
	// All 80,000 sessions take some 40 MB; one day, some 2 MB.
	if grew := int64(peak) - int64(baseline); grew > 12<<20 {
		t.Fatalf("the query held %d MB", grew>>20)
	}
}
