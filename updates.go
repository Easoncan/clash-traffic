package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
)

// UpdateInfo is what the settings page shows of updates.
type UpdateInfo struct {
	// Enabled is false when this build cannot update itself, for Reason.
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason"`
	// LastCheck is when the app last checked, in Unix milliseconds; 0 for
	// never.
	LastCheck int64 `json:"lastCheck"`
	// The choices the update window can change too.
	AutoCheck   bool `json:"autoCheck"`
	AutoInstall bool `json:"autoInstall"`
}

// UpdateEvent tells the page that the update preferences changed, from
// the update window as well.
var UpdateEvent = mygo.NewEvent[UpdateInfo]("updates")

// updaterUsed is set once the app uses the updater plugin, whose functions
// panic without it: tests run without.
var updaterUsed bool

// useUpdater checks for updates once a day, in the update window of the
// plugin, in Chinese as the rest of the app. Requests to GitHub go through
// the proxy of Clash.
func useUpdater(core *Core) {
	mygo.Use(updater.New(updater.Options{Language: "zh-Hans"}))
	updaterUsed = true
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		t.Proxy = core.updateProxy
	}
	updater.OnChange(func() { _ = UpdateEvent.Broadcast(updateInfo()) })
}

// updateProxy is the proxy of the app's own requests (the updater's and
// WebDAV's): the environment's, which an app opened from the Finder or the
// Start menu has none of, else for GitHub the core's. With the system proxy
// of Clash Verge on, rather than its TUN mode, the browsers reach GitHub
// through it but the app would not, and GitHub can be slow or out of
// reach without.
func (c *Core) updateProxy(r *http.Request) (*url.URL, error) {
	if u, err := envProxy(r); u != nil || err != nil {
		return u, err
	}
	if !isGitHub(r.URL.Hostname()) {
		return nil, nil
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	u, err := client.ProxyURL(ctx)
	if err != nil {
		return nil, nil // straight to GitHub, then
	}
	return u, nil
}

// envProxy is the proxy the environment names; replaced by tests, as
// net/http reads the environment once.
var envProxy = http.ProxyFromEnvironment

// isGitHub reports a host that serves GitHub's releases: the release pages
// redirect downloads to githubusercontent.com.
func isGitHub(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range []string{"github.com", "githubusercontent.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// updateMenuItem is "检查更新…", or nil without the updater.
func updateMenuItem() *mygo.MenuItem {
	if !updaterUsed {
		return nil
	}
	return updater.MenuItem()
}

func updateInfo() UpdateInfo {
	// Enabled tries to write where the app is installed.
	info := UpdateInfo{Enabled: mygo.Updater.Enabled()}
	if !info.Enabled {
		info.Reason = whyNoUpdates()
	}
	info.AutoCheck, info.AutoInstall = updatePrefs()
	if updaterUsed {
		if t := updater.LastCheck(); !t.IsZero() {
			info.LastCheck = t.UnixMilli()
		}
	}
	return info
}

// updatePrefs returns whether the app checks for updates by itself and
// installs them without asking.
func updatePrefs() (autoCheck, autoInstall bool) {
	if !updaterUsed {
		return false, false
	}
	return updater.AutomaticChecks(), updater.AutomaticDownloads()
}

// whyNoUpdates explains why this build cannot update itself.
func whyNoUpdates() string {
	if mygo.IsDev() {
		return "开发版不会自动更新"
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	switch {
	case runtime.GOOS == "darwin" && strings.HasPrefix(exe, "/Volumes/"):
		return "正在从磁盘映像运行：请先把 Clash Traffic 拖到“应用程序”文件夹"
	case runtime.GOOS == "darwin" && strings.Contains(exe, "/AppTranslocation/"):
		return "macOS 正在隔离运行本应用：请把它移到“应用程序”文件夹后重新打开"
	case runtime.GOOS == "linux" && (strings.HasPrefix(exe, "/opt/") || strings.HasPrefix(exe, "/usr/")):
		return "通过软件包安装：请用系统的软件包管理器更新"
	}
	return "没有写入安装目录的权限，无法自动更新"
}

// setUpdatePrefs applies the update choices of saved settings.
func setUpdatePrefs(autoCheck, autoInstall bool) {
	if !updaterUsed {
		return
	}
	if autoCheck != updater.AutomaticChecks() {
		updater.SetAutomaticChecks(autoCheck)
	}
	if autoInstall != updater.AutomaticDownloads() {
		updater.SetAutomaticDownloads(autoInstall)
	}
}

// checkForUpdates checks now, in the update window.
func checkForUpdates() {
	if updaterUsed {
		updater.CheckForUpdates()
	}
}
