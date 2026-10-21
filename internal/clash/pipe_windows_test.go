package clash

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func TestPipePollingAndRestart(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\clash-traffic-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	var total atomic.Int64
	start := func() *http.Server {
		t.Helper()
		ln, err := winio.ListenPipe(name, nil)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/version" {
				_, _ = w.Write([]byte(`{"version":"test"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(Snapshot{DownloadTotal: total.Load()})
		})}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		return srv
	}
	server := start()
	client := NewClient(Endpoint{Kind: KindPipe, Address: name})
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := client.Version(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		s, err := client.Connections(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if s.DownloadTotal != want {
			t.Fatalf("download = %d, want %d", s.DownloadTotal, want)
		}
	}
	check(0)
	total.Store(1024)
	check(1024) // an idle controller starts carrying traffic

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	total.Store(32)
	start()
	check(32) // the same client recovers after the pipe server restarts
}
