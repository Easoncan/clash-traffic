// Package store keeps the traffic of every device as hourly counters, one
// gzipped JSON file per device and UTC day:
//
//	<root>/<device id>/device.json
//	<root>/<device id>/2026-10-13.json.gz
//
// Each device writes only its own directory, so the directories of several
// computers merge without conflicts: copying them into one root, as the
// syncer does, is all it takes to see their traffic together.
package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// fileVersion 2 added the subscription to the rows; version 1 files read
// with none.
const fileVersion = 2

// DeviceFile and DayExt name the files of a device's directory.
const (
	DeviceFile = "device.json"
	DayExt     = ".json.gz"
)

// Counter counts bytes and connections.
type Counter struct {
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
	Conns int64 `json:"conns"`
}

// Add adds o to c.
func (c *Counter) Add(o Counter) {
	c.Up += o.Up
	c.Down += o.Down
	c.Conns += o.Conns
}

// Key identifies the traffic of an application to a host through a proxy
// of a subscription in one hour.
type Key struct {
	Hour  int64 // unix seconds of the start of the hour
	App   string
	Proc  string
	Host  string
	Chain string // the final outbound: a node, DIRECT or REJECT
	Sub   string // the subscription the node came from; "" when direct
}

// Row is a counter and its key.
type Row struct {
	Key
	Counter
}

// DeviceInfo describes a computer whose traffic is counted.
type DeviceInfo struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	OS      string    `json:"os"`
	Arch    string    `json:"arch"`
	Version string    `json:"version"`
	Updated int64     `json:"updated"` // unix ms of its last write; orders copies of the file
	Gen     int64     `json:"gen"`     // the generation of its newest day files
	Subs    []SubInfo `json:"subs"`    // the subscriptions configured on it
}

// SubInfo is a subscription as the proxy client knows it, with the usage
// its provider reports.
type SubInfo struct {
	Name     string `json:"name"`
	Current  bool   `json:"current"`  // in use on the device
	Upload   int64  `json:"upload"`   // bytes, as the provider counts them
	Download int64  `json:"download"` // bytes, as the provider counts them
	Total    int64  `json:"total"`    // the plan's bytes; 0 when unknown
	Expire   int64  `json:"expire"`   // unix seconds; 0 when it does not expire
	Updated  int64  `json:"updated"`  // unix seconds the client last fetched it
}

type day struct {
	rows  map[Key]*Counter
	paths map[string]string
	dirty bool
	// broken days had a file that could not be read nor moved aside:
	// writing them would overwrite what the file holds.
	broken bool
}

type cached struct {
	rows    []Row
	paths   map[string]string
	size    int64
	modTime time.Time
	used    time.Time
}

// Store reads the traffic of every device and writes the local one's.
type Store struct {
	mu    sync.Mutex
	root  string
	self  DeviceInfo
	open  map[string]*day // the local device's days in memory, by UTC date
	cache map[string]*cached
	// gen numbers the flushes that wrote days: each day file carries the
	// one that wrote it, which orders copies of it on other computers and
	// tells the collector whether its checkpoint matches the files.
	gen int64

	dmu     sync.Mutex // guards domains
	domains map[string]string
}

// DayOf returns the UTC date of t, which names its file.
func DayOf(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// Open opens the store at root for the local device self.
func Open(root string, self DeviceInfo) (*Store, error) {
	if self.ID == "" {
		return nil, errors.New("store: device without an id")
	}
	if err := os.MkdirAll(filepath.Join(root, self.ID), 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		root:    root,
		self:    self,
		open:    map[string]*day{},
		cache:   map[string]*cached{},
		domains: map[string]string{},
	}
	// The device file records the generation each flush reached; the
	// newest day files do too, should a crash have come between them.
	var prev DeviceInfo
	if b, err := os.ReadFile(filepath.Join(root, self.ID, DeviceFile)); err == nil {
		_ = json.Unmarshal(b, &prev)
	}
	s.gen = max(prev.Gen, s.latestGen())
	s.self.Gen = s.gen
	s.self.Updated = max(time.Now().UnixMilli(), prev.Updated+1)
	return s, s.writeDevice()
}

// latestGen returns the generation of the local device's files of the
// latest dates, which a flush just before a crash wrote.
func (s *Store) latestGen() int64 {
	entries, _ := os.ReadDir(filepath.Join(s.root, s.self.ID))
	var days []string
	for _, e := range entries {
		if dayFilePattern.MatchString(e.Name()) {
			days = append(days, e.Name())
		}
	}
	sort.Strings(days)
	var gen int64
	for i := max(0, len(days)-3); i < len(days); i++ {
		if f, err := readDayFile(filepath.Join(s.root, s.self.ID, days[i])); err == nil {
			gen = max(gen, f.Gen)
		}
	}
	return gen
}

// Gen returns the generation of the last flush that wrote days.
func (s *Store) Gen() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

var (
	deviceIDPattern = regexp.MustCompile(`^[0-9a-f]{12}(-dev)?$`)
	dayFilePattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.json\.gz$`)
)

// ValidDeviceID reports whether id is a device id as devices make them:
// twelve lowercase hex digits, with -dev for development builds. Nothing
// else names a directory, which on file systems that ignore case keeps a
// remote device from naming this one's.
func ValidDeviceID(id string) bool { return deviceIDPattern.MatchString(id) }

// ValidFileName reports whether name is a file of a device's directory.
func ValidFileName(name string) bool { return name == DeviceFile || dayFilePattern.MatchString(name) }

// Root returns the directory of the store.
func (s *Store) Root() string { return s.root }

// Self returns the local device.
func (s *Store) Self() DeviceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self
}

// Rename renames the local device.
func (s *Store) Rename(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.self.Name = name
	s.touch()
	return s.writeDevice()
}

// touch moves the device file's time on, past the last one even if the
// clock went back, so that copies of it order. s.mu is held.
func (s *Store) touch() {
	s.self.Updated = max(time.Now().UnixMilli(), s.self.Updated+1)
}

func (s *Store) writeDevice() error {
	b, err := json.MarshalIndent(s.self, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.root, s.self.ID, DeviceFile), b)
}

// SetSubs records the subscriptions of the local device, for the others.
func (s *Store) SetSubs(subs []SubInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subsEqual(s.self.Subs, subs) {
		return nil
	}
	s.self.Subs = subs
	s.touch()
	return s.writeDevice()
}

func subsEqual(a, b []SubInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Add counts traffic of the local device under k, in the hour starting at
// k.Hour. path, the application's executable, may be empty.
func (s *Store) Add(k Key, path string, c Counter) {
	if c == (Counter{}) {
		return
	}
	k.Hour -= k.Hour % 3600
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.openDay(DayOf(time.Unix(k.Hour, 0)))
	app := k.App
	if cur := d.rows[k]; cur != nil {
		cur.Add(c)
	} else {
		cc := c
		d.rows[k] = &cc
	}
	if path != "" && app != "" && d.paths[app] == "" {
		d.paths[app] = path
	}
	d.dirty = true
}

// openDay returns a day of the local device, loading what an earlier run
// saved of it. s.mu is held.
func (s *Store) openDay(date string) *day {
	if d := s.open[date]; d != nil {
		return d
	}
	d := &day{rows: map[Key]*Counter{}, paths: map[string]string{}}
	path := s.dayPath(s.self.ID, date)
	rows, paths, err := readDay(path)
	switch {
	case err == nil:
		for _, r := range rows {
			c := r.Counter
			d.rows[r.Key] = &c
		}
		maps.Copy(d.paths, paths)
	case errors.Is(err, os.ErrNotExist):
	default:
		// Never write over a file that could not be read: keep it aside,
		// out of the way of reads and syncs, and count the day anew.
		aside := fmt.Sprintf("%s.unreadable-%d", path, time.Now().Unix())
		if rerr := os.Rename(path, aside); rerr != nil {
			d.broken = true
			log.Printf("store: %s cannot be read (%v) nor moved aside (%v): the day is not saved", path, err, rerr)
		} else {
			log.Printf("store: %s cannot be read (%v): kept as %s", path, err, filepath.Base(aside))
		}
	}
	s.open[date] = d
	delete(s.cache, s.self.ID+"/"+date)
	return d
}

func (s *Store) dayPath(device, date string) string {
	return filepath.Join(s.root, device, date+DayExt)
}

// Flush writes the local device's changed days to disk, and lets go of the
// days that are over.
//
// The device file goes first, with the generation the days are written
// with: it is then never behind a day file. A crash between the two leaves
// the store a generation ahead of the collector's checkpoint, which a new
// run then does not use: some bytes may go uncounted, none twice.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	keep := map[string]bool{DayOf(time.Now()): true, DayOf(time.Now().Add(-time.Hour)): true}
	var dirty []string
	for date, d := range s.open {
		if d.broken && d.dirty {
			errs = append(errs, fmt.Errorf("%s 的数据文件无法读取，未保存这一天的新数据", date))
		}
		if d.dirty && !d.broken {
			dirty = append(dirty, date)
		}
	}
	if len(dirty) > 0 {
		gen := s.gen + 1
		prev := s.self
		s.self.Gen = gen
		s.touch()
		if err := s.writeDevice(); err != nil {
			s.self = prev
			return errors.Join(append(errs, err)...)
		}
		s.gen = gen
		for _, date := range dirty {
			d := s.open[date]
			if err := writeDay(s.dayPath(s.self.ID, date), s.self.ID, date, gen, d); err != nil {
				errs = append(errs, err)
				continue
			}
			d.dirty = false
		}
	}
	for date, d := range s.open {
		if !keep[date] && !d.dirty {
			delete(s.open, date)
		}
	}
	return errors.Join(errs...)
}

// Devices returns the devices with data in the store, the local one first.
func (s *Store) Devices() []DeviceInfo {
	s.mu.Lock()
	self := s.self
	s.mu.Unlock()
	out := []DeviceInfo{self}
	entries, _ := os.ReadDir(s.root)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == self.ID || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info := DeviceInfo{ID: e.Name(), Name: e.Name()}
		if b, err := os.ReadFile(filepath.Join(s.root, e.Name(), DeviceFile)); err == nil {
			_ = json.Unmarshal(b, &info)
			info.ID = e.Name()
		}
		out = append(out, info)
	}
	sort.SliceStable(out[1:], func(i, j int) bool { return out[1+i].Name < out[1+j].Name })
	return out
}

// Forget deletes the data of another device from this computer.
func (s *Store) Forget(id string) error {
	if id == "" || strings.EqualFold(id, s.Self().ID) || strings.ContainsAny(id, `/\.`) {
		return fmt.Errorf("不能删除设备 %q", id)
	}
	s.mu.Lock()
	for k := range s.cache {
		if strings.HasPrefix(k, id+"/") {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.root, id))
}

// rows returns the rows of a device's day: from memory for the local
// device's open days, else from its file. s.mu is held.
func (s *Store) rows(device, date string) ([]Row, map[string]string) {
	if device == s.self.ID {
		if d := s.open[date]; d != nil {
			rows := make([]Row, 0, len(d.rows))
			for k, c := range d.rows {
				rows = append(rows, Row{Key: k, Counter: *c})
			}
			// A copy: the collector adds to the day while queries read.
			return rows, maps.Clone(d.paths)
		}
	}
	path := s.dayPath(device, date)
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	key := device + "/" + date
	if c := s.cache[key]; c != nil && c.size == st.Size() && c.modTime.Equal(st.ModTime()) {
		c.used = time.Now()
		return c.rows, c.paths
	}
	rows, paths, err := readDay(path)
	if err != nil {
		return nil, nil
	}
	if len(s.cache) > 400 {
		s.evict()
	}
	s.cache[key] = &cached{rows: rows, paths: paths, size: st.Size(), modTime: st.ModTime(), used: time.Now()}
	return rows, paths
}

func (s *Store) evict() {
	type kv struct {
		k string
		t time.Time
	}
	all := make([]kv, 0, len(s.cache))
	for k, c := range s.cache {
		all = append(all, kv{k, c.used})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.Before(all[j].t) })
	for _, e := range all[:len(all)/2] {
		delete(s.cache, e.k)
	}
}

type dayFile struct {
	V      int    `json:"v"`
	Device string `json:"device"`
	Day    string `json:"day"`
	// Gen is the generation of the flush that wrote the file: a copy with
	// a higher one is newer. Files written before it have none.
	Gen int64 `json:"gen,omitempty"`
	// Strings holds the names the rows refer to by index.
	Strings []string `json:"strings"`
	// Rows are [hour of the day, app, process, host, chain, subscription,
	// up, down, connections], the names as indexes into Strings. Version 1
	// rows have no subscription.
	Rows  [][]int64         `json:"rows"`
	Paths map[string]string `json:"paths,omitempty"`
}

func writeDay(path, device, date string, gen int64, d *day) error {
	start, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return err
	}
	f := dayFile{V: fileVersion, Device: device, Day: date, Gen: gen, Paths: d.paths}
	index := map[string]int64{}
	str := func(s string) int64 {
		if i, ok := index[s]; ok {
			return i
		}
		i := int64(len(f.Strings))
		index[s] = i
		f.Strings = append(f.Strings, s)
		return i
	}
	keys := make([]Key, 0, len(d.rows))
	for k := range d.rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		if a.App != b.App {
			return a.App < b.App
		}
		if a.Host != b.Host {
			return a.Host < b.Host
		}
		if a.Proc != b.Proc {
			return a.Proc < b.Proc
		}
		if a.Chain != b.Chain {
			return a.Chain < b.Chain
		}
		return a.Sub < b.Sub
	})
	f.Rows = make([][]int64, 0, len(keys))
	for _, k := range keys {
		c := d.rows[k]
		f.Rows = append(f.Rows, []int64{(k.Hour - start.Unix()) / 3600, str(k.App), str(k.Proc), str(k.Host), str(k.Chain), str(k.Sub), c.Up, c.Down, c.Conns})
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(f); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return WriteFileAtomic(path, buf.Bytes())
}

// Limits caps what decoding a day file may take. Files from other
// computers get RemoteLimits: a small file could otherwise decompress into,
// or decode into, more than the memory there is. This device's own files
// were written here, and get no limits.
type Limits struct {
	Bytes   int64 // decompressed
	Rows    int
	Strings int // in the dictionary, and paths
	String  int // bytes in one string
}

// RemoteLimits are far above what a device's day holds.
// Bytes also bounds what a single JSON value may take while it decodes.
var RemoteLimits = Limits{Bytes: 128 << 20, Rows: 1_000_000, Strings: 500_000, String: 8 << 10}

var localLimits = Limits{Bytes: math.MaxInt64, Rows: math.MaxInt, Strings: math.MaxInt, String: math.MaxInt}

var errTooLarge = errors.New("解压后超过大小上限")

type capped struct {
	r    io.Reader
	left int64
}

func (c *capped) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func readDayFile(path string) (*dayFile, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return decodeDay(fh, localLimits)
}

// decodeDay decodes a day file within lim, as a stream, so that limits act
// before memory is taken. It reads the file to its very end: gzip checks
// the data against its checksum only once the stream is read through, and
// nothing may follow the one gzip member a day file is.
func decodeDay(r io.Reader, lim Limits) (*dayFile, error) {
	br := bufio.NewReader(r)
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, err
	}
	zr.Multistream(false)
	body := &capped{r: zr, left: lim.Bytes}
	dec := json.NewDecoder(body)
	dec.UseNumber()
	f, err := decodeDayJSON(dec, lim)
	if err != nil {
		return nil, err
	}
	rest, err := io.ReadAll(io.MultiReader(dec.Buffered(), body))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return nil, errors.New("数据之后还有多余内容")
	}
	if _, err := br.ReadByte(); err != io.EOF {
		return nil, errors.New("压缩数据之后还有多余内容")
	}
	if _, err := time.Parse(time.DateOnly, f.Day); err != nil {
		return nil, fmt.Errorf("day %q: %w", f.Day, err)
	}
	return f, nil
}

func delim(dec *json.Decoder, want json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != want {
		return fmt.Errorf("期望 %q", want)
	}
	return nil
}

func str(dec *json.Decoder, lim Limits) (string, error) {
	t, err := dec.Token()
	if err != nil {
		return "", err
	}
	v, ok := t.(string)
	if !ok {
		return "", errors.New("期望字符串")
	}
	if len(v) > lim.String {
		return "", errors.New("字符串过长")
	}
	return v, nil
}

func decodeDayJSON(dec *json.Decoder, lim Limits) (*dayFile, error) {
	f := &dayFile{}
	if err := delim(dec, '{'); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := str(dec, lim)
		if err != nil {
			return nil, err
		}
		// Each field once: the limits count each list whole.
		if seen[key] {
			return nil, fmt.Errorf("字段 %q 重复", key)
		}
		seen[key] = true
		switch key {
		case "v":
			err = dec.Decode(&f.V)
		case "device":
			f.Device, err = str(dec, lim)
		case "day":
			f.Day, err = str(dec, lim)
		case "gen":
			err = dec.Decode(&f.Gen)
		case "strings":
			err = decodeStrings(dec, lim, f)
		case "rows":
			err = decodeRows(dec, lim, f)
		case "paths":
			err = decodePaths(dec, lim, f)
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	return f, delim(dec, '}')
}

func decodeStrings(dec *json.Decoder, lim Limits, f *dayFile) error {
	if err := delim(dec, '['); err != nil {
		return err
	}
	for dec.More() {
		if len(f.Strings) >= lim.Strings {
			return errors.New("字符串过多")
		}
		v, err := str(dec, lim)
		if err != nil {
			return err
		}
		f.Strings = append(f.Strings, v)
	}
	return delim(dec, ']')
}

func decodeRows(dec *json.Decoder, lim Limits, f *dayFile) error {
	if err := delim(dec, '['); err != nil {
		return err
	}
	for n := 0; dec.More(); n++ {
		if n >= lim.Rows {
			return errors.New("行数过多")
		}
		if err := delim(dec, '['); err != nil {
			return err
		}
		row := make([]int64, 0, 9)
		for dec.More() {
			if len(row) == 9 {
				return errors.New("行过宽")
			}
			t, err := dec.Token()
			if err != nil {
				return err
			}
			num, ok := t.(json.Number)
			if !ok {
				return errors.New("期望数字")
			}
			v, err := num.Int64()
			if err != nil {
				return err
			}
			row = append(row, v)
		}
		if err := delim(dec, ']'); err != nil {
			return err
		}
		if len(row) == 8 || len(row) == 9 {
			f.Rows = append(f.Rows, row)
		}
	}
	return delim(dec, ']')
}

func decodePaths(dec *json.Decoder, lim Limits, f *dayFile) error {
	if err := delim(dec, '{'); err != nil {
		return err
	}
	f.Paths = map[string]string{}
	for dec.More() {
		if len(f.Paths) >= lim.Strings {
			return errors.New("路径过多")
		}
		k, err := str(dec, lim)
		if err != nil {
			return err
		}
		v, err := str(dec, lim)
		if err != nil {
			return err
		}
		f.Paths[k] = v
	}
	return delim(dec, '}')
}

func readDay(path string) ([]Row, map[string]string, error) {
	f, err := readDayFile(path)
	if err != nil {
		return nil, nil, err
	}
	start, _ := time.Parse(time.DateOnly, f.Day)
	name := func(i int64) string {
		if i < 0 || int(i) >= len(f.Strings) {
			return ""
		}
		return f.Strings[i]
	}
	rows := make([]Row, 0, len(f.Rows))
	for _, r := range f.Rows {
		var row Row
		switch len(r) {
		case 8: // version 1
			row = Row{
				Key:     Key{Hour: start.Unix() + r[0]*3600, App: name(r[1]), Proc: name(r[2]), Host: name(r[3]), Chain: name(r[4])},
				Counter: Counter{Up: r[5], Down: r[6], Conns: r[7]},
			}
		case 9:
			row = Row{
				Key:     Key{Hour: start.Unix() + r[0]*3600, App: name(r[1]), Proc: name(r[2]), Host: name(r[3]), Chain: name(r[4]), Sub: name(r[5])},
				Counter: Counter{Up: r[6], Down: r[7], Conns: r[8]},
			}
		default:
			continue
		}
		rows = append(rows, row)
	}
	if f.Paths == nil {
		f.Paths = map[string]string{}
	}
	return rows, f.Paths, nil
}

// total sums the counters of a day file: they only grow while its device
// writes it, so a larger total is a newer copy of files without Gen.
func (f *dayFile) total() int64 {
	var t int64
	for _, r := range f.Rows {
		for _, v := range r[max(len(r)-3, 0):] {
			t += v
		}
	}
	return t
}

// replaceMu makes comparing a copy with the one on disk and replacing it
// one step, for the ways copies arrive run side by side.
var replaceMu sync.Mutex

// ReplaceIfNewer writes data, a file another device wrote, to path if it is
// newer than the copy there, so that copies arriving by several ways
// (Tailscale, a shared folder, WebDAV) never take the store back to an
// older one. It reports whether it wrote, and fails when data is no such
// file.
func ReplaceIfNewer(path, name string, data []byte) (bool, error) {
	replaceMu.Lock()
	defer replaceMu.Unlock()
	newer, err := ShouldReplace(path, name, data)
	if err != nil || !newer {
		return false, err
	}
	return true, WriteFileAtomic(path, data)
}

// ShouldReplace reports whether data is newer than the copy at path.
// Writers use ReplaceIfNewer.
func ShouldReplace(path, name string, data []byte) (bool, error) {
	if name == DeviceFile {
		var in DeviceInfo
		if err := json.Unmarshal(data, &in); err != nil {
			return false, fmt.Errorf("%s: %w", name, err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return true, nil
		}
		var cur DeviceInfo
		if json.Unmarshal(b, &cur) != nil {
			return true, nil
		}
		// Every change moves the time on: the same time is the same file.
		return in.Updated > cur.Updated, nil
	}
	in, err := decodeDay(bytes.NewReader(data), RemoteLimits)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	cur, err := readDayFile(path)
	if err != nil {
		return true, nil // none, or unreadable: any good copy is better
	}
	if in.Gen != cur.Gen {
		return in.Gen > cur.Gen, nil
	}
	return in.total() > cur.total(), nil
}

// MaxFileBytes caps a file fetched from another computer, as it is read:
// day files are a few megabytes at most.
var MaxFileBytes int64 = 64 << 20

// ReadLimited reads all of r, failing when it holds more than limit bytes:
// a file cut at the limit must not pass for the whole one.
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("文件超过 %d MB 上限", limit>>20)
	}
	return b, nil
}

// WriteFileAtomic writes a file through a temporary one, so that readers,
// and folders synced by other programs, never see half of it.
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
