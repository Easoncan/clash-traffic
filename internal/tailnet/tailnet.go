// Package tailnet reads the Tailscale network this computer is in: its own
// address and the other devices, from the tailscale command line.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"clashtraffic/internal/i18n"
)

// Node is a device of the tailnet.
type Node struct {
	HostName string
	DNSName  string // e.g. mac-mini.tail1234.ts.net
	OS       string // "macOS", "windows", "linux", "android", "iOS"…
	IPs      []netip.Addr
	Online   bool
	// Sharee is a device of someone this tailnet shared a device with: it
	// is in the network map because it may connect to that device.
	Sharee bool
	// AltSharer is set on a device someone other than its owner shared.
	AltSharer bool
}

// IPv4 returns the node's IPv4 address in the tailnet.
func (n Node) IPv4() (netip.Addr, bool) {
	for _, ip := range n.IPs {
		if ip.Is4() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// Desktop reports whether the node runs a desktop system, where the app
// can run.
func (n Node) Desktop() bool {
	switch strings.ToLower(n.OS) {
	case "macos", "windows", "linux":
		return true
	}
	return false
}

// Status is the tailnet as this computer sees it.
type Status struct {
	State     string // "Running" when connected
	Tailnet   string
	DNSSuffix string // the tailnet's MagicDNS domain, e.g. tail1234.ts.net
	Self      Node
	Peers     []Node
}

// Own reports whether a peer is a device of this tailnet, rather than one
// shared into it from another tailnet or one of the people this tailnet
// shares with. Devices shared in keep the MagicDNS name of their own
// tailnet, so a peer is this tailnet's when its name is under this one's
// domain. Without the domain, no peer is.
func (s *Status) Own(n Node) bool {
	if s.DNSSuffix == "" || n.Sharee || n.AltSharer {
		return false
	}
	return strings.HasSuffix(strings.ToLower(n.DNSName), "."+strings.ToLower(s.DNSSuffix))
}

// Running reports whether Tailscale is connected.
func (s *Status) Running() bool { return s.State == "Running" }

type statusJSON struct {
	BackendState   string
	MagicDNSSuffix string
	CurrentTailnet *struct{ Name string }
	Self           *nodeJSON
	Peer           map[string]*nodeJSON
}

type nodeJSON struct {
	HostName        string
	DNSName         string
	OS              string
	TailscaleIPs    []string
	Online          bool
	ShareeNode      bool
	AltSharerUserID int64
}

func (n *nodeJSON) node() Node {
	out := Node{HostName: n.HostName, DNSName: strings.TrimSuffix(n.DNSName, "."), OS: n.OS, Online: n.Online,
		Sharee: n.ShareeNode, AltSharer: n.AltSharerUserID != 0}
	for _, s := range n.TailscaleIPs {
		if ip, err := netip.ParseAddr(s); err == nil {
			out.IPs = append(out.IPs, ip)
		}
	}
	return out
}

// Parse reads the output of tailscale status --json.
func Parse(data []byte) (*Status, error) {
	var j statusJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	suffix := strings.Trim(j.MagicDNSSuffix, ".")
	st := &Status{State: j.BackendState, Tailnet: suffix, DNSSuffix: suffix}
	if j.CurrentTailnet != nil && j.CurrentTailnet.Name != "" {
		st.Tailnet = j.CurrentTailnet.Name
	}
	if j.Self != nil {
		st.Self = j.Self.node()
		st.Self.Online = true
	}
	for _, p := range j.Peer {
		if p != nil {
			st.Peers = append(st.Peers, p.node())
		}
	}
	sort.Slice(st.Peers, func(i, k int) bool { return st.Peers[i].HostName < st.Peers[k].HostName })
	return st, nil
}

// Get runs tailscale status --json.
func Get(ctx context.Context) (*Status, error) {
	cli := cliPath()
	if cli == "" {
		return nil, errors.New(i18n.T("Tailscale was not found: install it and sign in first"))
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "status", "--json")
	hideWindow(cmd)
	out, err := cmd.Output()
	if len(out) > 0 {
		// It exits non-zero when stopped or logged out, but still reports.
		if st, perr := Parse(out); perr == nil {
			if !st.Running() {
				return st, i18n.Errorf("Tailscale is not connected (%s)", st.State)
			}
			return st, nil
		}
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("tailscale status: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("tailscale status: %w", err)
	}
	return nil, errors.New(i18n.T("tailscale status printed nothing"))
}

// cliPath finds the tailscale command. Apps started from the desktop do
// not get the shell's PATH, so the usual places are tried too.
func cliPath() string {
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		cands = []string{"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if dir := os.Getenv(env); dir != "" {
				cands = append(cands, filepath.Join(dir, "Tailscale", "tailscale.exe"))
			}
		}
	default:
		cands = []string{"/usr/bin/tailscale", "/usr/sbin/tailscale", "/usr/local/bin/tailscale", "/snap/bin/tailscale"}
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		cands = append([]string{p}, cands...)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}
