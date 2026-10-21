// Package mesh shares the traffic of the user's computers over Tailscale,
// without any setup: each copy of the app serves its own data files on its
// tailnet address, finds the other devices of the tailnet that run the app,
// and pulls what changed of their files into the store.
//
// Only the tailnet's own devices may read the files, which Tailscale vouches
// for: a connection from a tailnet address comes from that device. Devices
// shared into the tailnet from another one, and those of people the tailnet
// shares with, are neither served nor pulled from. Each device serves only
// the files it writes, so nothing is ever written by two devices, and a copy
// only ever replaces an older one.
package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"clashtraffic/internal/i18n"
	"clashtraffic/internal/store"
	"clashtraffic/internal/tailnet"
)

const (
	appName  = "clash-traffic"
	protocol = 1

	PathHello = "/clash-traffic/v1/hello"
	PathFile  = "/clash-traffic/v1/file/"

	// ScanInterval is how often the tailnet is scanned and peers pulled.
	ScanInterval = time.Minute
)

// LiveSummary is what a device's core does right now.
type LiveSummary struct {
	Connected bool  `json:"connected"` // the app reaches its Clash core
	UpRate    int64 `json:"upRate"`
	DownRate  int64 `json:"downRate"`
}

// FileInfo is a file a device serves.
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Tag  string `json:"tag"` // changes whenever the file does
}

// Hello is what a device answers about itself.
type Hello struct {
	App      string           `json:"app"`
	Protocol int              `json:"protocol"`
	Device   store.DeviceInfo `json:"device"`
	Files    []FileInfo       `json:"files"`
	Live     LiveSummary      `json:"live"`
}

// PeerState tells what a device of the tailnet is to the app.
type PeerState string

const (
	PeerApp     PeerState = "app"     // runs the app: its traffic is shared
	PeerNoApp   PeerState = "noapp"   // online, without the app
	PeerOffline PeerState = "offline" // not connected to the tailnet
)

// PeerStatus is a desktop device of the tailnet.
type PeerStatus struct {
	Host       string      `json:"host"`    // its tailnet name
	DNSName    string      `json:"dnsName"` // e.g. mac.tail1234.ts.net
	IP         string      `json:"ip"`
	OS         string      `json:"os"`
	State      PeerState   `json:"state"`
	Error      string      `json:"error"`    // why it did not answer
	DeviceID   string      `json:"deviceId"` // its device in the store, with the app
	DeviceName string      `json:"deviceName"`
	LastSeen   int64       `json:"lastSeen"` // unix ms it last answered
	LastPull   int64       `json:"lastPull"` // unix ms its files were last pulled
	Pulled     int         `json:"pulled"`   // files the last pull fetched
	Live       LiveSummary `json:"live"`
}

// MeshStatus is the state of sharing over Tailscale.
type MeshStatus struct {
	Enabled    bool         `json:"enabled"`
	Available  bool         `json:"available"` // Tailscale is installed and connected
	Error      string       `json:"error"`
	Tailnet    string       `json:"tailnet"`
	SelfHost   string       `json:"selfHost"`
	SelfIP     string       `json:"selfIP"`
	Port       int          `json:"port"`
	Serving    bool         `json:"serving"`
	ServeError string       `json:"serveError"`
	Scanning   bool         `json:"scanning"`
	LastScan   int64        `json:"lastScan"` // unix ms
	Peers      []PeerStatus `json:"peers"`
}

// Node serves this device's files and pulls the other devices'.
type Node struct {
	root      string // the store's root
	self      string // this device's id
	port      int
	statePath string

	// Hello describes this device; Files is filled in by the node.
	Hello func() Hello
	// Enabled reports whether sharing is on.
	Enabled func() bool
	// OnChange gets the status whenever it changes.
	OnChange func(MeshStatus)
	// OnData is called when files of other devices arrived.
	OnData func()
	// TailnetStatus reads the tailnet; tailnet.Get unless replaced in tests.
	TailnetStatus func(ctx context.Context) (*tailnet.Status, error)
	// PeerURL returns the base URL of a peer; its tailnet address and the
	// node's port unless replaced in tests.
	PeerURL func(ip netip.Addr) string

	client  *http.Client
	trigger chan struct{}

	mu       sync.Mutex
	status   MeshStatus
	allowed  map[netip.Addr]bool
	tags     map[string]string // device/name → tag of the copy pulled
	server   *http.Server
	listenOn string
	peers    map[string]PeerStatus // by tailnet IP, kept across scans
}

// New returns a node for the store at root of device self, serving on
// port, which keeps what it pulled in statePath.
func New(root, self string, port int, statePath string) *Node {
	n := &Node{
		root: root, self: self, port: port, statePath: statePath,
		TailnetStatus: tailnet.Get,
		client: &http.Client{
			// Straight to the peer: never through a proxy of the environment.
			Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second},
			Timeout:   30 * time.Second,
		},
		trigger: make(chan struct{}, 1),
		allowed: map[netip.Addr]bool{},
		tags:    map[string]string{},
		peers:   map[string]PeerStatus{},
	}
	n.status.Port = port
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &n.tags)
	}
	return n
}

// Status returns the state of sharing.
func (n *Node) Status() MeshStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.status
	st.Peers = append([]PeerStatus(nil), st.Peers...)
	return st
}

// Trigger scans soon.
func (n *Node) Trigger() {
	select {
	case n.trigger <- struct{}{}:
	default:
	}
}

// Run scans the tailnet every minute, and when triggered, until ctx ends.
func (n *Node) Run(ctx context.Context) {
	defer n.stopServer()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-n.trigger:
		}
		n.scan(ctx)
		timer.Reset(ScanInterval)
	}
}

func (n *Node) update(fn func(*MeshStatus)) {
	n.mu.Lock()
	fn(&n.status)
	st := n.status
	st.Peers = append([]PeerStatus(nil), st.Peers...)
	n.mu.Unlock()
	if n.OnChange != nil {
		n.OnChange(st)
	}
}

func (n *Node) scan(ctx context.Context) {
	if n.Enabled != nil && !n.Enabled() {
		n.stopServer()
		n.update(func(s *MeshStatus) { *s = MeshStatus{Port: n.port, Error: i18n.T("Off")} })
		return
	}
	n.update(func(s *MeshStatus) { s.Enabled, s.Scanning = true, true })
	st, err := n.TailnetStatus(ctx)
	if err != nil || st == nil || !st.Running() {
		n.stopServer()
		msg := i18n.T("Tailscale is not connected")
		if err != nil {
			msg = err.Error()
		}
		n.update(func(s *MeshStatus) {
			*s = MeshStatus{Enabled: true, Port: n.port, Error: msg, LastScan: time.Now().UnixMilli()}
		})
		return
	}

	// Who may read: this device and the tailnet's own devices.
	allowed := map[netip.Addr]bool{}
	for _, ip := range st.Self.IPs {
		allowed[ip] = true
	}
	for _, p := range st.Peers {
		if st.Own(p) {
			for _, ip := range p.IPs {
				allowed[ip] = true
			}
		}
	}
	n.mu.Lock()
	n.allowed = allowed
	n.mu.Unlock()

	selfIP, _ := st.Self.IPv4()
	serveErr := n.serve(selfIP)

	var cands []tailnet.Node
	for _, p := range st.Peers {
		if st.Own(p) && p.Desktop() {
			cands = append(cands, p)
		}
	}
	results := make([]PeerStatus, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	pulledAny := false
	var pmu sync.Mutex
	for i, p := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ps, pulled := n.visit(ctx, p)
			results[i] = ps
			if pulled > 0 {
				pmu.Lock()
				pulledAny = true
				pmu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		rank := map[PeerState]int{PeerApp: 0, PeerNoApp: 1, PeerOffline: 2}
		if rank[results[i].State] != rank[results[j].State] {
			return rank[results[i].State] < rank[results[j].State]
		}
		return results[i].Host < results[j].Host
	})
	n.saveTags()

	n.update(func(s *MeshStatus) {
		s.Enabled, s.Available, s.Error, s.Scanning = true, true, "", false
		s.Tailnet, s.SelfHost, s.SelfIP = st.Tailnet, st.Self.HostName, selfIP.String()
		s.Serving, s.ServeError = serveErr == nil, ""
		if serveErr != nil {
			s.ServeError = serveErr.Error()
		}
		s.LastScan = time.Now().UnixMilli()
		s.Peers = results
	})
	if pulledAny && n.OnData != nil {
		n.OnData()
	}
}

// visit asks a peer whether it runs the app, and pulls its changed files.
func (n *Node) visit(ctx context.Context, p tailnet.Node) (PeerStatus, int) {
	ip, _ := p.IPv4()
	key := ip.String()
	n.mu.Lock()
	ps := n.peers[key]
	n.mu.Unlock()
	ps.Host, ps.DNSName, ps.IP, ps.OS = p.HostName, p.DNSName, key, p.OS
	ps.Error = ""
	defer func() {
		n.mu.Lock()
		n.peers[key] = ps
		n.mu.Unlock()
	}()
	if !ip.IsValid() {
		ps.State = PeerOffline
		return ps, 0
	}
	base := "http://" + net.JoinHostPort(key, strconv.Itoa(n.port))
	if n.PeerURL != nil {
		base = n.PeerURL(ip)
	}
	// Tailscale's online flag lags, and is wrong while its control
	// connection is down: devices it calls offline are tried too, briefly.
	timeout := 4 * time.Second
	if !p.Online {
		timeout = 2 * time.Second
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var h Hello
	if err := n.getJSON(hctx, base+PathHello, &h); err != nil {
		ps.State, ps.Error = PeerNoApp, shortError(err)
		if !p.Online {
			ps.State, ps.Error = PeerOffline, ""
		}
		return ps, 0
	}
	if h.App != appName || h.Protocol != protocol || !store.ValidDeviceID(h.Device.ID) || strings.EqualFold(h.Device.ID, n.self) {
		ps.State, ps.Error = PeerNoApp, i18n.T("not a Clash Traffic this one knows")
		return ps, 0
	}
	ps.State = PeerApp
	ps.DeviceID, ps.DeviceName = h.Device.ID, h.Device.Name
	ps.LastSeen = time.Now().UnixMilli()
	ps.Live = h.Live

	pulled, err := n.pull(ctx, base, h)
	ps.Pulled = pulled
	if err != nil {
		ps.Error = shortError(err)
	} else {
		ps.LastPull = time.Now().UnixMilli()
	}
	return ps, pulled
}

func (n *Node) pull(ctx context.Context, base string, h Hello) (int, error) {
	dev := h.Device.ID
	var errs []error
	pulled := 0
	// The device file last, so that it never claims data not pulled yet.
	files := append([]FileInfo(nil), h.Files...)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Name != store.DeviceFile && files[j].Name == store.DeviceFile
	})
	for _, f := range files {
		if !store.ValidFileName(f.Name) {
			continue
		}
		key := dev + "/" + f.Name
		local := filepath.Join(n.root, dev, f.Name)
		n.mu.Lock()
		have := n.tags[key] == f.Tag
		n.mu.Unlock()
		if have {
			if _, err := os.Stat(local); err == nil {
				continue
			}
		}
		data, err := n.get(ctx, base+PathFile+f.Name, store.MaxFileBytes)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Name, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		// A copy that came another way (a shared folder, WebDAV) may be newer.
		wrote, err := store.ReplaceIfNewer(local, f.Name, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if wrote {
			pulled++
		}
		n.mu.Lock()
		n.tags[key] = f.Tag
		n.mu.Unlock()
	}
	return pulled, errors.Join(errs...)
}

func (n *Node) getJSON(ctx context.Context, url string, v any) error {
	b, err := n.get(ctx, url, 4<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (n *Node) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return store.ReadLimited(resp.Body, limit)
}

func (n *Node) saveTags() {
	n.mu.Lock()
	b, err := json.Marshal(n.tags)
	n.mu.Unlock()
	if err == nil {
		_ = store.WriteFileAtomic(n.statePath, b)
	}
}

// serve listens on the tailnet address, moving when it changed.
func (n *Node) serve(ip netip.Addr) error {
	if !ip.IsValid() {
		n.stopServer()
		return errors.New(i18n.T("This computer has no Tailscale IPv4 address"))
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(n.port))
	n.mu.Lock()
	running := n.server != nil && n.listenOn == addr
	n.mu.Unlock()
	if running {
		return nil
	}
	n.stopServer()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return i18n.Errorf("cannot listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: n, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	n.mu.Lock()
	n.server, n.listenOn = srv, addr
	n.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (n *Node) stopServer() {
	n.mu.Lock()
	srv := n.server
	n.server, n.listenOn = nil, ""
	n.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// ServeHTTP answers the devices of the tailnet.
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	n.mu.Lock()
	ok := err == nil && n.allowed[ap.Addr().Unmap()]
	n.mu.Unlock()
	if !ok {
		n.Trigger() // a device that just joined the tailnet is let in after a scan
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case r.URL.Path == PathHello:
		h := Hello{}
		if n.Hello != nil {
			h = n.Hello()
		}
		h.App, h.Protocol = appName, protocol
		h.Device.ID = n.self
		h.Files = n.ownFiles()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
	case strings.HasPrefix(r.URL.Path, PathFile):
		name := strings.TrimPrefix(r.URL.Path, PathFile)
		if !store.ValidFileName(name) {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(n.root, n.self, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// ownFiles lists the files this device writes.
func (n *Node) ownFiles() []FileInfo {
	entries, _ := os.ReadDir(filepath.Join(n.root, n.self))
	var out []FileInfo
	for _, e := range entries {
		if !e.Type().IsRegular() || !store.ValidFileName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileInfo{Name: e.Name(), Size: info.Size(), Tag: fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())})
	}
	return out
}

func shortError(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return i18n.T("Timed out")
	case strings.Contains(err.Error(), "connection refused"):
		return i18n.T("Does not run Clash Traffic")
	}
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
