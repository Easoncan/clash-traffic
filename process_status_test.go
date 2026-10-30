package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"clashtraffic/internal/clash"
)

func TestProcessIdentificationChecks(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wantMode                                              string
		maintain, manual, patchFails, ignorePatch, readFails, verifyFails bool
		wantError                                                         bool
		wantReads, wantPatches                                            int
		noSamples, sampleFails                                            bool
	}{
		{name: "read only even with maintenance saved", mode: "strict", wantMode: "strict", maintain: true, manual: true, wantReads: 1},
		{name: "already enabled", mode: "always", wantMode: "always", maintain: true, wantReads: 1},
		{name: "maintenance off", mode: "off", wantMode: "off", wantReads: 1},
		{name: "read fails after prior enabled status", mode: "always", readFails: true, wantError: true, wantReads: 1},
		{name: "unknown mode is not changed", mode: "unsupported", wantMode: "unsupported", maintain: true, wantError: true, wantReads: 1},
		{name: "enable and read back", mode: "strict", wantMode: "always", maintain: true, wantReads: 2, wantPatches: 1},
		{name: "enable fails", mode: "strict", wantMode: "strict", maintain: true, patchFails: true, wantError: true, wantReads: 1, wantPatches: 1},
		{name: "accepted but ineffective", mode: "strict", wantMode: "strict", maintain: true, ignorePatch: true, wantError: true, wantReads: 2, wantPatches: 1},
		{name: "cannot verify the write", mode: "strict", maintain: true, verifyFails: true, wantError: true, wantReads: 2, wantPatches: 1},
		{name: "idle is not a disabled mode", mode: "always", wantMode: "always", noSamples: true, wantReads: 1},
		{name: "sample failure is separate from mode", mode: "always", wantMode: "always", sampleFails: true, wantReads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, reads, patches := tc.mode, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/connections" {
					if r.Method != http.MethodGet {
						t.Errorf("observation changed core: %s", r.Method)
					}
					if tc.sampleFails {
						http.Error(w, "cannot observe", 500)
						return
					}
					if tc.noSamples {
						w.Write([]byte(`{"connections":[]}`))
						return
					}
					w.Write([]byte(`{"connections":[{"id":"known","metadata":{"type":"HTTP","process":"browser"}},{"id":"unknown","metadata":{"type":"Tun"}},{"id":"internal","metadata":{"type":"Inner","process":"mihomo"}}]}`))
					return
				}
				if r.URL.Path != "/configs" {
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				switch r.Method {
				case http.MethodGet:
					reads++
					if tc.readFails || tc.verifyFails && reads > 1 {
						http.Error(w, "cannot read", 500)
						return
					}
					json.NewEncoder(w).Encode(map[string]string{"find-process-mode": mode})
				case http.MethodPatch:
					patches++
					if tc.patchFails {
						http.Error(w, "cannot write", 403)
						return
					}
					if !tc.ignorePatch {
						mode = "always"
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected method %s", r.Method)
				}
			}))
			defer srv.Close()
			client := clash.NewClient(clash.Endpoint{Kind: clash.KindHTTP, Address: srv.URL})
			defer client.Close()
			c := &Core{client: client, status: ClashStatus{Connected: true, Endpoint: srv.URL, Since: 1, FindProcessMode: "always", ProcessCheckedAt: 1}, file: settingsFile{Settings: Settings{ForceProcess: tc.maintain}}}
			if tc.manual {
				c.CheckProcessIdentification(t.Context())
			} else {
				c.checkProcessMode(t.Context(), client)
			}
			s := c.Status()
			if s.FindProcessMode != tc.wantMode || (s.ProcessCheckError != "") != tc.wantError || s.ProcessCheckedAt <= 1 || reads != tc.wantReads || patches != tc.wantPatches {
				t.Fatalf("status=%+v reads=%d patches=%d; want mode=%q error=%v reads=%d patches=%d", s, reads, patches, tc.wantMode, tc.wantError, tc.wantReads, tc.wantPatches)
			}
			if !tc.wantError && !tc.noSamples && !tc.sampleFails && (s.ProcessConnections != 2 || s.ProcessIdentified != 1 || s.ProcessSupplemented != 0) {
				t.Fatalf("actual process observations are wrong: %+v", s)
			}
			if (s.ProcessSampleError != "") != tc.sampleFails {
				t.Fatalf("sample failure mixed with runtime check: %+v", s)
			}
		})
	}
}

func TestProcessIdentificationDefaultsOn(t *testing.T) {
	if !defaultSettings().ForceProcess {
		t.Fatal("process identification must be enabled by default")
	}
}

func TestOldProcessCheckCannotCertifyNewConnection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.Write([]byte(`{"find-process-mode":"always"}`))
	}))
	defer srv.Close()
	client := clash.NewClient(clash.Endpoint{Kind: clash.KindHTTP, Address: srv.URL})
	defer client.Close()
	c := &Core{client: client, status: ClashStatus{Connected: true, Since: 1}}
	done := make(chan struct{})
	go func() { defer close(done); c.CheckProcessIdentification(context.Background()) }()
	<-started
	c.setStatus(ClashStatus{Connected: true, Endpoint: "another core", Since: 2})
	close(release)
	<-done
	if s := c.Status(); s.FindProcessMode != "" || s.ProcessCheckedAt != 0 {
		t.Fatalf("stale result copied to new core: %+v", s)
	}
}

func TestReconnectDropsProcessCheck(t *testing.T) {
	c := &Core{status: ClashStatus{Connected: true, FindProcessMode: "always", ProcessCheckedAt: 123}}
	c.setStatus(ClashStatus{Connected: true, Endpoint: "new core"})
	if s := c.Status(); s.FindProcessMode != "" || s.ProcessCheckedAt != 0 {
		t.Fatalf("reconnect inherited an old result: %+v", s)
	}
}
