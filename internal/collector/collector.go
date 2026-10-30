// Package collector turns snapshots of the core's open connections into
// traffic counters. Connections report the bytes they carried since they
// opened; the collector counts what each carried since the last snapshot.
//
// A connection that closes between two snapshots takes the bytes it
// carried after the first one with it, and one that opens and closes
// between them is never seen. The core's totals still count them, so the
// collector hands the difference to the connections that just closed, in
// proportion to how busy they were, or else to a bucket of its own. The
// counted totals so match the core's.
//
// The core reads its totals and its connections' counters at slightly
// different moments, so a difference may be a skew the next snapshot takes
// back. The collector keeps the difference as a balance and hands out only
// what stayed positive over two snapshots.
package collector

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"sort"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/procname"
	"clashtraffic/internal/sessions"
	"clashtraffic/internal/store"
)

// Names of the counters the collector makes up.
const (
	AppUnknown   = "未识别进程"
	AppShortConn = "短连接（未捕获）"
	HostUnknown  = "（未知）"
	SubUnknown   = store.SubUnknown // proxied, but no subscription could be named
	LANPrefix    = "局域网 "
)

// Sink receives the counted traffic: the store.
type Sink interface {
	Add(k store.Key, path string, c store.Counter)
}

type tracked struct {
	up, down         int64 // bytes counted so far
	baseUp, baseDown int64 // bytes carried before the user cleared storage
	cutAt            int64 // sessions begin again at a clear
	lastUp, lastDown int64 // counted at the last snapshot
	seen             int64 // unix ms of the last snapshot that had it
	keyed            bool
	app, proc, host  string
	chain, path      string
	route            store.Route
	sub              string

	// What the session log keeps of it.
	id, dest, source string
	network, inbound string
	chains, rule     string
	start            int64
}

// LiveConn is an open connection.
type LiveConn struct {
	ID       string      `json:"id"`
	App      string      `json:"app"`
	Process  string      `json:"process"`
	Host     string      `json:"host"`
	Dest     string      `json:"dest"`
	Chain    string      `json:"chain"`
	Chains   string      `json:"chains"` // the whole chain, e.g. "Proxy → HK 01"
	Route    store.Route `json:"route"`
	Sub      string      `json:"sub"`
	Source   string      `json:"source"`
	Rule     string      `json:"rule"`
	Network  string      `json:"network"`
	Inbound  string      `json:"inbound"`
	Up       int64       `json:"up"`
	Down     int64       `json:"down"`
	UpRate   int64       `json:"upRate"`   // bytes per second
	DownRate int64       `json:"downRate"` // bytes per second
	Start    int64       `json:"start"`    // unix ms
}

// Live is what the core does right now.
type Live struct {
	T        int64      `json:"t"` // unix ms
	UpRate   int64      `json:"upRate"`
	DownRate int64      `json:"downRate"`
	CoreUp   int64      `json:"coreUp"`   // bytes since the core started
	CoreDown int64      `json:"coreDown"` // bytes since the core started
	Conns    []LiveConn `json:"conns"`
	Total    int        `json:"total"` // open connections, Conns may list fewer
	// Run and Seq order snapshots: Seq counts the snapshots of one run of
	// the app, whatever the clock does.
	Run string `json:"run"`
	Seq int64  `json:"seq"`
}

// Collector counts the traffic of successive snapshots.
type Collector struct {
	sink Sink
	// OnClose, when set, gets each connection that went away.
	OnClose func(sessions.Session)
	// CurrentSub, when set, names the subscription in use, which proxied
	// connections whose node comes from no proxy provider count towards.
	CurrentSub func() string
	run        string
	seq        int64
	// countFrom is when counting began: the bytes a connection open
	// before then carried earlier are not counted.
	countFrom time.Time
	clearedAt int64
	prev      map[string]*tracked
	started   bool
	coreUp    int64
	coreDown  int64
	// The difference to the core's totals not counted yet, as it was at
	// the last snapshot and before.
	balUp, balDown         int64
	prevBalUp, prevBalDown int64
	// Connections that closed at the last snapshot: the difference the
	// next one hands out came from them as much as from those closing then.
	lastClosed []*tracked
	// verify asks the next snapshot whether the core is the one counted
	// before a reconnect, or before the app restarted (restored).
	verify     bool
	restored   bool
	endpoint   string // the controller the counts came from
	lastTick   time.Time
	localIPs   map[netip.Addr]bool
	localsTime time.Time
}

// New returns a collector that counts into sink from now on.
func New(sink Sink) *Collector {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return &Collector{sink: sink, prev: map[string]*tracked{}, countFrom: time.Now(), run: hex.EncodeToString(b)}
}

// Reset forgets the last snapshot, after the core went away: the next
// snapshot starts counting again, without the difference to the totals.
func (c *Collector) Reset() {
	c.started = false
	c.resetBalance()
}

// Clear starts a new counting run. Existing connections stay open, but
// their old bytes must not return to reports, live counters or sessions.
func (c *Collector) Clear(now time.Time, snap *clash.Snapshot) Live {
	fresh := New(c.sink)
	fresh.OnClose, fresh.CurrentSub = c.OnClose, c.CurrentSub
	fresh.endpoint = c.endpoint
	fresh.countFrom, fresh.clearedAt = now, now.UnixMilli()
	*c = *fresh
	if snap == nil {
		return Live{T: now.UnixMilli(), Run: c.run, Conns: []LiveConn{}}
	}
	return c.Tick(now, snap)
}

// Connected tells the collector the controller it reads from now, at each
// connection to the core, the first of a run of the app included. Another
// controller than the counts came from is another core: counting starts
// over. The same one, maybe after a gap or a restart of the app, goes on if
// the next snapshot shows the core is the same.
func (c *Collector) Connected(endpoint string) {
	switch {
	case c.endpoint != "" && endpoint != c.endpoint:
		c.Reset()
	case c.started:
		c.verify = true
	}
	c.endpoint = endpoint
}

func (c *Collector) resetBalance() {
	c.balUp, c.balDown, c.prevBalUp, c.prevBalDown = 0, 0, 0, 0
	c.lastClosed = nil
}

// Tick counts a snapshot taken at now and returns the live view of it.
func (c *Collector) Tick(now time.Time, snap *clash.Snapshot) Live {
	hour := now.Unix() - now.Unix()%3600
	first := !c.started
	if c.started && (snap.UploadTotal < c.coreUp || snap.DownloadTotal < c.coreDown) {
		// The core restarted: its connections are all new.
		first = true
		for _, t := range c.prev {
			c.closed(t)
		}
		c.prev = map[string]*tracked{}
		c.resetBalance()
	}
	if c.verify && !first {
		// Back after a gap. A connection open before it is open still: the
		// same core. After a reconnect, a short gap is taken for the same
		// core too. After a restart, the totals saved count on only when it
		// was quick and the core the same: the gap's difference is then a
		// moment's, not the hours the app may have been closed.
		gap := now.Sub(c.lastTick)
		overlap := false
		for i := range snap.Connections {
			if c.prev[snap.Connections[i].ID] != nil {
				overlap = true
				break
			}
		}
		same := overlap || gap <= time.Minute
		if c.restored {
			same = gap <= time.Minute && (overlap || len(c.prev) == 0)
		}
		if !same {
			first = true
			c.resetBalance()
		}
	}
	c.verify, c.restored = false, false
	dt := now.Sub(c.lastTick).Seconds()
	if first || dt <= 0 {
		dt = 0
	}
	c.refreshLocals(now)

	c.seq++
	live := Live{T: now.UnixMilli(), CoreUp: snap.UploadTotal, CoreDown: snap.DownloadTotal, Run: c.run, Seq: c.seq}
	if !first && dt > 0 {
		live.UpRate = int64(float64(snap.UploadTotal-c.coreUp) / dt)
		live.DownRate = int64(float64(snap.DownloadTotal-c.coreDown) / dt)
	}

	seen := make(map[string]bool, len(snap.Connections))
	var attrUp, attrDown int64
	live.Conns = make([]LiveConn, 0, len(snap.Connections))
	for i := range snap.Connections {
		conn := &snap.Connections[i]
		// mihomo lists dialer-proxy hops as Inner connections, but their
		// bytes are already carried by the application's connection and do
		// not contribute to the core's totals. Counting them again inflates
		// applications, subscriptions and sessions (often as AppUnknown).
		if conn.Metadata.Type == "Inner" {
			delete(c.prev, conn.ID) // also discard a pre-fix checkpoint entry
			continue
		}
		seen[conn.ID] = true
		t := c.prev[conn.ID]
		isNew := t == nil
		if isNew {
			t = &tracked{}
			if conn.Start.Before(c.countFrom) {
				// Open before counting began: count what it carries from now.
				t.up, t.down = conn.Upload, conn.Download
				if c.clearedAt != 0 {
					t.baseUp, t.baseDown, t.cutAt = conn.Upload, conn.Download, c.clearedAt
				}
				isNew = false
			}
			c.prev[conn.ID] = t
		}
		if !t.keyed {
			c.key(t, conn)
		} else if t.app == AppUnknown {
			// A lookup can succeed after the first snapshot. Keep the original
			// route/subscription and counters; only later deltas change owner.
			if app := procname.App(conn.Metadata.Process, conn.Metadata.ProcessPath); app != "" {
				t.app, t.proc, t.path = app, conn.Metadata.Process, conn.Metadata.ProcessPath
			}
		}
		du, dd := max(conn.Upload-t.up, 0), max(conn.Download-t.down, 0)
		t.up, t.down = conn.Upload, conn.Download
		t.seen = live.T
		t.lastUp, t.lastDown = du, dd
		var conns int64
		if isNew {
			conns = 1
		}
		c.sink.Add(t.key(hour), t.path, store.Counter{Up: du, Down: dd, Conns: conns})
		attrUp += du
		attrDown += dd

		lc := LiveConn{
			ID: conn.ID, App: t.app, Process: t.proc, Host: t.host, Dest: t.dest,
			Chain: t.chain, Chains: t.chains, Route: t.route, Sub: t.sub, Source: t.source,
			Rule: t.rule, Network: t.network, Inbound: t.inbound,
			Up: max(conn.Upload-t.baseUp, 0), Down: max(conn.Download-t.baseDown, 0), Start: t.start,
		}
		if dt > 0 {
			lc.UpRate = int64(float64(du) / dt)
			lc.DownRate = int64(float64(dd) / dt)
		}
		live.Conns = append(live.Conns, lc)
	}
	live.Total = len(live.Conns)

	var closed []*tracked
	for id, t := range c.prev {
		if !seen[id] {
			if t.keyed {
				closed = append(closed, t)
				c.closed(t)
			}
			delete(c.prev, id)
		}
	}
	if !first {
		cands := slices.Concat(c.lastClosed, closed)
		c.balUp, c.prevBalUp = c.settle(hour, c.balUp+snap.UploadTotal-c.coreUp-attrUp, c.prevBalUp, cands, true)
		c.balDown, c.prevBalDown = c.settle(hour, c.balDown+snap.DownloadTotal-c.coreDown-attrDown, c.prevBalDown, cands, false)
	}
	c.lastClosed = closed

	c.started = true
	c.coreUp, c.coreDown = snap.UploadTotal, snap.DownloadTotal
	c.lastTick = now

	sort.Slice(live.Conns, func(i, j int) bool {
		a, b := live.Conns[i], live.Conns[j]
		if ra, rb := a.UpRate+a.DownRate, b.UpRate+b.DownRate; ra != rb {
			return ra > rb
		}
		return a.Start > b.Start
	})
	return live
}

// settle hands out the part of the balance that stayed positive since the
// last snapshot, and returns the balance left, twice: as the new balance
// and as the previous one for the next snapshot.
func (c *Collector) settle(hour, bal, prev int64, cands []*tracked, up bool) (int64, int64) {
	const maxDebt = 64 << 20
	if give := min(bal, prev); give > 0 {
		c.spread(hour, give, cands, up)
		bal -= give
	}
	bal = max(bal, -maxDebt)
	return bal, bal
}

// spread counts bytes the snapshots missed: in proportion to what the
// connections that just closed carried last, or else as short connections.
func (c *Collector) spread(hour, n int64, cands []*tracked, up bool) {
	counter := func(n int64) store.Counter {
		if up {
			return store.Counter{Up: n}
		}
		return store.Counter{Down: n}
	}
	last := func(t *tracked) int64 {
		if up {
			return t.lastUp
		}
		return t.lastDown
	}
	if len(cands) == 0 {
		c.sink.Add(store.Key{Hour: hour, App: AppShortConn, Host: HostUnknown}, "", counter(n))
		return
	}
	// Weigh them by what they carried last, plus a little, so that idle
	// ones get a share too.
	var total int64
	for _, t := range cands {
		total += last(t) + 1024
	}
	left := n
	for i, t := range cands {
		share := left
		if i < len(cands)-1 {
			share = min(int64(float64(n)*float64(last(t)+1024)/float64(total)), left)
		}
		left -= share
		c.sink.Add(t.key(hour), t.path, counter(share))
	}
}

// key works out what a connection's traffic is counted under.
func (c *Collector) key(t *tracked, conn *clash.Conn) {
	m := conn.Metadata
	t.keyed = true
	t.proc = m.Process
	t.path = m.ProcessPath
	t.app = procname.App(m.Process, m.ProcessPath)
	if t.app == "" {
		if c.isLAN(m.SourceIP) {
			t.app = LANPrefix + m.SourceIP
		} else {
			t.app = AppUnknown
		}
	}
	t.host = m.Host
	if t.host == "" {
		t.host = m.SniffHost
	}
	if t.host == "" {
		t.host = m.DestinationIP
	}
	if t.host == "" {
		t.host = HostUnknown
	}
	if len(conn.Chains) > 0 {
		t.chain = conn.Chains[0]
	}
	t.route = store.RouteOf(t.chain)
	if t.route == store.RouteProxy {
		for _, p := range conn.ProviderChains {
			if p != "" {
				t.sub = p
				break
			}
		}
		if t.sub == "" && c.CurrentSub != nil {
			t.sub = c.CurrentSub()
		}
		if t.sub == "" {
			t.sub = SubUnknown
		}
	}

	t.id = conn.ID
	t.start = max(conn.Start.UnixMilli(), t.cutAt)
	t.source = m.SourceIP
	t.network = m.Network
	t.inbound = m.InboundName
	t.rule = conn.Rule
	if conn.RulePayload != "" {
		t.rule += "(" + conn.RulePayload + ")"
	}
	// The rule's group first, down to the node the connection left through.
	for j := len(conn.Chains) - 1; j >= 0; j-- {
		if t.chains != "" {
			t.chains += " → "
		}
		t.chains += conn.Chains[j]
	}
	if m.DestinationIP != "" {
		t.dest = net.JoinHostPort(m.DestinationIP, m.DestinationPort)
	} else if m.DestinationPort != "" {
		t.dest = ":" + m.DestinationPort
	}
}

func (t *tracked) key(hour int64) store.Key {
	return store.Key{Hour: hour, App: t.app, Proc: t.proc, Host: t.host, Chain: t.chain, Sub: t.sub}
}

func (t *tracked) session(open bool) sessions.Session {
	return sessions.Session{
		ID: t.id, Start: t.start, End: t.seen, Open: open,
		App: t.app, Process: t.proc, Path: t.path, Host: t.host, Dest: t.dest, Source: t.source,
		Network: t.network, Inbound: t.inbound, Chain: t.chain, Chains: t.chains, Route: t.route, Sub: t.sub, Rule: t.rule,
		Up: max(t.up-t.baseUp, 0), Down: max(t.down-t.baseDown, 0),
	}
}

// closed hands a connection that went away to OnClose.
func (c *Collector) closed(t *tracked) {
	if c.OnClose != nil && t.keyed && t.seen > 0 {
		c.OnClose(t.session(false))
	}
}

// OpenSessions returns the connections open at the last snapshot, for the
// session log to keep when the app quits.
func (c *Collector) OpenSessions() []sessions.Session {
	var out []sessions.Session
	for _, t := range c.prev {
		if t.keyed && t.seen > 0 {
			out = append(out, t.session(true))
		}
	}
	return out
}

var tunRange = netip.MustParsePrefix("198.18.0.0/15")

// isLAN reports whether a connection came from another computer of the
// local network, through allow-lan.
func (c *Collector) isLAN(src string) bool {
	ip, err := netip.ParseAddr(src)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsUnspecified() || tunRange.Contains(ip) || c.localIPs[ip] {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func (c *Collector) refreshLocals(now time.Time) {
	if c.localIPs != nil && now.Sub(c.localsTime) < time.Minute {
		return
	}
	c.localsTime = now
	c.localIPs = map[netip.Addr]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			c.localIPs[p.Addr().Unmap()] = true
		}
	}
}

type savedConn struct {
	Up       int64 `json:"up"`
	Down     int64 `json:"down"`
	BaseUp   int64 `json:"baseUp,omitempty"`
	BaseDown int64 `json:"baseDown,omitempty"`
	CutAt    int64 `json:"cutAt,omitempty"`
}

type saved struct {
	AccountingVersion int                  `json:"accountingVersion,omitempty"`
	ClearedAt         int64                `json:"clearedAt,omitempty"`
	Saved             int64                `json:"saved"` // unix ms of the snapshot the counts are of
	Gen               int64                `json:"gen"`   // the store's generation they go with
	Conns             map[string]savedConn `json:"conns"`

	// The core's totals at that snapshot and the difference to them not
	// counted yet, which a quick restart goes on with.
	Endpoint    string        `json:"endpoint"`
	Started     bool          `json:"started"`
	CoreUp      int64         `json:"coreUp"`
	CoreDown    int64         `json:"coreDown"`
	BalUp       int64         `json:"balUp"`
	BalDown     int64         `json:"balDown"`
	PrevBalUp   int64         `json:"prevBalUp"`
	PrevBalDown int64         `json:"prevBalDown"`
	LastClosed  []savedClosed `json:"lastClosed"`
}

// Version 1 excludes Inner hops. Older balances and closed candidates may
// include duplicate traffic; keep their raw connection offsets, not the debt.
const accountingVersion = 1

// savedClosed is a connection that closed at the snapshot saved: the
// difference still to hand out goes to it.
type savedClosed struct {
	App, Proc, Host, Chain, Sub, Path string
	LastUp, LastDown                  int64
}

// ErrStaleCheckpoint is returned by Load when the store was written after
// the checkpoint: counting on from it would count some bytes twice.
var ErrStaleCheckpoint = errors.New("collector checkpoint older than the store")

// Save writes what the open connections were counted for, so that a new
// run of the app goes on from there instead of counting them again. Call it
// right after flushing the store, with the store's generation, so that a
// new run can tell whether both agree.
func (c *Collector) Save(path string, gen int64) error {
	at := c.lastTick
	if at.IsZero() {
		at = c.countFrom
	}
	s := saved{AccountingVersion: accountingVersion, Saved: at.UnixMilli(), ClearedAt: c.clearedAt, Gen: gen, Conns: make(map[string]savedConn, len(c.prev))}
	for id, t := range c.prev {
		s.Conns[id] = savedConn{Up: t.up, Down: t.down, BaseUp: t.baseUp, BaseDown: t.baseDown, CutAt: t.cutAt}
	}
	s.Endpoint = c.endpoint
	if c.started {
		s.Started = true
		s.CoreUp, s.CoreDown = c.coreUp, c.coreDown
		s.BalUp, s.BalDown, s.PrevBalUp, s.PrevBalDown = c.balUp, c.balDown, c.prevBalUp, c.prevBalDown
		for _, t := range c.lastClosed {
			s.LastClosed = append(s.LastClosed, savedClosed{App: t.app, Proc: t.proc, Host: t.host, Chain: t.chain, Sub: t.sub, Path: t.path, LastUp: t.lastUp, LastDown: t.lastDown})
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(path, b)
}

// Load reads what Save wrote, if it goes with the store's generation gen:
// counting goes on from when it was saved. Otherwise the connections open
// now count only from now on, which may miss a little but never counts
// twice.
func (c *Collector) Load(path string, gen int64) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s saved
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s.Gen != gen {
		return ErrStaleCheckpoint
	}
	for id, sc := range s.Conns {
		c.prev[id] = &tracked{up: sc.Up, down: sc.Down, baseUp: sc.BaseUp, baseDown: sc.BaseDown, cutAt: sc.CutAt}
	}
	c.clearedAt = s.ClearedAt
	c.endpoint = s.Endpoint
	if s.Started {
		c.started, c.verify, c.restored = true, true, true
		c.lastTick = time.UnixMilli(s.Saved)
		c.coreUp, c.coreDown = s.CoreUp, s.CoreDown
		c.resetBalance()
		if s.AccountingVersion == accountingVersion {
			c.balUp, c.balDown, c.prevBalUp, c.prevBalDown = s.BalUp, s.BalDown, s.PrevBalUp, s.PrevBalDown
			for _, sc := range s.LastClosed {
				c.lastClosed = append(c.lastClosed, &tracked{keyed: true, app: sc.App, proc: sc.Proc, host: sc.Host,
					chain: sc.Chain, sub: sc.Sub, path: sc.Path, lastUp: sc.LastUp, lastDown: sc.LastDown})
			}
		}
	}
	// Connections that opened since were never seen: count them whole.
	if saved := time.UnixMilli(s.Saved); saved.Before(c.countFrom) {
		c.countFrom = saved
	}
	return nil
}
