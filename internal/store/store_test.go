package store

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func hourOf(t time.Time) int64 { return t.Unix() - t.Unix()%3600 }

func TestAddFlushReopen(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, DeviceInfo{ID: "mac", Name: "MacBook"})
	if err != nil {
		t.Fatal(err)
	}
	h := hourOf(time.Now())
	s.Add(Key{Hour: h, App: "Google Chrome", Proc: "Google Chrome Helper", Host: "www.google.com", Chain: "HK 01"}, "/Applications/Google Chrome.app", Counter{Up: 10, Down: 100, Conns: 1})
	s.Add(Key{Hour: h, App: "Google Chrome", Proc: "Google Chrome Helper", Host: "www.google.com", Chain: "HK 01"}, "", Counter{Up: 5, Down: 50})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	// A new run continues the day where the last one stopped.
	s2, err := Open(root, DeviceInfo{ID: "mac", Name: "MacBook"})
	if err != nil {
		t.Fatal(err)
	}
	s2.Add(Key{Hour: h, App: "Google Chrome", Proc: "Google Chrome Helper", Host: "www.google.com", Chain: "HK 01"}, "", Counter{Up: 1, Down: 1})
	r := s2.Query(Query{From: (h - 3600) * 1000, To: (h + 3600) * 1000})
	if r.Up != 16 || r.Down != 151 || r.Conns != 1 {
		t.Fatalf("totals = %d/%d/%d, want 16/151/1", r.Up, r.Down, r.Conns)
	}
	if len(r.Apps) != 1 || r.Apps[0].Detail != "/Applications/Google Chrome.app" {
		t.Fatalf("apps = %+v", r.Apps)
	}
	if r.Bucket != "hour" || len(r.Series) != 2 || r.Series[1].Down != 151 {
		t.Fatalf("series = %s %+v", r.Bucket, r.Series)
	}
}

func TestDevicesMergeAndCrossFilter(t *testing.T) {
	root := t.TempDir()
	h := hourOf(time.Now())
	mac, _ := Open(root, DeviceInfo{ID: "mac", Name: "MacBook"})
	win, _ := Open(root, DeviceInfo{ID: "win", Name: "Desktop"})
	mac.Add(Key{Hour: h, App: "Chrome", Proc: "chrome", Host: "a.google.com", Chain: "HK"}, "", Counter{Down: 100, Conns: 1})
	mac.Add(Key{Hour: h, App: "Chrome", Proc: "chrome", Host: "b.google.com", Chain: "HK"}, "", Counter{Down: 50, Conns: 1})
	mac.Add(Key{Hour: h, App: "Telegram", Proc: "Telegram", Host: "149.154.167.51", Chain: "SG"}, "", Counter{Down: 30, Conns: 1})
	win.Add(Key{Hour: h, App: "Chrome", Proc: "chrome.exe", Host: "github.com", Chain: "DIRECT"}, "", Counter{Down: 1000, Conns: 2})
	if err := win.Flush(); err != nil {
		t.Fatal(err)
	}

	q := Query{From: h * 1000, To: (h + 3600) * 1000}
	r := mac.Query(q)
	if r.Down != 1180 || len(r.Devices) != 2 {
		t.Fatalf("down %d, devices %d", r.Down, len(r.Devices))
	}
	if !r.Devices[0].Self || r.Devices[0].Down != 180 || r.Devices[1].Down != 1000 {
		t.Fatalf("devices = %+v", r.Devices)
	}

	// Picking Chrome lists Chrome's hosts, but still every application.
	q.App = "Chrome"
	r = mac.Query(q)
	if r.Down != 1150 || len(r.Apps) != 2 || len(r.Hosts) != 3 {
		t.Fatalf("app filter: down %d apps %d hosts %d", r.Down, len(r.Apps), len(r.Hosts))
	}
	// One device only; device totals still cover both.
	q.Devices = []string{"mac"}
	r = mac.Query(q)
	if r.Down != 150 || r.Devices[1].Down != 1000 {
		t.Fatalf("device filter: down %d, %+v", r.Down, r.Devices)
	}
	// Hosts grouped by domain.
	q = Query{From: h * 1000, To: (h + 3600) * 1000, GroupDomain: true, Devices: []string{"mac"}}
	r = mac.Query(q)
	if len(r.Hosts) != 2 || r.Hosts[0].Name != "google.com" || r.Hosts[0].Down != 150 {
		t.Fatalf("domains = %+v", r.Hosts)
	}
	q.Host = "google.com"
	if r = mac.Query(q); r.Down != 150 {
		t.Fatalf("domain filter: %d", r.Down)
	}

	var buf bytes.Buffer
	if err := mac.Export(Query{From: h * 1000, To: (h + 3600) * 1000}, &buf); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(buf.String(), "\n"); lines != 5 {
		t.Fatalf("csv has %d lines:\n%s", lines, buf.String())
	}
	// The search of the page exports the lines it shows.
	for _, c := range []struct {
		q     Query
		lines int
	}{
		{Query{Search: "chrome", SearchIn: "app"}, 4},
		{Query{Search: "google", SearchIn: "host"}, 3},
		{Query{Search: "google", SearchIn: "app"}, 1},
		{Query{Search: "telegram"}, 2},
	} {
		c.q.From, c.q.To = h*1000, (h+3600)*1000
		buf.Reset()
		if err := mac.Export(c.q, &buf); err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(buf.String(), "\n"); lines != c.lines {
			t.Fatalf("%q in %q: csv has %d lines:\n%s", c.q.Search, c.q.SearchIn, lines, buf.String())
		}
	}
}

func TestOpenRejectsPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	for _, id := range []string{"../escape", "a/b", "..", "/abs"} {
		if _, err := Open(root, DeviceInfo{ID: id}); err == nil {
			t.Errorf("Open(%q) took it", id)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Fatal("a directory outside of the store was made")
	}
}

func TestDayBuckets(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -6)
	for i := range 7 {
		d := start.AddDate(0, 0, i).Add(12 * time.Hour)
		s.Add(Key{Hour: hourOf(d), App: "app", Proc: "app", Host: "host", Chain: "DIRECT"}, "", Counter{Down: int64(i + 1)})
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	r := s.Query(Query{From: start.UnixMilli(), To: start.AddDate(0, 0, 7).UnixMilli()})
	if r.Bucket != "day" || len(r.Series) != 7 {
		t.Fatalf("%s %d", r.Bucket, len(r.Series))
	}
	for i, p := range r.Series {
		if p.Down != int64(i+1) {
			t.Fatalf("series[%d] = %+v", i, p)
		}
	}
}

func TestRoutes(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, DeviceInfo{ID: "a", Name: "A"})
	h := hourOf(time.Now())
	s.Add(Key{Hour: h, App: "Chrome", Proc: "chrome", Host: "youtube.com", Chain: "HK 01"}, "", Counter{Down: 100})
	s.Add(Key{Hour: h, App: "Chrome", Proc: "chrome", Host: "baidu.com", Chain: "DIRECT"}, "", Counter{Down: 40})
	s.Add(Key{Hour: h, App: "Steam", Proc: "steam", Host: "steamcdn.com", Chain: "DIRECT"}, "", Counter{Down: 500})
	s.Add(Key{Hour: h, App: "ads", Proc: "x", Host: "ads.example", Chain: "REJECT"}, "", Counter{Conns: 3})
	s.Add(Key{Hour: h, App: "短连接", Proc: "", Host: "?", Chain: ""}, "", Counter{Down: 7})

	q := Query{From: h * 1000, To: (h + 3600) * 1000}
	r := s.Query(q)
	got := map[string]int64{}
	for _, it := range r.Routes {
		got[it.Name] = it.Down
	}
	if got["proxy"] != 100 || got["direct"] != 540 || got["unknown"] != 7 || r.Down != 647 {
		t.Fatalf("routes %v, down %d", got, r.Down)
	}

	// Only the proxied traffic: what an airport plan counts.
	q.Route = RouteProxy
	r = s.Query(q)
	if r.Down != 100 || len(r.Apps) != 1 || r.Apps[0].Name != "Chrome" || len(r.Routes) != 4 {
		t.Fatalf("proxy only: down %d apps %+v routes %d", r.Down, r.Apps, len(r.Routes))
	}
	if r.Devices[0].Down != 100 {
		t.Fatalf("device total should follow the route: %+v", r.Devices[0])
	}
}

func TestSubsAndVersion1Files(t *testing.T) {
	root := t.TempDir()
	h := hourOf(time.Now())
	mac, _ := Open(root, DeviceInfo{ID: "mac", Name: "Mac"})
	win, _ := Open(root, DeviceInfo{ID: "win", Name: "Win"})
	mac.Add(Key{Hour: h, App: "Chrome", Host: "youtube.com", Chain: "HK 01", Sub: "蜂鸟云"}, "", Counter{Down: 100})
	mac.Add(Key{Hour: h, App: "Chrome", Host: "github.com", Chain: "JP 02", Sub: "TAG"}, "", Counter{Down: 30})
	mac.Add(Key{Hour: h, App: "Steam", Host: "steamcdn.com", Chain: "DIRECT"}, "", Counter{Down: 500})
	win.Add(Key{Hour: h, App: "Edge", Host: "bing.com", Chain: "HK 01", Sub: "蜂鸟云"}, "", Counter{Down: 7})
	if err := mac.SetSubs([]SubInfo{{Name: "蜂鸟云", Current: true, Download: 3 << 30, Total: 250 << 30}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{mac, win} {
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
	}

	// A version 1 file, from before subscriptions were counted.
	day := DayOf(time.Unix(h, 0))
	start, _ := time.Parse(time.DateOnly, day)
	v1 := `{"v":1,"device":"old","day":"` + day + `","strings":["App","h","DIRECT"],"rows":[[` +
		strconv.FormatInt((h-start.Unix())/3600, 10) + `,0,0,1,2,1,2,3]]}`
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(v1))
	zw.Close()
	if err := WriteFileAtomic(filepath.Join(root, "old", day+DayExt), gz.Bytes()); err != nil {
		t.Fatal(err)
	}

	q := Query{From: h * 1000, To: (h + 3600) * 1000}
	r := mac.Query(q)
	subs := map[string]int64{}
	for _, it := range r.Subs {
		subs[it.Name] = it.Down
	}
	// The version 1 row went DIRECT: it counts towards no subscription.
	if subs["蜂鸟云"] != 107 || subs["TAG"] != 30 || subs[""] != 502 || r.Down != 639 {
		t.Fatalf("subs %v down %d", subs, r.Down)
	}
	q.Sub = "蜂鸟云"
	if r = mac.Query(q); r.Down != 107 || len(r.Subs) != 3 {
		t.Fatalf("one sub: %d %+v", r.Down, r.Subs)
	}
	var winShare int64
	for _, sd := range r.SubDevices {
		if sd.Sub == "蜂鸟云" && sd.Device == "win" {
			winShare = sd.Down
		}
	}
	if winShare != 7 {
		t.Fatalf("sub by device: %+v", r.SubDevices)
	}
	// Picking a device splits its subscriptions by it alone.
	q.Devices = []string{"mac"}
	r = mac.Query(q)
	if len(r.SubDevices) == 0 {
		t.Fatal("no sub by device, mac picked")
	}
	for _, sd := range r.SubDevices {
		if sd.Device != "mac" {
			t.Fatalf("sub by device, mac picked: %+v", r.SubDevices)
		}
	}
	q.Devices = nil
	q.Sub, q.NoSub = "", true
	if r = mac.Query(q); r.Down != 502 {
		t.Fatalf("no sub: %d", r.Down)
	}
	// Even a report of one line lists every subscription.
	if r = mac.Query(Query{From: h * 1000, To: (h + 3600) * 1000, Limit: 1}); len(r.Subs) != 3 || len(r.Apps) != 1 {
		t.Fatalf("limited report: %d subs, %d apps", len(r.Subs), len(r.Apps))
	}
	// Proxied traffic without a subscription, counted before there were.
	mac.Add(Key{Hour: h, App: "Old", Host: "x.com", Chain: "HK 01"}, "", Counter{Down: 1})
	if r = mac.Query(Query{From: h * 1000, To: (h + 3600) * 1000, Sub: SubUnknown}); r.Down != 1 {
		t.Fatalf("unknown subscription: %d", r.Down)
	}
	if d := mac.Devices(); len(d[0].Subs) != 1 || d[0].Subs[0].Total != 250<<30 {
		t.Fatalf("device subs: %+v", d[0])
	}
}
