package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMissingUpdateFeed(t *testing.T) {
	for _, tc := range []struct {
		name, tag             string
		apiStatus, repoStatus int
		wantError             bool
	}{
		{"no release yet", "", 404, 200, false},
		{"same version, missing asset", "v0.1.4", 200, 200, false},
		{"running a newer version", "v0.1.3", 200, 200, false},
		{"new release missing its package", "v0.1.5", 200, 200, true},
		{"GitHub rate limit", "", 403, 200, true},
		{"GitHub outage", "", 503, 200, true},
		{"repository inaccessible", "", 404, 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				status, body := 404, "not found"
				if r.URL.Hostname() == "api.github.com" {
					if strings.HasSuffix(r.URL.Path, "/releases/latest") {
						status, body = tc.apiStatus, fmt.Sprintf(`{"tag_name":%q}`, tc.tag)
					} else {
						status, body = tc.repoStatus, `{}`
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			tr := &updateFeedTransport{base: base, version: func() string { return "0.1.4" }}
			req, _ := http.NewRequest(http.MethodGet, "https://github.com/"+updateRepository+"/releases/latest/download/update-darwin-arm64.json", nil)
			resp, err := tr.RoundTrip(req)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var manifest map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 || manifest["version"] != "0.1.4" || manifest["signature"] == "" || manifest["url"] == "" {
				t.Fatalf("not an up-to-date result: %+v", manifest)
			}
		})
	}
}

func TestUniversalUpdateThroughGitHubRedirect(t *testing.T) {
	const manifest = `{"version":"0.1.5","url":"https://example.com/signed.tar.gz","signature":"unchanged"}`
	var visited []string
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		visited = append(visited, r.URL.Path)
		status, body := 404, "not found"
		headers := http.Header{}
		switch {
		case strings.Contains(r.URL.Path, "/latest/download/"):
			status = 302
			headers.Set("Location", strings.Replace(r.URL.String(), "/latest/download/", "/download/v0.1.5/", 1))
		case strings.HasSuffix(r.URL.Path, "update-darwin-universal.json"):
			status, body = 200, manifest
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	client := &http.Client{Transport: &updateFeedTransport{base: base, version: func() string { return "0.1.4" }}}
	resp, err := client.Get("https://github.com/" + updateRepository + "/releases/latest/download/update-darwin-arm64.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != manifest || len(visited) != 3 {
		t.Fatalf("manifest=%s, requests=%v", data, visited)
	}
}

func TestOther404sAreNotHidden(t *testing.T) {
	tr := &updateFeedTransport{base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("missing")), Request: r}, nil
	}), version: func() string { return "0.1.4" }}
	for _, url := range []string{"https://github.com/another/repository/releases/latest/download/update-darwin-arm64.json", "https://github.com/" + updateRepository + "/releases/download/v0.1.4/app.tar.gz"} {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := tr.RoundTrip(req)
		if err != nil || resp.StatusCode != 404 {
			t.Fatalf("404 hidden for %s: %v, %+v", url, err, resp)
		}
		resp.Body.Close()
	}
}

func TestUpdateVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"v0.1.4", "0.1.4", 0}, {"0.1.10", "0.1.9", 1}, {"0.1.4-beta.2", "0.1.4-beta.10", -1}, {"0.1.4-beta.1", "0.1.4", -1}, {"0.1.4+build.1", "0.1.4", 0}} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%s,%s)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
