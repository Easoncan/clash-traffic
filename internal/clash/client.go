// Package clash talks to the external controller of a Clash / mihomo core,
// the one Clash Verge runs, over a Unix socket, a Windows named pipe or HTTP.
package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"clashtraffic/internal/i18n"
)

// Kind is how the controller is reached.
type Kind string

const (
	KindUnix Kind = "unix" // a Unix domain socket (macOS, Linux)
	KindPipe Kind = "pipe" // a named pipe (Windows)
	KindHTTP Kind = "http" // a TCP address, external-controller
)

// Endpoint locates an external controller.
type Endpoint struct {
	Kind    Kind   `json:"kind"`
	Address string `json:"address"` // socket path, pipe name or base URL
	Secret  string `json:"-"`
	Source  string `json:"source"` // where the endpoint was found: "default", "the running core", "set by hand" (in English, which the page translates) or a config file
}

func (e Endpoint) String() string {
	switch e.Kind {
	case KindUnix:
		return "unix://" + e.Address
	case KindPipe:
		return "pipe:" + e.Address
	}
	return e.Address
}

// ParseEndpoint reads an address typed by the user: unix:///path,
// \\.\pipe\name, host:port or http(s)://host:port.
func ParseEndpoint(s, secret string) (Endpoint, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Endpoint{}, errors.New(i18n.T("The address is empty"))
	case strings.HasPrefix(s, "unix://"):
		return Endpoint{Kind: KindUnix, Address: strings.TrimPrefix(s, "unix://"), Secret: secret, Source: "set by hand"}, nil
	case strings.HasPrefix(s, `\\.\pipe\`), strings.HasPrefix(s, "pipe:"):
		return Endpoint{Kind: KindPipe, Address: strings.TrimPrefix(s, "pipe:"), Secret: secret, Source: "set by hand"}, nil
	case strings.HasPrefix(s, "/"):
		return Endpoint{Kind: KindUnix, Address: s, Secret: secret, Source: "set by hand"}, nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Endpoint{}, i18n.Errorf("cannot read the address %q", s)
	}
	return Endpoint{Kind: KindHTTP, Address: strings.TrimRight(u.String(), "/"), Secret: secret, Source: "set by hand"}, nil
}

// Metadata describes the two ends of a connection.
type Metadata struct {
	Network           string `json:"network"`
	Type              string `json:"type"`
	SourceIP          string `json:"sourceIP"`
	SourcePort        string `json:"sourcePort"`
	DestinationIP     string `json:"destinationIP"`
	DestinationPort   string `json:"destinationPort"`
	Host              string `json:"host"`
	SniffHost         string `json:"sniffHost"`
	Process           string `json:"process"`
	ProcessPath       string `json:"processPath"`
	InboundName       string `json:"inboundName"`
	RemoteDestination string `json:"remoteDestination"`
}

// Conn is an open connection with the bytes it carried so far.
type Conn struct {
	ID       string    `json:"id"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	// ProviderChains names the proxy provider of each of Chains, "" for
	// proxies of the configuration itself.
	ProviderChains []string `json:"providerChains"`
	Rule           string   `json:"rule"`
	RulePayload    string   `json:"rulePayload"`
	Metadata       Metadata `json:"metadata"`
}

// Snapshot is the answer of GET /connections.
type Snapshot struct {
	UploadTotal   int64  `json:"uploadTotal"`
	DownloadTotal int64  `json:"downloadTotal"`
	Connections   []Conn `json:"connections"`
	Memory        int64  `json:"memory"`
}

// Client calls one controller.
type Client struct {
	ep   Endpoint
	base string
	hc   *http.Client
}

// NewClient returns a client of ep.
func NewClient(ep Endpoint) *Client {
	tr := &http.Transport{
		MaxIdleConns:        2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	base := ep.Address
	switch ep.Kind {
	case KindUnix:
		path := ep.Address
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
		base = "http://localhost"
	case KindPipe:
		name := ep.Address
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialPipe(ctx, name)
		}
		base = "http://localhost"
	}
	return &Client{ep: ep, base: base, hc: &http.Client{Transport: tr, Timeout: 10 * time.Second}}
}

// Endpoint returns the controller the client calls.
func (c *Client) Endpoint() Endpoint { return c.ep }

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if c.ep.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.ep.Secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New(i18n.T("Wrong secret"))
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version returns the core's version, e.g. "v1.19.32".
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", nil, &v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", errors.New(i18n.T("Not a Clash controller"))
	}
	return v.Version, nil
}

// Connections returns the open connections and the core's totals.
func (c *Client) Connections(ctx context.Context) (*Snapshot, error) {
	var s Snapshot
	if err := c.do(ctx, http.MethodGet, "/connections", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// FindProcessMode returns find-process-mode of the running configuration:
// "always", "strict" or "off".
func (c *Client) FindProcessMode(ctx context.Context) (string, error) {
	var cfg struct {
		FindProcessMode string `json:"find-process-mode"`
	}
	if err := c.do(ctx, http.MethodGet, "/configs", nil, &cfg); err != nil {
		return "", err
	}
	return cfg.FindProcessMode, nil
}

// SetFindProcessMode changes find-process-mode of the running core. Clash
// Verge restores its own value when it reloads the configuration.
func (c *Client) SetFindProcessMode(ctx context.Context, mode string) error {
	return c.do(ctx, http.MethodPatch, "/configs", map[string]string{"find-process-mode": mode}, nil)
}

// ProxyURL returns the core's HTTP proxy, mixed-port or else port, as
// this computer reaches it: nil when the core has neither, runs on another
// computer, which its controller's address tells, or wants a password from
// this computer, which the controller does not tell.
func (c *Client) ProxyURL(ctx context.Context) (*url.URL, error) {
	if c.ep.Kind == KindHTTP {
		u, err := url.Parse(c.ep.Address)
		if err != nil {
			return nil, err
		}
		if ip, err := netip.ParseAddr(u.Hostname()); u.Hostname() != "localhost" && (err != nil || !ip.IsLoopback()) {
			return nil, nil
		}
	}
	var cfg struct {
		Port        int    `json:"port"`
		MixedPort   int    `json:"mixed-port"`
		AllowLAN    bool   `json:"allow-lan"`
		BindAddress string `json:"bind-address"`
		// The users of the proxies, without their passwords, and the
		// addresses let in without one.
		Authentication   []string `json:"authentication"`
		SkipAuthPrefixes []string `json:"skip-auth-prefixes"`
	}
	if err := c.do(ctx, http.MethodGet, "/configs", nil, &cfg); err != nil {
		return nil, err
	}
	port := cfg.MixedPort
	if port <= 0 {
		port = cfg.Port
	}
	if port <= 0 {
		return nil, nil
	}
	// The proxy listens on the loopback address, or once it is open to the
	// LAN on bind-address: "*", or an address, IPv6 ones in brackets.
	host := netip.MustParseAddr("127.0.0.1")
	if cfg.AllowLAN {
		bind := strings.TrimSuffix(strings.TrimPrefix(cfg.BindAddress, "["), "]")
		if ip, err := netip.ParseAddr(bind); err == nil && !ip.IsUnspecified() && ip.Zone() == "" {
			host = ip.Unmap()
		}
	}
	addr := net.JoinHostPort(host.String(), strconv.Itoa(port))
	// The core lets in without a password the addresses connections come
	// from, as the system picks them.
	if len(cfg.Authentication) > 0 && !inPrefixes(sourceAddr(addr), cfg.SkipAuthPrefixes) {
		return nil, nil
	}
	return &url.URL{Scheme: "http", Host: addr}, nil
}

// sourceAddr returns the address the system sends from to addr: a UDP
// socket picks it when it connects, which sends nothing.
func sourceAddr(addr string) netip.Addr {
	c, err := net.Dial("udp", addr)
	if err != nil {
		return netip.Addr{}
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
}

func inPrefixes(ip netip.Addr, prefixes []string) bool {
	for _, s := range prefixes {
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// CloseConnection closes one connection.
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/connections/"+url.PathEscape(id), nil, nil)
}
