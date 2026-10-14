package clash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestProxyURL(t *testing.T) {
	for _, tc := range []struct {
		name, configs, want string
	}{
		{"mixed port", `{"port":0,"mixed-port":7890,"allow-lan":false,"bind-address":"*"}`, "http://127.0.0.1:7890"},
		{"http port", `{"port":7891,"mixed-port":0}`, "http://127.0.0.1:7891"},
		{"mixed port first", `{"port":7891,"mixed-port":7890}`, "http://127.0.0.1:7890"},
		{"open to the LAN", `{"mixed-port":7890,"allow-lan":true,"bind-address":"*"}`, "http://127.0.0.1:7890"},
		{"bound to an address", `{"mixed-port":7890,"allow-lan":true,"bind-address":"192.168.1.5"}`, "http://192.168.1.5:7890"},
		{"bound but local only", `{"mixed-port":7890,"allow-lan":false,"bind-address":"192.168.1.5"}`, "http://127.0.0.1:7890"},
		{"no proxy port", `{"port":0,"socks-port":7891,"mixed-port":0}`, ""},
		{"bound to an IPv6 address", `{"mixed-port":7890,"allow-lan":true,"bind-address":"[fd00::5]"}`, "http://[fd00::5]:7890"},
		{"bound to IPv6 loopback", `{"mixed-port":7890,"allow-lan":true,"bind-address":"[::1]"}`, "http://[::1]:7890"},
		{"bound to every address", `{"mixed-port":7890,"allow-lan":true,"bind-address":"0.0.0.0"}`, "http://127.0.0.1:7890"},
		{"bound to every IPv6 address", `{"mixed-port":7890,"allow-lan":true,"bind-address":"[::]"}`, "http://127.0.0.1:7890"},
		{"no authentication", `{"mixed-port":7890,"authentication":null,"skip-auth-prefixes":null}`, "http://127.0.0.1:7890"},
		{"authentication", `{"mixed-port":7890,"authentication":["me"],"skip-auth-prefixes":null}`, ""},
		{"authentication but not here", `{"mixed-port":7890,"authentication":["me"],"skip-auth-prefixes":["127.0.0.1/8","::1/128"]}`, "http://127.0.0.1:7890"},
		{"authentication on the LAN address", `{"mixed-port":7890,"allow-lan":true,"bind-address":"192.168.1.5","authentication":["me"],"skip-auth-prefixes":["127.0.0.1/8"]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/configs" {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(tc.configs))
			}))
			defer srv.Close()
			u, err := NewClient(Endpoint{Kind: KindHTTP, Address: srv.URL}).ProxyURL(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if u != nil {
				got = u.String()
			}
			if got != tc.want {
				t.Errorf("ProxyURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// The core checks where connections come from, which differs from where
// they go for another loopback address on Linux (review 8).
func TestSourceAddr(t *testing.T) {
	if got := sourceAddr("127.0.0.1:7890"); got != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("sourceAddr(127.0.0.1) = %v", got)
	}
	src := sourceAddr("127.0.0.2:7890")
	if !src.IsValid() {
		t.Skip("127.0.0.2 is not reachable here")
	}
	t.Logf("127.0.0.2 is reached from %v", src)
	for _, tc := range []struct {
		skip string
		want bool
	}{
		{src.String() + "/32", true},
		{"10.0.0.0/8", false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"mixed-port":7890,"allow-lan":true,"bind-address":"127.0.0.2","authentication":["me"],"skip-auth-prefixes":["` + tc.skip + `"]}`))
		}))
		u, err := NewClient(Endpoint{Kind: KindHTTP, Address: srv.URL}).ProxyURL(context.Background())
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if (u != nil) != tc.want {
			t.Errorf("skip-auth-prefixes %s: ProxyURL = %v, want a proxy: %v", tc.skip, u, tc.want)
		}
	}
}

// A core on another computer has its proxy there: the client asks nothing.
func TestProxyURLRemoteCore(t *testing.T) {
	for _, addr := range []string{"http://192.168.1.2:9090", "http://nas.local:9090"} {
		u, err := NewClient(Endpoint{Kind: KindHTTP, Address: addr}).ProxyURL(context.Background())
		if u != nil || err != nil {
			t.Errorf("%s: ProxyURL = %v, %v; want nil, nil", addr, u, err)
		}
	}
	u, err := NewClient(Endpoint{Kind: KindHTTP, Address: "http://localhost:1"}).ProxyURL(context.Background())
	if u != nil || err == nil {
		t.Errorf("localhost: ProxyURL = %v, %v; want it to ask the core", u, err)
	}
}
