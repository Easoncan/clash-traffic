package mesh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clashtraffic/internal/store"
	"clashtraffic/internal/tailnet"
)

const (
	macID = "00000000a11e"
	winID = "00000000b0b0"
)

type device struct {
	store *store.Store
	node  *Node
	srv   *httptest.Server
	ip    netip.Addr
}

func newDevice(t *testing.T, id, ip string) *device {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "data"), store.DeviceInfo{ID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	n := New(s.Root(), id, 47399, filepath.Join(dir, "mesh.json"))
	n.Hello = func() Hello { return Hello{Device: s.Self(), Live: LiveSummary{Connected: true, DownRate: 42}} }
	d := &device{store: s, node: n, srv: httptest.NewServer(n), ip: netip.MustParseAddr(ip)}
	t.Cleanup(d.srv.Close)
	return d
}

// tailnetOf makes every device see the others, the test servers' loopback
// address standing for each one's tailnet address.
// extra peers every device sees besides the test's devices.
var extra []tailnet.Node

func tailnetOf(devs ...*device) { tailnetWith(false, devs...) }

func tailnetWith(offline bool, devs ...*device) {
	urls := map[netip.Addr]string{}
	for _, d := range devs {
		urls[d.ip] = d.srv.URL
	}
	for _, d := range devs {
		self := d
		d.node.PeerURL = func(ip netip.Addr) string { return urls[ip] }
		d.node.TailnetStatus = func(context.Context) (*tailnet.Status, error) {
			st := &tailnet.Status{State: "Running", Tailnet: "test", DNSSuffix: "tail1234.ts.net",
				Self: tailnet.Node{HostName: self.store.Self().ID, OS: "macOS", IPs: []netip.Addr{self.ip, netip.MustParseAddr("127.0.0.1")}}}
			for _, o := range devs {
				if o != self {
					st.Peers = append(st.Peers, tailnet.Node{HostName: o.store.Self().ID, DNSName: o.store.Self().ID + ".tail1234.ts.net",
						OS: "windows", Online: true, IPs: []netip.Addr{o.ip}})
				}
			}
			for _, x := range extra {
				st.Peers = append(st.Peers, x)
			}
			st.Peers = append(st.Peers, tailnet.Node{HostName: "phone", DNSName: "phone.tail1234.ts.net", OS: "android", Online: true, IPs: []netip.Addr{netip.MustParseAddr("100.64.0.99")}})
			if offline {
				// Tailscale calls every peer offline; they still answer.
				for i := range st.Peers {
					st.Peers[i].Online = false
				}
			}
			return st, nil
		}
	}
}

func TestPull(t *testing.T) {
	h := time.Now().Unix() - time.Now().Unix()%3600
	mac := newDevice(t, macID, "100.64.0.1")
	win := newDevice(t, winID, "100.64.0.2")
	tailnetOf(mac, win)
	mac.store.Add(store.Key{Hour: h, App: "Chrome", Host: "google.com", Chain: "HK", Sub: "A"}, "", store.Counter{Down: 100})
	win.store.Add(store.Key{Hour: h, App: "Edge", Host: "bing.com", Chain: "DIRECT"}, "", store.Counter{Down: 40})
	for _, d := range []*device{mac, win} {
		if err := d.store.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	var data int
	mac.node.OnData = func() { data++ }

	// Each lets in the tailnet's devices once it scanned it.
	win.node.scan(context.Background())
	mac.node.scan(context.Background())
	st := mac.node.Status()
	if !st.Available || len(st.Peers) != 1 || st.Peers[0].State != PeerApp || st.Peers[0].DeviceID != winID || st.Peers[0].Pulled != 2 || st.Peers[0].Live.DownRate != 42 {
		t.Fatalf("status %+v", st)
	}
	if data != 1 {
		t.Fatalf("OnData called %d times", data)
	}
	q := store.Query{From: h * 1000, To: (h + 3600) * 1000}
	if r := mac.store.Query(q); r.Down != 140 || len(r.Devices) != 2 {
		t.Fatalf("mac sees %d over %d devices", r.Down, len(r.Devices))
	}

	// Nothing changed: nothing is pulled again.
	mac.node.scan(context.Background())
	if st = mac.node.Status(); st.Peers[0].Pulled != 0 {
		t.Fatalf("pulled again: %+v", st.Peers[0])
	}
	// A change on Windows reaches the Mac at the next scan.
	win.store.Add(store.Key{Hour: h, App: "Edge", Host: "bing.com", Chain: "DIRECT"}, "", store.Counter{Down: 60})
	time.Sleep(5 * time.Millisecond)
	if err := win.store.Flush(); err != nil {
		t.Fatal(err)
	}
	mac.node.scan(context.Background())
	if r := mac.store.Query(q); r.Down != 200 {
		t.Fatalf("after the change: %d", r.Down)
	}
}

func TestStrangersAreRefused(t *testing.T) {
	mac := newDevice(t, macID, "100.64.0.1")
	// No scan yet: nobody is allowed.
	resp, err := http.Get(mac.srv.URL + PathHello)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", resp.StatusCode)
	}
	tailnetOf(mac)
	mac.node.scan(context.Background())
	for _, path := range []string{PathFile + "../../etc/passwd", PathFile + ".tmp", PathFile + "settings.json"} {
		resp, err := http.Get(mac.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("%s served", path)
		}
	}
	resp, err = http.Get(mac.srv.URL + PathFile + store.DeviceFile)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("own device file: %d", resp.StatusCode)
	}
}

func TestDisabled(t *testing.T) {
	mac := newDevice(t, macID, "100.64.0.1")
	tailnetOf(mac)
	mac.node.Enabled = func() bool { return false }
	mac.node.scan(context.Background())
	if st := mac.node.Status(); st.Enabled || st.Available || st.Serving {
		t.Fatalf("%+v", st)
	}
}

func TestPeersCalledOfflineAreTried(t *testing.T) {
	h := time.Now().Unix() - time.Now().Unix()%3600
	mac := newDevice(t, macID, "100.64.0.1")
	win := newDevice(t, winID, "100.64.0.2")
	tailnetWith(true, mac, win)
	win.store.Add(store.Key{Hour: h, App: "Edge", Host: "bing.com", Chain: "DIRECT"}, "", store.Counter{Down: 40})
	if err := win.store.Flush(); err != nil {
		t.Fatal(err)
	}
	win.node.scan(context.Background())
	mac.node.scan(context.Background())
	if st := mac.node.Status(); len(st.Peers) != 1 || st.Peers[0].State != PeerApp || st.Peers[0].Pulled != 2 {
		t.Fatalf("%+v", st.Peers)
	}
}

// Devices shared into the tailnet are neither served nor pulled from, and
// a device id differing only in case cannot write to this device's
// directory (review #3, #4).
func TestTrust(t *testing.T) {
	mac := newDevice(t, macID, "100.64.0.1")
	// A server shared in from another tailnet runs the app.
	shared := newDevice(t, "00000000c0c0", "100.64.0.3")
	extra = []tailnet.Node{{HostName: "shared", DNSName: "server.yak-bebop.ts.net", OS: "linux", Online: true, IPs: []netip.Addr{shared.ip}}}
	defer func() { extra = nil }()
	tailnetOf(mac)
	mac.node.PeerURL = func(netip.Addr) string { return shared.srv.URL }
	shared.node.allowed[netip.MustParseAddr("127.0.0.1")] = true
	mac.node.scan(context.Background())
	if st := mac.node.Status(); len(st.Peers) != 0 {
		t.Fatalf("a shared-in device was visited: %+v", st.Peers)
	}

	// A peer of the tailnet claiming this device's id, in other case.
	h := time.Now().Unix() - time.Now().Unix()%3600
	mac.store.Add(store.Key{Hour: h, App: "mine"}, "", store.Counter{Down: 1})
	mac.store.Flush()
	before, _ := os.ReadFile(filepath.Join(mac.store.Root(), macID, store.DayOf(time.Unix(h, 0))+store.DayExt))
	evil := newDevice(t, strings.ToUpper(macID), "100.64.0.4")
	evil.store.Add(store.Key{Hour: h, App: "forged"}, "", store.Counter{Down: 999})
	evil.store.Flush()
	extra = nil
	tailnetOf(mac, evil)
	evil.node.scan(context.Background())
	mac.node.scan(context.Background())
	after, _ := os.ReadFile(filepath.Join(mac.store.Root(), macID, store.DayOf(time.Unix(h, 0))+store.DayExt))
	if string(before) != string(after) {
		t.Fatal("a remote device wrote this device's data")
	}
	if st := mac.node.Status(); st.Peers[0].State != PeerNoApp {
		t.Fatalf("an invalid device id was accepted: %+v", st.Peers[0])
	}
}

// Downloads past the limit fail instead of keeping a cut copy (review #13).
func TestReadLimited(t *testing.T) {
	if _, err := store.ReadLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("cut copy accepted")
	}
	if b, err := store.ReadLimited(strings.NewReader("1234"), 4); err != nil || string(b) != "1234" {
		t.Fatalf("%q %v", b, err)
	}
}
