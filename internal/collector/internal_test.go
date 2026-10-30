package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/sessions"
	"clashtraffic/internal/store"
)

// Pre-fix checkpoints may carry debt from counting Inner traffic twice,
// or pending balances whose closed candidates contain those Inner hops.
func TestLegacyCheckpointDoesNotCarryInternalAccounting(t *testing.T) {
	for _, balance := range []int64{-999, 999} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			s := &sink{}
			old := saved{Saved: t0.UnixMilli(), Gen: 1, Started: true, Endpoint: "test-core", CoreUp: 1000, CoreDown: 2000,
				BalUp: balance, BalDown: balance, PrevBalUp: balance, PrevBalDown: balance,
				Conns:      map[string]savedConn{"old": {Up: 500, Down: 1000}},
				LastClosed: []savedClosed{{App: AppUnknown, LastUp: 999, LastDown: 999}}}
			data, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "old-checkpoint.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			c := New(s)
			if err := c.Load(path, 1); err != nil {
				t.Fatal(err)
			}
			c.Connected("test-core")
			active := conn("old", "browser", 500, 1000)
			snap := &clash.Snapshot{UploadTotal: 1100, DownloadTotal: 2200, Connections: []clash.Conn{active}}
			c.Tick(t0.Add(time.Second), snap)
			c.Tick(t0.Add(2*time.Second), snap)
			if s.total != (store.Counter{Up: 100, Down: 200}) || s.byApp[AppUnknown] != (store.Counter{}) || s.byApp[AppShortConn].Down != 200 {
				t.Fatalf("legacy accounting changed new traffic: total=%+v apps=%+v", s.total, s.byApp)
			}
		})
	}
}

// A dialer-proxy exposes both the application's connection and an Inner
// connection to the next proxy. Only the former contributes to core totals.
func TestInternalProxyTrafficIsNotCountedTwice(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			s := &sink{}
			var closed []sessions.Session
			newCollector := func() *Collector {
				c := New(s)
				c.CurrentSub = func() string { return "subscription" }
				c.OnClose = func(s sessions.Session) { closed = append(closed, s) }
				return c
			}
			c := newCollector()
			c.Connected("test-core")
			app := conn("app", "agy", 100, 1000)
			app.Metadata.Type, app.Metadata.Network, app.Metadata.InboundName = "Socks5", network, "us-port"
			app.Chains = []string{"Proxy-Cheap", "US-Group"}
			codex := conn("codex", "codex", 50, 500)
			codex.Metadata.Type, codex.Metadata.Network, codex.Metadata.InboundName = "HTTP", network, "common-port"
			codex.Chains = []string{"Proxy-Cheap", "GPT-Group"}
			// Unknown user traffic must still count, even with the same target
			// as the intermediate connection and no process information.
			unknown := conn("unknown", "", 7, 70)
			unknown.Metadata.Type, unknown.Metadata.Network = "Tun", network
			unknown.Metadata.Host = "48.45.238.122"
			direct := conn("direct", "WeChat", 8, 80)
			direct.Chains = []string{"DIRECT", "国内流量"}
			inner := conn("inner", "", 180, 1800)
			inner.Metadata.Type, inner.Metadata.Network = "Inner", network
			inner.Metadata.Host = unknown.Metadata.Host
			inner.Chains = []string{"HK", "OneCat"}
			inner.ProviderChains = []string{"upstream-provider", ""}

			live := c.Tick(t0, &clash.Snapshot{UploadTotal: 165, DownloadTotal: 1650,
				Connections: []clash.Conn{inner, app, codex, unknown, direct}})
			want := store.Counter{Up: 165, Down: 1650, Conns: 4}
			if s.total != want {
				t.Fatalf("counted %+v, want %+v (without the intermediate hop)", s.total, want)
			}
			if live.Total != 4 || len(live.Conns) != 4 || len(c.OpenSessions()) != 4 {
				t.Fatalf("internal connection exposed: live=%+v sessions=%+v", live, c.OpenSessions())
			}
			if s.byApp[AppUnknown].Down != 70 || s.bySub["subscription"].Down != 1570 || s.bySub[""].Down != 80 || s.bySub["upstream-provider"].Down != 0 {
				t.Fatalf("apps=%+v subscriptions=%+v", s.byApp, s.bySub)
			}

			// A checkpoint and quick reconnect must keep excluding the hop.
			path := filepath.Join(t.TempDir(), "checkpoint.json")
			if err := c.Save(path, 1); err != nil {
				t.Fatal(err)
			}
			c = newCollector()
			if err := c.Load(path, 1); err != nil {
				t.Fatal(err)
			}
			c.Connected("test-core")
			app.Upload, app.Download = 150, 1500
			codex.Upload, codex.Download = 75, 750
			direct.Upload, direct.Download = 12, 120
			inner.Upload, inner.Download = 260, 2600
			live = c.Tick(t0.Add(time.Second), &clash.Snapshot{UploadTotal: 244, DownloadTotal: 2440,
				Connections: []clash.Conn{app, codex, inner, unknown, direct}})
			if s.total.Up != 244 || s.total.Down != 2440 || live.UpRate != 79 || live.DownRate != 790 {
				t.Fatalf("after reconnect: counted=%+v live=%+v", s.total, live)
			}

			// Closing everything must not create a session or residual traffic
			// for the intermediate hop. The real connection's unseen tail is
			// still reconciled against the core's totals.
			c.Tick(t0.Add(2*time.Second), &clash.Snapshot{UploadTotal: 254, DownloadTotal: 2540})
			c.Tick(t0.Add(3*time.Second), &clash.Snapshot{UploadTotal: 254, DownloadTotal: 2540})
			if s.total.Up != 254 || s.total.Down != 2540 || len(closed) != 4 {
				t.Fatalf("after close: counted=%+v sessions=%+v", s.total, closed)
			}
			for _, s := range closed {
				if s.ID == inner.ID {
					t.Fatal("internal hop written to access log")
				}
			}
		})
	}
}

func TestUnknownProcessCanBeResolvedLater(t *testing.T) {
	s := &sink{}
	c := New(s)
	x := conn("late-process", "", 100, 1000)
	x.Metadata.Type = "Tun"
	c.Tick(t0, &clash.Snapshot{UploadTotal: 100, DownloadTotal: 1000, Connections: []clash.Conn{x}})
	if s.byApp[AppUnknown].Down != 1000 {
		t.Fatal("missing process was guessed from its destination")
	}
	x.Metadata.Process = "Electron"
	x.Metadata.ProcessPath = "/Applications/WorkBuddy.app/Contents/MacOS/Electron"
	x.Upload, x.Download = 125, 1250
	live := c.Tick(t0.Add(time.Second), &clash.Snapshot{UploadTotal: 125, DownloadTotal: 1250, Connections: []clash.Conn{x}})
	if live.Conns[0].App != "WorkBuddy" || s.byApp["WorkBuddy"].Down != 250 || s.byApp[AppUnknown].Down != 1000 || s.total.Conns != 1 || s.total.Down != 1250 {
		t.Fatalf("late attribution changed totals or kept the unknown owner: live=%+v apps=%+v total=%+v", live, s.byApp, s.total)
	}
}
