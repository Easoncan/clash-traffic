package tailnet

import (
	"context"
	"os"
	"testing"
)

const sample = `{
  "BackendState": "Running",
  "MagicDNSSuffix": "tail1234.ts.net",
  "CurrentTailnet": {"Name": "ada@example.com"},
  "Self": {"HostName": "mac", "DNSName": "mac.tail1234.ts.net.", "OS": "macOS", "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"]},
  "Peer": {
    "nodekey:a": {"HostName": "win", "DNSName": "win.tail1234.ts.net.", "OS": "windows", "TailscaleIPs": ["100.64.0.2"], "Online": true},
    "nodekey:b": {"HostName": "phone", "DNSName": "phone.tail1234.ts.net.", "OS": "android", "TailscaleIPs": ["100.64.0.3"], "Online": true},
    "nodekey:c": {"HostName": "friend", "DNSName": "friend.other.ts.net.", "OS": "linux", "TailscaleIPs": ["100.64.0.4"], "Online": true, "ShareeNode": true},
    "nodekey:d": {"HostName": "shared-in", "DNSName": "server.yak-bebop.ts.net.", "OS": "linux", "TailscaleIPs": ["100.64.0.5"], "Online": true},
    "nodekey:e": {"HostName": "evil", "DNSName": "evil.tail1234.ts.net.evil.com.", "OS": "linux", "TailscaleIPs": ["100.64.0.6"], "Online": true}
  }
}`

func TestParse(t *testing.T) {
	st, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	ip, ok := st.Self.IPv4()
	if !st.Running() || st.Tailnet != "ada@example.com" || !ok || ip.String() != "100.64.0.1" || st.Self.DNSName != "mac.tail1234.ts.net" {
		t.Fatalf("%+v", st)
	}
	own := map[string]bool{}
	for _, p := range st.Peers {
		own[p.HostName] = st.Own(p)
	}
	// Only this tailnet's devices: not the device shared in, not the
	// sharee's, not a name that merely contains the domain.
	want := map[string]bool{"win": true, "phone": true, "friend": false, "shared-in": false, "evil": false}
	for h, w := range want {
		if own[h] != w {
			t.Errorf("Own(%s) = %v", h, own[h])
		}
	}
	if st.DNSSuffix != "tail1234.ts.net" {
		t.Fatalf("suffix %q", st.DNSSuffix)
	}
	if (&Status{}).Own(st.Peers[0]) {
		t.Fatal("trusted a peer without knowing the tailnet's domain")
	}
}

func TestGetLive(t *testing.T) {
	if os.Getenv("TAILSCALE_LIVE") == "" {
		t.Skip("set TAILSCALE_LIVE=1 to read this computer's tailnet")
	}
	st, err := Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ip, _ := st.Self.IPv4()
	t.Logf("self %s %s, %d peers", st.Self.HostName, ip, len(st.Peers))
	for _, p := range st.Peers {
		t.Logf("  %s %s online=%v desktop=%v", p.HostName, p.OS, p.Online, p.Desktop())
	}
}
