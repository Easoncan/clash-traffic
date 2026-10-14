package clash

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// Service mode can use a different pipe from the stale sidecar address in
// Verge's configuration, even when the core's command line is inaccessible.
func TestCandidatesRunningPipeWithStaleConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("APPDATA", configDir)
	t.Setenv("USERPROFILE", configDir)
	dir := filepath.Join(configDir, "io.github.clash-verge-rev.clash-verge-rev")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	stale := `\\.\pipe\verge-mihomo-sidecar-release-` + id
	live := `\\.\pipe\verge-mihomo-production-` + id
	if err := os.WriteFile(filepath.Join(dir, "clash-verge.yaml"), []byte("external-controller-pipe: '"+stale+"'\nexternal-controller: ''\n"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := winio.ListenPipe(live, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"version":"test-core","meta":true}`)
		case "/connections":
			fmt.Fprint(w, `{"uploadTotal":12,"downloadTotal":34,"connections":[]}`)
		default:
			http.NotFound(w, r)
		}
	})}
	defer server.Close()
	defer listener.Close()
	go server.Serve(listener)

	var foundStale, foundLive bool
	for _, ep := range Candidates() {
		if ep.Address == stale {
			foundStale = true
		}
		if ep.Address != live {
			continue
		}
		foundLive = true
		client := NewClient(ep)
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if version, err := client.Version(ctx); err != nil || version != "test-core" {
			t.Fatalf("discovered pipe version = %q, %v", version, err)
		}
		if snapshot, err := client.Connections(ctx); err != nil {
			t.Fatal(err)
		} else if snapshot.UploadTotal != 12 || snapshot.DownloadTotal != 34 {
			t.Fatalf("unexpected snapshot: %+v", snapshot)
		}
	}
	if !foundStale || !foundLive {
		t.Fatalf("stale configuration found = %v, running service pipe found = %v", foundStale, foundLive)
	}
}
