// Package sessions keeps a log of the connections of this computer: when
// each opened and closed, which application made it, where it went and
// through which proxy, and how many bytes it carried.
//
// Closed connections are appended to one file per UTC day, as gzip members
// of JSON lines, so a day's file grows without being rewritten:
//
//	<dir>/2026-10-13.jsonl.gz
//
// The log stays on this computer: it is far larger than the hourly
// counters the devices share.
package sessions

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"clashtraffic/internal/store"
)

const ext = ".jsonl.gz"

// Session is a connection, from when it opened to when it was last seen.
type Session struct {
	ID    string `json:"id"`
	Start int64  `json:"start"` // unix ms
	End   int64  `json:"end"`   // unix ms of the last snapshot that had it
	Open  bool   `json:"open"`  // still open, or open when the app quit
	// Cut sessions were open when the app quit and closed before it ran
	// again: they ended after End, at a time nobody knows.
	Cut     bool        `json:"cut"`
	App     string      `json:"app"`
	Process string      `json:"process"`
	Path    string      `json:"path"`
	Host    string      `json:"host"`
	Dest    string      `json:"dest"`   // ip:port
	Source  string      `json:"source"` // the address that opened it
	Network string      `json:"network"`
	Inbound string      `json:"inbound"`
	Chain   string      `json:"chain"`  // the final outbound
	Chains  string      `json:"chains"` // the whole chain, rule's group last
	Route   store.Route `json:"route"`
	Sub     string      `json:"sub"` // the subscription; "" when direct
	Rule    string      `json:"rule"`
	Up      int64       `json:"up"`
	Down    int64       `json:"down"`
}

// SessionQuery selects sessions that overlap a range of time.
type SessionQuery struct {
	From        int64       `json:"from"` // unix ms
	To          int64       `json:"to"`   // unix ms
	App         string      `json:"app"`
	Host        string      `json:"host"`
	Chain       string      `json:"chain"`
	Route       store.Route `json:"route"`
	Sub         string      `json:"sub"`
	NoSub       bool        `json:"noSub"`       // with Sub "": only sessions of no subscription
	GroupDomain bool        `json:"groupDomain"` // Host is a registrable domain
	Search      string      `json:"search"`
	Offset      int         `json:"offset"`
	Limit       int         `json:"limit"` // 0 is 200
}

// SessionPage is a page of the sessions a query selects, newest first.
type SessionPage struct {
	Total int       `json:"total"` // sessions selected, on every page
	Up    int64     `json:"up"`    // of every selected session
	Down  int64     `json:"down"`
	Items []Session `json:"items"`
	Since int64     `json:"since"` // unix ms of the oldest day kept, 0 when none
}

type cachedDay struct {
	sessions []Session
	size     int64
	modTime  time.Time
	used     time.Time
}

// summary is what a query needs of a day it may skip: the earliest start of
// its sessions.
type summary struct {
	minStart int64
	size     int64
	modTime  time.Time
}

// MaxCached caps the sessions kept decoded in memory across days.
var MaxCached = 400_000

// Log is the session log of this computer.
type Log struct {
	dir string

	pmu      sync.Mutex // guards pending and inflight only: recording never waits for a query
	pending  []Session
	inflight []Session // being written: queries still see it

	mu        sync.Mutex // guards the caches, held while a query reads files
	cache     map[string]*cachedDay
	cached    int // sessions in cache
	summaries map[string]summary

	fmu     sync.Mutex      // serializes writing files; never held while taking mu or pmu
	checked map[string]bool // days whose file was checked before appending to it
}

// Open opens the log in dir.
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Log{dir: dir, cache: map[string]*cachedDay{}, summaries: map[string]summary{}, checked: map[string]bool{}}, nil
}

// Add records a session; Flush writes it.
func (l *Log) Add(s Session) {
	l.pmu.Lock()
	defer l.pmu.Unlock()
	l.pending = append(l.pending, s)
}

// Flush appends the recorded sessions to the files of the days they ended.
// Until they are written, queries find them among the inflight.
func (l *Log) Flush() error {
	l.pmu.Lock()
	if len(l.inflight) > 0 { // another Flush is writing
		l.pmu.Unlock()
		return nil
	}
	l.inflight, l.pending = l.pending, nil
	batch := l.inflight
	l.pmu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	byDay := map[string][]Session{}
	for _, s := range batch {
		day := store.DayOf(time.UnixMilli(s.End))
		byDay[day] = append(byDay[day], s)
	}
	var errs []error
	var retry []Session
	for day, list := range byDay {
		if err := l.appendDay(day, list); err != nil {
			errs = append(errs, err)
			retry = append(retry, list...)
		}
	}
	l.pmu.Lock()
	l.pending = append(retry, l.pending...) // try again next time
	l.inflight = nil
	l.pmu.Unlock()
	return errors.Join(errs...)
}

// appendDay appends sessions to a day's file. A run of the app checks the
// file before it first appends to it: a crash in the middle of a write
// leaves a member cut short, which would hide every member after it. A
// write that fails is taken back off the file, or the file is checked again
// before the next append.
func (l *Log) appendDay(day string, list []Session) error {
	l.fmu.Lock()
	defer l.fmu.Unlock()
	path := filepath.Join(l.dir, day+ext)
	if !l.checked[day] {
		if err := repair(path); err != nil {
			return err
		}
		l.checked[day] = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if err := writeMember(f, list); err != nil {
		if f.Truncate(st.Size()) != nil {
			l.checked[day] = false
		}
		f.Close()
		return err
	}
	if err := closeFile(f); err != nil {
		// Written or not, the batch is tried again: take it back off.
		if os.Truncate(path, st.Size()) != nil {
			l.checked[day] = false
		}
		return err
	}
	return nil
}

// writeMember writes sessions as one gzip member. Tests replace it.
var writeMember = func(w io.Writer, list []Session) error {
	zw := gzip.NewWriter(w)
	enc := json.NewEncoder(zw)
	for _, s := range list {
		if err := enc.Encode(s); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

// queryHook runs in a query between taking the sessions in memory and
// reading the files; queryDayHook after each day it reads. Tests set them.
var (
	queryHook    func()
	queryDayHook func()
)

// closeFile closes a file appended to. Tests replace it.
var closeFile = func(f *os.File) error { return f.Close() }

// openFile opens a day's file for reading. Tests replace it.
var openFile = func(path string) (io.ReadCloser, error) { return os.Open(path) }

// repair rewrites a file that is damaged with what it holds up to the
// damage, keeping a copy of the damaged one beside it. The file stays as it
// was until the repaired one replaces it whole: a repair that fails half
// way is done again. A file that could not be read is left alone: that is
// no damage.
func repair(path string) error {
	var list []Session
	complete, err := scanDay(path, func(s Session) { list = append(list, s) })
	if errors.Is(err, os.ErrNotExist) || (err == nil && complete) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 %s: %w", filepath.Base(path), err)
	}
	var buf bytes.Buffer
	if err := writeMember(&buf, list); err != nil {
		return err
	}
	damaged, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(fmt.Sprintf("%s.damaged-%d", path, time.Now().UnixNano()), damaged); err != nil {
		return err
	}
	return replaceFile(path, buf.Bytes())
}

// replaceFile replaces a file whole. Tests replace it.
var replaceFile = store.WriteFileAtomic

// sticky remembers the first error reading the file gave, which tells a
// failing read from damaged content.
type sticky struct {
	r   io.Reader
	err error
}

func (s *sticky) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF && s.err == nil {
		s.err = err
	}
	return n, err
}

// scanDay decodes a day's file, handing each session to fn, and reports
// whether it read to the end: damage ends the day there, and is no error.
// Errors are those of reading the file.
func scanDay(path string, fn func(Session)) (bool, error) {
	f, err := openFile(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	src := &sticky{r: f}
	zr, err := gzip.NewReader(bufio.NewReader(src))
	if err != nil {
		if src.err != nil {
			return false, src.err
		}
		return errors.Is(err, io.EOF), nil // empty, or no member at all
	}
	dec := json.NewDecoder(zr)
	for {
		var s Session
		if err := dec.Decode(&s); err != nil {
			if src.err != nil {
				return false, src.err
			}
			return errors.Is(err, io.EOF), nil
		}
		fn(s)
	}
}

// day returns the sessions of a day, from the cache when the file did not
// change. l.mu is held.
func (l *Log) day(date string) []Session {
	path := filepath.Join(l.dir, date+ext)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if c := l.cache[date]; c != nil && c.size == st.Size() && c.modTime.Equal(st.ModTime()) {
		c.used = time.Now()
		return c.sessions
	}
	list, err := readDay(path)
	if err != nil {
		return nil
	}
	if c := l.cache[date]; c != nil {
		l.cached -= len(c.sessions)
		delete(l.cache, date)
	}
	for l.cached+len(list) > MaxCached && len(l.cache) > 0 {
		var oldest string
		for k, c := range l.cache {
			if oldest == "" || c.used.Before(l.cache[oldest].used) {
				oldest = k
			}
		}
		l.cached -= len(l.cache[oldest].sessions)
		delete(l.cache, oldest)
	}
	if len(list) <= MaxCached {
		l.cache[date] = &cachedDay{sessions: list, size: st.Size(), modTime: st.ModTime(), used: time.Now()}
		l.cached += len(list)
	}
	l.summaries[date] = summarize(list, st)
	return list
}

// readDay reads a day's sessions. A batch written twice, by a retry after a
// write that failed yet reached the disk, is kept once.
func readDay(path string) ([]Session, error) {
	type key struct {
		id   string
		open bool
	}
	at := map[key]int{}
	var list []Session
	_, err := scanDay(path, func(s Session) {
		k := key{s.ID, s.Open}
		if i, ok := at[k]; ok {
			list[i] = s
			return
		}
		at[k] = len(list)
		list = append(list, s)
	})
	return list, err
}

func summarize(list []Session, st os.FileInfo) summary {
	sum := summary{minStart: math.MaxInt64, size: st.Size(), modTime: st.ModTime()}
	for _, s := range list {
		sum.minStart = min(sum.minStart, s.Start)
	}
	return sum
}

// minStart returns the earliest start of a day's sessions, reading the file
// only when it changed, and without keeping its sessions. l.mu is held.
func (l *Log) minStart(date string) int64 {
	path := filepath.Join(l.dir, date+ext)
	st, err := os.Stat(path)
	if err != nil {
		return math.MaxInt64
	}
	if sum, ok := l.summaries[date]; ok && sum.size == st.Size() && sum.modTime.Equal(st.ModTime()) {
		return sum.minStart
	}
	sum := summary{minStart: math.MaxInt64, size: st.Size(), modTime: st.ModTime()}
	if _, err := scanDay(path, func(s Session) { sum.minStart = min(sum.minStart, s.Start) }); err != nil {
		return math.MinInt64 // unknown: read it
	}
	l.summaries[date] = sum
	return sum.minStart
}

// days returns the dates of the files kept, oldest first.
func (l *Log) days() []string {
	entries, _ := os.ReadDir(l.dir)
	var out []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ext) && !e.IsDir() {
			out = append(out, strings.TrimSuffix(name, ext))
		}
	}
	sort.Strings(out)
	return out
}

// Cleanup deletes the days older than keepDays, and the damaged files kept
// of them; 0 keeps every day.
func (l *Log) Cleanup(keepDays int) error {
	if keepDays <= 0 {
		return nil
	}
	cutoff := store.DayOf(time.Now().AddDate(0, 0, -keepDays))
	entries, _ := os.ReadDir(l.dir)
	var errs []error
	for _, e := range entries {
		name := e.Name()
		date, _, ok := strings.Cut(name, ".")
		if !ok || date >= cutoff || !strings.HasPrefix(name[len(date):], ext) {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, name)); err != nil {
			errs = append(errs, err)
		}
		l.mu.Lock()
		if c := l.cache[date]; c != nil {
			l.cached -= len(c.sessions)
		}
		delete(l.cache, date)
		delete(l.summaries, date)
		l.mu.Unlock()
	}
	return errors.Join(errs...)
}

// page keeps the sessions a query selects that fall on its page: the
// newest offset+limit, in a heap with the oldest on top.
type page struct {
	keep  int
	items []Session
}

func newer(a, b Session) bool { return a.Start > b.Start || (a.Start == b.Start && a.ID < b.ID) }

func (p *page) Len() int           { return len(p.items) }
func (p *page) Less(i, j int) bool { return newer(p.items[j], p.items[i]) }
func (p *page) Swap(i, j int)      { p.items[i], p.items[j] = p.items[j], p.items[i] }
func (p *page) Push(x any)         { p.items = append(p.items, x.(Session)) }
func (p *page) Pop() any {
	s := p.items[len(p.items)-1]
	p.items = p.items[:len(p.items)-1]
	return s
}

func (p *page) add(s Session) {
	if len(p.items) < p.keep {
		heap.Push(p, s)
	} else if p.keep > 0 && newer(s, p.items[0]) {
		p.items[0] = s
		heap.Fix(p, 0)
	}
}

// Query returns the sessions q selects. open lists the connections open
// now, which take the place of what the log has of them.
func (l *Log) Query(q SessionQuery, open []Session) SessionPage {
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	offset := max(q.Offset, 0)
	search := strings.ToLower(strings.TrimSpace(q.Search))
	domains := map[string]string{}
	var out SessionPage
	pg := &page{keep: offset + limit}
	consider := func(s Session) {
		if s.Start >= q.To || s.End < q.From {
			return
		}
		if (q.App != "" && s.App != q.App) || (q.Chain != "" && s.Chain != q.Chain) || (q.Route != "" && s.Route != q.Route) ||
			((q.Sub != "" || q.NoSub) && s.Sub != q.Sub) {
			return
		}
		if q.Host != "" {
			host := s.Host
			if q.GroupDomain {
				d, ok := domains[host]
				if !ok {
					d = store.Domain(host)
					domains[host] = d
				}
				host = d
			}
			if host != q.Host {
				return
			}
		}
		if search != "" && !matches(&s, search) {
			return
		}
		out.Total++
		out.Up += s.Up
		out.Down += s.Down
		pg.add(s)
	}

	// Not on disk yet, or being written: these take the place of what the
	// files have of them, so that a batch written while the query reads is
	// seen once.
	l.pmu.Lock()
	mem := slices.Concat(l.inflight, l.pending)
	l.pmu.Unlock()
	inMem := make(map[string]bool, len(mem))
	for _, s := range mem {
		inMem[s.ID] = true
	}
	if queryHook != nil {
		queryHook()
	}

	// A session is in the file of the day it ended, which for a long one is
	// after the range: those days are read only if a session of theirs
	// started before the range ended.
	first := store.DayOf(time.UnixMilli(q.From))
	last := store.DayOf(time.UnixMilli(q.To))
	var days []string
	l.mu.Lock()
	kept := l.days()
	for _, d := range kept {
		if d < first || (d > last && l.minStart(d) >= q.To) {
			continue
		}
		days = append(days, d)
	}
	l.mu.Unlock()
	live := make(map[string]bool, len(open))
	for _, s := range open {
		live[s.ID] = true
	}
	// Each pass reads the days one by one: a day not cached is let go once
	// read, so a long range never sits in memory whole.
	records := func(fn func(Session)) {
		for _, d := range days {
			l.mu.Lock()
			list := l.day(d)
			l.mu.Unlock()
			for _, s := range list {
				if !inMem[s.ID] && !live[s.ID] {
					fn(s)
				}
			}
			if queryDayHook != nil {
				queryDayHook()
			}
		}
		for _, s := range mem {
			if !live[s.ID] {
				fn(s)
			}
		}
	}

	// A connection open when the app quit has a record of then, and one of
	// when it closed if the app saw it close. Which file each is in follows
	// the clock, which may have been set back: the closing record wins
	// wherever it is.
	quit := map[string]Session{}
	records(func(s Session) {
		if cur, ok := quit[s.ID]; s.Open && (!ok || s.End > cur.End) {
			quit[s.ID] = s
		}
	})
	records(func(s Session) {
		if !s.Open {
			delete(quit, s.ID)
			consider(s)
		}
	})
	// Open when the app quit, and not open now: it ended while the app was
	// not watching, at a time nobody knows.
	for _, s := range quit {
		s.Open, s.Cut = false, true
		consider(s)
	}
	for _, s := range open {
		consider(s)
	}

	sort.Slice(pg.items, func(i, j int) bool { return newer(pg.items[i], pg.items[j]) })
	if offset < len(pg.items) {
		out.Items = pg.items[offset:]
	} else {
		out.Items = []Session{}
	}
	if len(kept) > 0 {
		if t, err := time.Parse(time.DateOnly, kept[0]); err == nil {
			out.Since = t.UnixMilli()
		}
	}
	return out
}

func matches(s *Session, search string) bool {
	for _, v := range []string{s.App, s.Process, s.Host, s.Dest, s.Chains, s.Rule, s.Source, s.Sub} {
		if strings.Contains(strings.ToLower(v), search) {
			return true
		}
	}
	return false
}
