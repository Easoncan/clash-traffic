package main

import (
	"cmp"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"clashtraffic/internal/i18n"
)

const updateRepository = "Easoncan/clash-traffic"

var manifestPath = regexp.MustCompile(`^/` + updateRepository + `/releases/(latest/download|download/[^/]+)/update-[a-z0-9-]+\.json$`)
var releaseVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// MyGo 0.4.8 treats a missing manifest as a fatal check error and requests
// this build's exact architecture. Releases may instead ship a universal
// macOS package, or may not exist yet. Adapt only this repository's feeds;
// successful manifests and signed archive downloads remain untouched.
type updateFeedTransport struct {
	base    http.RoundTripper
	version func() string
}

func (t *updateFeedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusNotFound || req.Method != http.MethodGet || req.URL.Hostname() != "github.com" || !manifestPath.MatchString(req.URL.Path) {
		return resp, err
	}
	resp.Body.Close()
	for _, arch := range []string{"arm64", "amd64"} {
		if strings.HasSuffix(req.URL.Path, "/update-darwin-"+arch+".json") {
			fallback := req.Clone(req.Context())
			u := *req.URL
			u.Path = strings.TrimSuffix(u.Path, "update-darwin-"+arch+".json") + "update-darwin-universal.json"
			fallback.URL = &u
			resp, err = t.base.RoundTrip(fallback)
			if err != nil || resp.StatusCode != http.StatusNotFound {
				return resp, err
			}
			resp.Body.Close()
			break
		}
	}
	client := &http.Client{Transport: t.base, Timeout: 15 * time.Second}
	apiURL := "https://api.github.com/repos/" + updateRepository
	get := func(path string, out any) (int, error) {
		r, err := http.NewRequestWithContext(req.Context(), http.MethodGet, apiURL+path, nil)
		if err != nil {
			return 0, err
		}
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("User-Agent", "Clash-Traffic")
		res, err := client.Do(r)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return res.StatusCode, nil
		}
		if out == nil {
			return res.StatusCode, nil
		}
		return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
	}
	var latest struct {
		Tag string `json:"tag_name"`
	}
	status, err := get("/releases/latest", &latest)
	if err != nil {
		return nil, err
	}
	current := t.version()
	if status == http.StatusNotFound {
		// Distinguish a public repository with no releases from a typo,
		// inaccessible repository or GitHub outage.
		status, err = get("", nil)
		if err != nil {
			return nil, err
		}
	} else if status == http.StatusOK {
		if !releaseVersion.MatchString(latest.Tag) || !releaseVersion.MatchString(current) {
			return nil, i18n.Errorf("Could not read the published update version")
		}
		if compareVersions(latest.Tag, current) > 0 {
			return nil, i18n.Errorf("Version %s is published, but its update package is not available yet. Please try again later.", latest.Tag)
		}
	}
	if status != http.StatusOK {
		return nil, i18n.Errorf("GitHub could not check for updates (HTTP %d). Please try again later.", status)
	}
	// The plugin has no public hook for a no-update result. Its core
	// requires these three fields before comparing versions. This current-
	// version response always compares equal and can never be installed.
	body, _ := json.Marshal(map[string]string{"version": current, "url": "https://github.com/" + updateRepository + "/releases", "signature": "no-update"})
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body)), Request: req}, nil
}

// Keep the same semantic ordering as MyGo's updater, including prereleases.
func compareVersions(a, b string) int {
	parts := func(v string) (string, string) {
		v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
		release, pre, _ := strings.Cut(v, "-")
		return release, pre
	}
	fields := func(a, b string) int {
		x, y := strings.Split(a, "."), strings.Split(b, ".")
		for i := 0; i < max(len(x), len(y)); i++ {
			if i >= len(x) {
				return -1
			}
			if i >= len(y) {
				return 1
			}
			if x[i] == y[i] {
				continue
			}
			xn, xe := strconv.Atoi(x[i])
			yn, ye := strconv.Atoi(y[i])
			if xe == nil && ye == nil {
				return cmp.Compare(xn, yn)
			}
			if xe == nil {
				return -1
			}
			if ye == nil {
				return 1
			}
			return strings.Compare(x[i], y[i])
		}
		return 0
	}
	ar, ap := parts(a)
	br, bp := parts(b)
	if c := fields(ar, br); c != 0 {
		return c
	}
	if ap == bp {
		return 0
	}
	if ap == "" {
		return 1
	}
	if bp == "" {
		return -1
	}
	return fields(ap, bp)
}
