package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"clashtraffic/internal/clash"
)

func TestIsGitHub(t *testing.T) {
	for host, want := range map[string]bool{
		"github.com":                            true,
		"GitHub.com.":                           true,
		"api.github.com":                        true,
		"objects.githubusercontent.com":         true,
		"release-assets.githubusercontent.com":  true,
		"notgithub.com":                         false,
		"github.com.example.org":                false,
		"dav.jianguoyun.com":                    false,
		"100.66.66.9":                           false,
		"evilgithubusercontent.com":             false,
		"raw.githubusercontent.com.example.org": false,
	} {
		if got := isGitHub(host); got != want {
			t.Errorf("isGitHub(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestUpdateProxy(t *testing.T) {
	core := &Core{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"mixed-port":7890}`))
	}))
	defer srv.Close()
	env := (*url.URL)(nil)
	old := envProxy
	envProxy = func(*http.Request) (*url.URL, error) { return env, nil }
	defer func() { envProxy = old }()

	proxy := func(rawURL string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
		u, err := core.updateProxy(req)
		if err != nil {
			t.Fatal(err)
		}
		if u == nil {
			return ""
		}
		return u.String()
	}
	const feed = "https://github.com/Easoncan/clash-traffic/releases/latest/download/update-darwin-universal.json"
	if got := proxy(feed); got != "" {
		t.Errorf("without Clash: proxy %q, want none", got)
	}

	core.client = clash.NewClient(clash.Endpoint{Kind: clash.KindHTTP, Address: srv.URL})
	if got := proxy(feed); got != "http://127.0.0.1:7890" {
		t.Errorf("GitHub: proxy %q, want Clash's", got)
	}
	if got := proxy("https://release-assets.githubusercontent.com/x.tar.gz"); got != "http://127.0.0.1:7890" {
		t.Errorf("GitHub's downloads: proxy %q, want Clash's", got)
	}
	if got := proxy("https://dav.jianguoyun.com/dav/ClashTraffic/"); got != "" {
		t.Errorf("WebDAV: proxy %q, want none", got)
	}

	// The environment's proxy comes first, for everything.
	env, _ = url.Parse("http://127.0.0.1:1080")
	if got := proxy(feed); got != "http://127.0.0.1:1080" {
		t.Errorf("with a proxy in the environment: proxy %q, want it", got)
	}
	env = nil

	// A core that does not answer leaves GitHub to be reached straight.
	srv.Close()
	if got := proxy(feed); got != "" {
		t.Errorf("Clash not answering: proxy %q, want none", got)
	}
}

// Without the updater plugin, as in tests, the settings read no update
// preferences and saving them does nothing.
func TestUpdatesWithoutPlugin(t *testing.T) {
	info := updateInfo()
	if info.Enabled || info.AutoCheck || info.AutoInstall || info.Reason == "" {
		t.Errorf("updateInfo() = %+v", info)
	}
	setUpdatePrefs(true, true)
	checkForUpdates()
	if updateMenuItem() != nil {
		t.Error("updateMenuItem() without the plugin")
	}
}
