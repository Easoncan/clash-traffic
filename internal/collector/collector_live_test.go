package collector

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/store"
)

// TestCollectLive sends three chunks through the running core to a loopback
// server. It starts counting after the first, like launching the app while
// Clash already has traffic, then reconnects before the last chunk.
// No controller settings are changed; counters are stored in t.TempDir.
func TestCollectLive(t *testing.T) {
	if os.Getenv("CLASH_LIVE_TRAFFIC") == "" {
		t.Skip("set CLASH_LIVE_TRAFFIC=1 to test counting through the running core's local HTTP proxy")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var ep clash.Endpoint
	var err error
	if address := os.Getenv("CLASH_LIVE_ADDRESS"); address != "" {
		ep, err = clash.ParseEndpoint(address, os.Getenv("CLASH_LIVE_SECRET"))
	} else {
		ep, _, err = clash.Detect(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	client := clash.NewClient(ep)
	defer func() { client.Close() }()
	proxyURL, err := client.ProxyURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil {
		t.Fatal("the controller has no accessible local HTTP proxy")
	}

	const chunkSize = 256 << 10
	chunk := strings.Repeat("x", chunkSize)
	next := make(chan struct{})
	reconnected := make(chan struct{})
	finish := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, wait := range []<-chan struct{}{next, reconnected, finish} {
			_, _ = io.WriteString(w, chunk)
			w.(http.Flusher).Flush()
			select {
			case <-wait:
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer origin.Close()
	defer close(finish)
	_, port, _ := net.SplitHostPort(origin.Listener.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	readChunk := func() {
		t.Helper()
		if n, err := io.CopyN(io.Discard, resp.Body, chunkSize); err != nil {
			t.Fatalf("proxy response: status %d, bytes %d: %v", resp.StatusCode, n, err)
		}
	}
	readChunk()

	s, err := store.Open(t.TempDir(), store.DeviceInfo{ID: "live-test", Name: "Live test"})
	if err != nil {
		t.Fatal(err)
	}
	c := New(s)
	c.Connected(ep.String())
	start := time.Now().Truncate(time.Hour)
	query := store.Query{From: start.UnixMilli(), To: start.Add(time.Hour).UnixMilli()}
	tick := func() (*clash.Snapshot, Live) {
		t.Helper()
		snap, err := client.Connections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return snap, c.Tick(time.Now(), snap)
	}
	before, _ := tick()
	var id string
	var initialDown int64
	for _, conn := range before.Connections {
		if conn.Metadata.DestinationPort == port && conn.Metadata.DestinationIP == "127.0.0.1" {
			id = conn.ID
			initialDown = conn.Download
		}
	}
	if id == "" {
		t.Fatal("the proxy request succeeded but this controller did not report the test connection")
	}

	// The core may not have accounted for the second chunk yet. Check the
	// test connection itself so background traffic cannot make this pass.
	check := func(phase string, want int64) {
		t.Helper()
		for {
			after, live := tick()
			r := s.Query(query)
			tracked := c.prev[id]
			if tracked != nil && tracked.down-initialDown >= want && r.Down >= want {
				t.Logf("%s (%s): core download delta %d, collected %d, live download rate %d", ep, phase, after.DownloadTotal-before.DownloadTotal, r.Down, live.DownRate)
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("%s: traffic did not reach the collector: core delta %d, collected %d: %v", phase, after.DownloadTotal-before.DownloadTotal, r.Down, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	close(next)
	readChunk()
	check("start counting an existing connection", chunkSize)

	client.Close()
	client = clash.NewClient(ep)
	c.Connected(ep.String())
	close(reconnected)
	readChunk()
	check("reconnect to the same core", 2*chunkSize)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
}
