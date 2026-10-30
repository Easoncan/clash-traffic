package procname

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"clashtraffic/internal/clash"
)

func TestSocketOwnerMatching(t *testing.T) {
	const data = `p30239
f55
tIPv4
PTCP
n198.18.0.1:62340->198.18.0.90:443
f56
tIPv4
PTCP
n127.0.0.1:62311
f57
tIPv6
PTCP
n[::1]:62341->[::1]:7897
p20769
f34
tIPv4
PUDP
n*:60182
f35
tIPv6
PUDP
n*:60183
f36
tIPv4
PUDP
n127.0.0.1:60184->8.134.98.121:21116
f37
n127.0.0.1:60185->8.134.98.121:21116
`
	sockets := parseSockets(data)
	if len(sockets) != 5 {
		t.Fatalf("parsed %d sockets: %+v", len(sockets), sockets)
	}
	for _, tc := range []struct{ name, network, source, target, want string }{
		{"TUN original fake IP", "tcp", "198.18.0.1:62340", "198.18.0.90:443", "30239"},
		{"same port different destination", "tcp", "198.18.0.1:62340", "183.232.189.188:443", ""},
		{"different source", "tcp", "127.0.0.1:62340", "198.18.0.90:443", ""},
		{"listener is not a client", "tcp", "127.0.0.1:62311", "198.18.0.90:443", ""},
		{"IPv6 HTTP proxy", "tcp", "[::1]:62341", "[::1]:7897", "30239"},
		{"UDP wildcard", "udp", "198.18.0.1:60182", "8.134.98.121:21116", "20769"},
		{"UDP IPv6 wildcard", "udp", "[::1]:60183", "[::1]:21116", "20769"},
		{"wrong wildcard family", "udp", "127.0.0.1:60183", "8.134.98.121:21116", ""},
		{"UDP connected", "udp", "127.0.0.1:60184", "8.134.98.121:21116", "20769"},
		{"UDP wrong remote", "udp", "127.0.0.1:60184", "8.134.98.122:21116", ""},
		{"protocol not inherited from previous file", "udp", "127.0.0.1:60185", "8.134.98.121:21116", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := socketIdentity{network: tc.network, source: netip.MustParseAddrPort(tc.source), target: netip.MustParseAddrPort(tc.target)}
			if got := matchSocket(id, sockets); got != tc.want {
				t.Fatalf("owner %q, want %q", got, tc.want)
			}
		})
	}
	id := socketIdentity{network: "tcp", source: sockets[0].source, target: sockets[0].target}
	duplicate := sockets[0]
	duplicate.pid = "999"
	if got := matchSocket(id, append(sockets, duplicate)); got != "" {
		t.Fatalf("ambiguous socket assigned to PID %s", got)
	}
}

func TestResolverConservativeAndCached(t *testing.T) {
	makeConn := func(id, port string) clash.Conn {
		return clash.Conn{ID: id, Start: time.Unix(1000, 0), Metadata: clash.Metadata{
			Type: "Tun", Network: "tcp", SourceIP: "127.0.0.1", SourcePort: port,
			InboundIP: "198.18.0.90", InboundPort: "443", DestinationIP: "183.232.189.188", DestinationPort: "443",
		}}
	}
	workbuddy := processInfo{name: "Electron", path: "/Applications/WorkBuddy.app/Contents/MacOS/Electron"}
	scans := 0
	r := Resolver{
		scan: func(context.Context) ([]socketOwner, error) {
			scans++
			return []socketOwner{
				{pid: "30239", network: "tcp", source: netip.MustParseAddrPort("127.0.0.1:62340"), target: netip.MustParseAddrPort("198.18.0.90:443")},
				{pid: "20769", network: "udp", source: netip.MustParseAddrPort("0.0.0.0:60182")},
			}, nil
		},
		paths: func(_ context.Context, _ []string) map[string]processInfo {
			return map[string]processInfo{"30239": workbuddy, "20769": {name: "RustDesk", path: "/Applications/RustDesk.app/Contents/MacOS/RustDesk"}}
		},
	}
	conns := []clash.Conn{makeConn("unknown", "62340"), makeConn("known", "62340"), makeConn("internal", "62340"), makeConn("remote", "60182")}
	conns[1].Metadata.Process = "Already known"
	conns[2].Metadata.Type = "Inner"
	conns[3].Metadata.Network, conns[3].Metadata.SourceIP = "udp", "203.0.113.2"
	r.Resolve(t.Context(), conns)
	if conns[0].Metadata.ProcessPath != workbuddy.path || App(conns[0].Metadata.Process, conns[0].Metadata.ProcessPath) != "WorkBuddy" {
		t.Fatalf("not resolved from original socket destination: %+v", conns[0])
	}
	if conns[1].Metadata.Process != "Already known" || conns[2].Metadata.Process != "" || conns[3].Metadata.Process != "" {
		t.Fatalf("overwrote core metadata, an internal hop, or a LAN client: %+v", conns)
	}
	fresh := []clash.Conn{makeConn("unknown", "62340"), makeConn("missing", "62342")}
	r.Resolve(t.Context(), fresh)
	if fresh[0].Metadata.ProcessPath != workbuddy.path || scans != 2 {
		t.Fatalf("positive cache or new lookup failed: %+v, scans=%d", fresh, scans)
	}
	fresh[0].Metadata.Process, fresh[0].Metadata.ProcessPath = "", ""
	r.Resolve(t.Context(), fresh)
	if scans != 2 || fresh[0].Metadata.ProcessPath != workbuddy.path {
		t.Fatalf("repeated scans for known/missing connections: %d", scans)
	}
	// Reused IDs with a changed socket must not inherit the old owner.
	changed := []clash.Conn{makeConn("unknown", "62343")}
	r.Resolve(t.Context(), changed)
	if changed[0].Metadata.Process != "" || scans != 3 {
		t.Fatalf("stale identity reused: %+v, scans=%d", changed, scans)
	}
	r.Resolve(t.Context(), nil)
	if len(r.cache) != 0 {
		t.Fatalf("closed connections retained: %+v", r.cache)
	}
	r.scan = func(context.Context) ([]socketOwner, error) { return nil, errors.New("permission denied") }
	failed := []clash.Conn{makeConn("failed", "62340")}
	r.Resolve(t.Context(), failed)
	if failed[0].Metadata.Process != "" {
		t.Fatal("failed OS lookup invented an owner")
	}
}

// Uses only sockets created by this test; no Clash/network configuration
// is read or changed. Opt in to avoid relying on lsof availability in CI.
func TestResolverLiveLocalSockets(t *testing.T) {
	if os.Getenv("CLASH_PROCESS_LIVE") == "" {
		t.Skip("set CLASH_PROCESS_LIVE=1 to verify macOS socket ownership")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	tcpSource := client.LocalAddr().(*net.TCPAddr).AddrPort()
	tcpTarget := server.LocalAddr().(*net.TCPAddr).AddrPort()
	udpSource := udp.LocalAddr().(*net.UDPAddr).AddrPort()
	conns := []clash.Conn{
		{ID: "tcp-test", Metadata: clash.Metadata{Type: "Tun", Network: "tcp", SourceIP: tcpSource.Addr().String(), SourcePort: strconv.Itoa(int(tcpSource.Port())), InboundIP: tcpTarget.Addr().String(), InboundPort: strconv.Itoa(int(tcpTarget.Port()))}},
		{ID: "udp-test", Metadata: clash.Metadata{Type: "Tun", Network: "udp", SourceIP: udpSource.Addr().String(), SourcePort: strconv.Itoa(int(udpSource.Port())), DestinationIP: "203.0.113.1", DestinationPort: "21116"}},
	}
	r := &Resolver{}
	r.Resolve(t.Context(), conns)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	for _, c := range conns {
		got, _ := filepath.EvalSymlinks(c.Metadata.ProcessPath)
		if got != exe {
			t.Fatalf("%s: owner %q, want this test executable %q", c.ID, c.Metadata.ProcessPath, exe)
		}
		t.Logf("%s matched this test's process", c.ID)
	}
}
