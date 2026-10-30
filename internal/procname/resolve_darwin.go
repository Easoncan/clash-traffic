package procname

import (
	"context"
	"net"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"clashtraffic/internal/clash"
)

type socketOwner struct {
	pid            string
	network        string
	source, target netip.AddrPort
}

type processInfo struct{ name, path string }

type socketIdentity struct {
	start          time.Time
	network        string
	source, target netip.AddrPort
}

type resolution struct {
	identity socketIdentity
	processInfo
	retry time.Time
}

// Resolver supplements missing core metadata with the owner of a local OS
// socket. It runs on the poll goroutine and never changes the core or routes.
type Resolver struct {
	cache map[string]resolution
	scan  func(context.Context) ([]socketOwner, error)
	paths func(context.Context, []string) map[string]processInfo
}

func (r *Resolver) Resolve(ctx context.Context, conns []clash.Conn) {
	if r.cache == nil {
		r.cache = map[string]resolution{}
	}
	alive := make(map[string]bool, len(conns))
	var pending []*clash.Conn
	now := time.Now()
	for i := range conns {
		c := &conns[i]
		alive[c.ID] = true
		m := &c.Metadata
		if c.ID == "" || m.Type == "Inner" || m.Process != "" || m.ProcessPath != "" {
			continue
		}
		id := identity(c)
		if !id.source.IsValid() {
			continue
		}
		if cached, ok := r.cache[c.ID]; ok && cached.identity == id {
			if cached.name != "" || cached.path != "" {
				m.Process, m.ProcessPath = cached.name, cached.path
				continue
			}
			if now.Before(cached.retry) {
				continue
			}
		}
		pending = append(pending, c)
	}
	for id := range r.cache {
		if !alive[id] {
			delete(r.cache, id)
		}
	}
	if len(pending) == 0 {
		return
	}
	// A slow or unavailable system lookup must not hold up counting.
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	scan, paths := r.scan, r.paths
	if scan == nil {
		scan = scanSockets
	}
	if paths == nil {
		paths = processPaths
	}
	sockets, err := scan(ctx)
	if err != nil {
		return
	}
	locals := localAddresses()
	matched := map[*clash.Conn]string{}
	pids := map[string]bool{}
	for _, c := range pending {
		id := identity(c)
		r.cache[c.ID] = resolution{identity: id, retry: now.Add(5 * time.Second)}
		// LAN clients belong to their own computer, including UDP clients
		// whose source port happens to equal a wildcard socket's here.
		if !id.source.Addr().IsLoopback() && !locals[id.source.Addr()] {
			continue
		}
		if pid := matchSocket(id, sockets); pid != "" {
			matched[c], pids[pid] = pid, true
		}
	}
	list := make([]string, 0, len(pids))
	for pid := range pids {
		list = append(list, pid)
	}
	if len(list) == 0 {
		return
	}
	info := paths(ctx, list)
	for c, pid := range matched {
		p := info[pid]
		if p.name == "" && p.path == "" {
			continue
		}
		c.Metadata.Process, c.Metadata.ProcessPath = p.name, p.path
		r.cache[c.ID] = resolution{identity: identity(c), processInfo: p}
	}
}

func identity(c *clash.Conn) socketIdentity {
	m := c.Metadata
	id := socketIdentity{start: c.Start, network: strings.ToLower(m.Network)}
	if id.network != "tcp" && id.network != "udp" {
		return id
	}
	id.source = addrPort(m.SourceIP, m.SourcePort)
	// For TUN, inboundIP is the original destination (often a fake IP).
	// For HTTP/SOCKS, it is the proxy listener the application connected to.
	id.target = addrPort(m.InboundIP, m.InboundPort)
	if !id.target.IsValid() {
		id.target = addrPort(m.DestinationIP, m.DestinationPort)
	}
	return id
}

func addrPort(host, port string) netip.AddrPort {
	ip, err := netip.ParseAddr(host)
	p, perr := strconv.ParseUint(port, 10, 16)
	if err != nil || perr != nil || p == 0 {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(p))
}

func matchSocket(id socketIdentity, sockets []socketOwner) string {
	pid := ""
	for _, s := range sockets {
		if s.network != id.network || s.source.Port() != id.source.Port() {
			continue
		}
		if s.source.Addr() != id.source.Addr() && !(s.source.Addr().IsUnspecified() && s.source.Addr().Is4() == id.source.Addr().Is4()) {
			continue
		}
		if s.target.IsValid() {
			if s.target != id.target {
				continue
			}
		} else if id.network != "udp" {
			continue // TCP listeners are not application connections.
		}
		if pid != "" && pid != s.pid {
			return "" // Shared/reused sockets have no unique owner.
		}
		pid = s.pid
	}
	return pid
}

func localAddresses() map[netip.Addr]bool {
	locals := map[netip.Addr]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			locals[p.Addr().Unmap()] = true
		}
	}
	return locals
}

func scanSockets(ctx context.Context) ([]socketOwner, error) {
	data, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP", "-iUDP", "-FpfPtn").Output()
	if err != nil {
		return nil, err
	}
	return parseSockets(string(data)), nil
}

func parseSockets(data string) []socketOwner {
	var out []socketOwner
	pid, network, family := "", "", ""
	for _, line := range strings.Split(data, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, network, family = line[1:], "", ""
		case 'f':
			network, family = "", ""
		case 'P':
			network = strings.ToLower(line[1:])
		case 't':
			family = line[1:]
		case 'n':
			if n, err := strconv.Atoi(pid); err != nil || n <= 0 || (network != "tcp" && network != "udp") {
				continue
			}
			local, remote, connected := strings.Cut(line[1:], "->")
			if !connected && network != "udp" {
				continue
			}
			if strings.HasPrefix(local, "*:") {
				if family == "IPv4" {
					local = "0.0.0.0:" + local[2:]
				} else if family == "IPv6" {
					local = "[::]:" + local[2:]
				}
			}
			source, err := netip.ParseAddrPort(local)
			if err != nil {
				continue
			}
			source = netip.AddrPortFrom(source.Addr().Unmap(), source.Port())
			target := netip.AddrPort{}
			if connected {
				remote = strings.TrimSuffix(remote, " (ESTABLISHED)")
				target, err = netip.ParseAddrPort(remote)
				if err != nil {
					continue
				}
				target = netip.AddrPortFrom(target.Addr().Unmap(), target.Port())
			}
			out = append(out, socketOwner{pid: pid, network: network, source: source, target: target})
		}
	}
	return out
}

func processPaths(ctx context.Context, pids []string) map[string]processInfo {
	sort.Strings(pids)
	data, err := exec.CommandContext(ctx, "/bin/ps", "-ww", "-p", strings.Join(pids, ","), "-o", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	out := map[string]processInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		pid, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		path = strings.TrimSpace(path)
		if ok && strings.HasPrefix(path, "/") {
			out[pid] = processInfo{name: base(path), path: path}
		}
	}
	return out
}
