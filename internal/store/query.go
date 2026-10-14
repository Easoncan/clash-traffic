package store

import (
	"encoding/csv"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Query selects traffic. Each list of a report ignores its own filter, so
// that picking an application still lists every application, and the
// hosts list shows the hosts of the picked one.
type Query struct {
	From        int64    `json:"from"` // unix ms, inclusive
	To          int64    `json:"to"`   // unix ms, exclusive
	Devices     []string `json:"devices"`
	App         string   `json:"app"`
	Host        string   `json:"host"`
	Chain       string   `json:"chain"`
	GroupDomain bool     `json:"groupDomain"` // hosts by registrable domain: a.b.google.com is google.com
	Search      string   `json:"search"`      // filters the names in the lists
	// SearchAlso are names that match too, as the data spells them: the
	// page shows some in the user's language.
	SearchAlso []string `json:"searchAlso"`
	Limit      int      `json:"limit"`  // per list; 0 is 100
	Bucket     string   `json:"bucket"` // "hour", "day", or "" to pick by the range
	Route      Route    `json:"route"`  // only this route; "" for all
	Sub        string   `json:"sub"`    // only this subscription; "" for all
	// NoSub, with Sub "", selects the traffic of no subscription: direct.
	NoSub bool `json:"noSub"`
}

// Route is where a connection went: through a proxy, directly, or nowhere.
type Route string

const (
	RouteProxy   Route = "proxy"
	RouteDirect  Route = "direct"
	RouteReject  Route = "reject"
	RouteUnknown Route = "unknown" // bytes no connection could be blamed for
)

// SubUnknown is the subscription of proxied traffic none could be named
// for, such as traffic counted before subscriptions were.
const SubUnknown = "未知订阅"

// RouteOf returns the route of traffic that left through chain, the
// connection's final outbound.
func RouteOf(chain string) Route {
	switch strings.ToUpper(strings.TrimSpace(chain)) {
	case "DIRECT":
		return RouteDirect
	case "REJECT", "REJECT-DROP":
		return RouteReject
	case "":
		return RouteUnknown
	}
	return RouteProxy
}

// Item is a line of a list.
type Item struct {
	Name   string `json:"name"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
	Conns  int64  `json:"conns"`
	Detail string `json:"detail"` // an application's executable
}

// Point is the traffic of a bucket of time.
type Point struct {
	T    int64 `json:"t"` // unix ms of the start of the bucket
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// DeviceTotal is the traffic of a device.
type DeviceTotal struct {
	Device DeviceInfo `json:"device"`
	Self   bool       `json:"self"`
	Up     int64      `json:"up"`
	Down   int64      `json:"down"`
	Conns  int64      `json:"conns"`
}

// Report summarizes the traffic a query selects.
type Report struct {
	Up        int64         `json:"up"`
	Down      int64         `json:"down"`
	Conns     int64         `json:"conns"`
	Bucket    string        `json:"bucket"`
	Series    []Point       `json:"series"`
	Apps      []Item        `json:"apps"`
	Hosts     []Item        `json:"hosts"`
	Chains    []Item        `json:"chains"`
	Devices   []DeviceTotal `json:"devices"`
	AppCount  int           `json:"appCount"`
	HostCount int           `json:"hostCount"`
	// The traffic of each list, of every line of it before search and
	// limit: the whole its lines are shares of. Each list ignores its own
	// filter, so these are not Up+Down.
	AppsTotal   int64 `json:"appsTotal"`
	HostsTotal  int64 `json:"hostsTotal"`
	ChainsTotal int64 `json:"chainsTotal"`
	SubsTotal   int64 `json:"subsTotal"`
	// Routes splits the traffic by route, whatever route the query asks for.
	Routes []Item `json:"routes"`
	// Subs splits it by subscription, whatever subscription the query asks
	// for; "" is the traffic of none.
	Subs []Item `json:"subs"`
	// SubDevices splits each subscription's traffic by device.
	SubDevices []SubDevice `json:"subDevices"`
}

// SubDevice is the traffic of a subscription on a device.
type SubDevice struct {
	Sub    string `json:"sub"`
	Device string `json:"device"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

type deviceRows struct {
	device string
	rows   [][]Row
	paths  []map[string]string
}

// gather collects the rows of every device's days that overlap the range.
func (s *Store) gather(from, to int64) ([]DeviceInfo, []deviceRows) {
	devices := s.Devices()
	var days []string
	for t := time.Unix(from, 0).UTC().Truncate(24 * time.Hour); t.Unix() < to; t = t.Add(24 * time.Hour) {
		days = append(days, DayOf(t))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]deviceRows, 0, len(devices))
	for _, d := range devices {
		dr := deviceRows{device: d.ID}
		for _, day := range days {
			rows, paths := s.rows(d.ID, day)
			if len(rows) > 0 {
				dr.rows = append(dr.rows, rows)
				dr.paths = append(dr.paths, paths)
			}
		}
		out = append(out, dr)
	}
	return devices, out
}

// Domain returns the registrable domain of a host: a.b.google.com is
// google.com, a.bbc.co.uk is bbc.co.uk. IP addresses stay as they are.
func Domain(host string) string {
	if net.ParseIP(host) == nil && strings.Contains(host, ".") {
		if v, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
			return v
		}
	}
	return host
}

// domain is Domain, remembered. s.dmu is held.
func (s *Store) domain(host string) string {
	if d, ok := s.domains[host]; ok {
		return d
	}
	d := Domain(host)
	if len(s.domains) > 200_000 {
		s.domains = map[string]string{}
	}
	s.domains[host] = d
	return d
}

// hostOf returns the host of a row as the query groups it.
func (s *Store) hostOf(q *Query, host string) string {
	if !q.GroupDomain {
		return host
	}
	s.dmu.Lock()
	defer s.dmu.Unlock()
	return s.domain(host)
}

// subOf returns the subscription a row counts towards.
func subOf(r Row) string {
	if r.Sub == "" && RouteOf(r.Chain) == RouteProxy {
		return SubUnknown
	}
	return r.Sub
}

func localMidnight(unix int64) time.Time {
	t := time.Unix(unix, 0).Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// hourRange returns the hourly buckets a query covers: those that start
// in [From, To). In time zones a fraction of an hour off UTC, a local day
// starts inside a bucket; the bucket counts towards the day it starts in,
// so that consecutive days never count it twice.
func hourRange(q *Query) (int64, int64) {
	from := (q.From + 3_599_999) / 3_600_000 * 3600
	to := (q.To + 999) / 1000
	return from, to
}

// Query summarizes the traffic q selects.
func (s *Store) Query(q Query) *Report {
	from, to := hourRange(&q)
	devices, all := s.gather(from, to)
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}

	selected := map[string]bool{}
	for _, d := range q.Devices {
		selected[d] = true
	}
	r := &Report{}

	// Buckets of the series.
	r.Bucket = q.Bucket
	if r.Bucket != "hour" && r.Bucket != "day" {
		r.Bucket = "hour"
		if to-from > 48*3600 {
			r.Bucket = "day"
		}
	}
	bucketOf := map[int64]int{} // hour → index into r.Series, -1 for none
	if r.Bucket == "hour" {
		for t := from; t < to; t += 3600 {
			bucketOf[t] = len(r.Series)
			r.Series = append(r.Series, Point{T: t * 1000})
		}
	} else {
		dayIndex := map[int64]int{} // local midnight → index into r.Series
		for d := localMidnight(from); d.Unix() < to; d = d.AddDate(0, 0, 1) {
			dayIndex[d.Unix()] = len(r.Series)
			r.Series = append(r.Series, Point{T: d.UnixMilli()})
		}
		for t := from; t < to; t += 3600 {
			if b, ok := dayIndex[localMidnight(t).Unix()]; ok {
				bucketOf[t] = b
			}
		}
	}

	type agg struct {
		Counter
		detail string
	}
	apps := map[string]*agg{}
	hosts := map[string]*agg{}
	chains := map[string]*agg{}
	routes := map[string]*agg{}
	subs := map[string]*agg{}
	subDevs := map[SubDevice]*Counter{}
	devTotals := map[string]*Counter{}
	add := func(m map[string]*agg, name string, c Counter) {
		a := m[name]
		if a == nil {
			a = &agg{}
			m[name] = a
		}
		a.Add(c)
	}

	s.dmu.Lock()
	for _, dr := range all {
		inDevices := len(selected) == 0 || selected[dr.device]
		for i, rows := range dr.rows {
			for j := range rows {
				row := &rows[j]
				if row.Hour < from || row.Hour >= to {
					continue
				}
				host := row.Host
				if q.GroupDomain {
					host = s.domain(host)
				}
				mApp := q.App == "" || row.App == q.App
				mHost := q.Host == "" || host == q.Host
				mChain := q.Chain == "" || row.Chain == q.Chain
				route := RouteOf(row.Chain)
				mRoute := q.Route == "" || route == q.Route
				sub := row.Sub
				if sub == "" && route == RouteProxy {
					sub = SubUnknown
				}
				mSub := (q.Sub == "" && !q.NoSub) || sub == q.Sub
				others := mApp && mHost && mChain // the filters each list respects
				if others && mRoute && mSub {
					dt := devTotals[dr.device]
					if dt == nil {
						dt = &Counter{}
						devTotals[dr.device] = dt
					}
					dt.Add(row.Counter)
				}
				if others && mRoute {
					k := SubDevice{Sub: sub, Device: dr.device}
					sd := subDevs[k]
					if sd == nil {
						sd = &Counter{}
						subDevs[k] = sd
					}
					sd.Add(row.Counter)
				}
				if !inDevices {
					continue
				}
				if others && mSub {
					add(routes, string(route), row.Counter)
				}
				if others && mRoute {
					add(subs, sub, row.Counter)
				}
				if !mRoute || !mSub {
					continue
				}
				if others {
					r.Up += row.Up
					r.Down += row.Down
					r.Conns += row.Conns
					if b, ok := bucketOf[row.Hour]; ok {
						r.Series[b].Up += row.Up
						r.Series[b].Down += row.Down
					}
				}
				if mHost && mChain {
					add(apps, row.App, row.Counter)
					if a := apps[row.App]; a.detail == "" {
						a.detail = dr.paths[i][row.App]
					}
				}
				if mApp && mChain {
					add(hosts, host, row.Counter)
				}
				if mApp && mHost {
					add(chains, row.Chain, row.Counter)
				}
			}
		}
	}
	s.dmu.Unlock()

	search := strings.ToLower(strings.TrimSpace(q.Search))
	list := func(m map[string]*agg, filter bool, limit int) ([]Item, int) {
		items := make([]Item, 0, len(m))
		for name, a := range m {
			if filter && search != "" && !Matches(name, search, q.SearchAlso) {
				continue
			}
			items = append(items, Item{Name: name, Up: a.Up, Down: a.Down, Conns: a.Conns, Detail: a.detail})
		}
		sort.Slice(items, func(i, j int) bool {
			ti, tj := items[i].Up+items[i].Down, items[j].Up+items[j].Down
			if ti != tj {
				return ti > tj
			}
			return items[i].Name < items[j].Name
		})
		n := len(items)
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		return items, n
	}
	sum := func(m map[string]*agg) int64 {
		var t int64
		for _, a := range m {
			t += a.Up + a.Down
		}
		return t
	}
	r.AppsTotal, r.HostsTotal, r.ChainsTotal, r.SubsTotal = sum(apps), sum(hosts), sum(chains), sum(subs)
	r.Apps, r.AppCount = list(apps, true, limit)
	r.Hosts, r.HostCount = list(hosts, true, limit)
	r.Chains, _ = list(chains, false, limit)
	// Few enough to list whole.
	r.Routes, _ = list(routes, false, len(routes))
	r.Subs, _ = list(subs, false, len(subs))
	for k, c := range subDevs {
		r.SubDevices = append(r.SubDevices, SubDevice{Sub: k.Sub, Device: k.Device, Up: c.Up, Down: c.Down})
	}
	sort.Slice(r.SubDevices, func(i, j int) bool {
		a, b := r.SubDevices[i], r.SubDevices[j]
		if a.Up+a.Down != b.Up+b.Down {
			return a.Up+a.Down > b.Up+b.Down
		}
		return a.Sub+a.Device < b.Sub+b.Device
	})

	self := s.Self().ID
	for _, d := range devices {
		t := DeviceTotal{Device: d, Self: d.ID == self}
		if c := devTotals[d.ID]; c != nil {
			t.Up, t.Down, t.Conns = c.Up, c.Down, c.Conns
		}
		r.Devices = append(r.Devices, t)
	}
	return r
}

// Export writes the rows q selects as CSV, one line per device, hour,
// application, host and proxy.
func (s *Store) Export(q Query, w io.Writer) error {
	from, to := hourRange(&q)
	devices, all := s.gather(from, to)
	names := map[string]string{}
	for _, d := range devices {
		names[d.ID] = d.Name
	}
	selected := map[string]bool{}
	for _, d := range q.Devices {
		selected[d] = true
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"time", "device", "app", "process", "host", "proxy", "route", "subscription", "upload_bytes", "download_bytes", "connections"}); err != nil {
		return err
	}
	type line struct {
		dev string
		Row
	}
	var lines []line
	for _, dr := range all {
		if len(selected) > 0 && !selected[dr.device] {
			continue
		}
		for _, rows := range dr.rows {
			for _, row := range rows {
				if row.Hour < from || row.Hour >= to {
					continue
				}
				host := s.hostOf(&q, row.Host)
				if (q.App != "" && row.App != q.App) || (q.Host != "" && host != q.Host) || (q.Chain != "" && row.Chain != q.Chain) ||
					(q.Route != "" && RouteOf(row.Chain) != q.Route) || ((q.Sub != "" || q.NoSub) && subOf(row) != q.Sub) {
					continue
				}
				row.Host = host
				lines = append(lines, line{dr.device, row})
			}
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].Hour != lines[j].Hour {
			return lines[i].Hour < lines[j].Hour
		}
		return lines[i].Up+lines[i].Down > lines[j].Up+lines[j].Down
	})
	for _, l := range lines {
		err := cw.Write([]string{
			time.Unix(l.Hour, 0).Local().Format("2006-01-02 15:04"),
			names[l.dev], l.App, l.Proc, l.Host, l.Chain, string(RouteOf(l.Chain)), subOf(l.Row),
			strconv.FormatInt(l.Up, 10), strconv.FormatInt(l.Down, 10), strconv.FormatInt(l.Conns, 10),
		})
		if err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// Matches reports whether name contains search, lower case, or one of
// also, the names the data gives what the page shows in another language.
func Matches(name, search string, also []string) bool {
	name = strings.ToLower(name)
	if strings.Contains(name, search) {
		return true
	}
	for _, a := range also {
		if a = strings.ToLower(a); a != "" && strings.Contains(name, a) {
			return true
		}
	}
	return false
}
