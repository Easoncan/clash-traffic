// Clash Traffic counts the traffic of Clash Verge by application, host and
// proxy, and adds up the traffic of the user's computers.
package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"clashtraffic/internal/collector"
	"clashtraffic/internal/icon"
	"clashtraffic/internal/store"
)

func main() {
	app := mygo.App
	if !app.RequestSingleInstanceLock() {
		return // the running instance shows its window
	}
	// The lock above is let go before an instance saves its counters on its
	// way out: wait for that before reading them, and never go without it.
	var lockErr error
	if os.Getenv("MYGO_GENERATE") == "" {
		dir, err := app.Path(mygo.PathUserData)
		if err == nil {
			dataLock, err = lockDataDir(dir, 15*time.Second)
		}
		lockErr = err
	}

	core := &Core{}
	mygo.Bind(&Traffic{core})
	mygo.Bind(&Monitor{core})
	mygo.Bind(&Preferences{core})
	mygo.Bind(&Sync{core})

	app.OnSecondInstance(func([]string, string) { showMainWindow() })
	app.WhenReady(func() {
		dir, err := app.Path(mygo.PathUserData)
		if err == nil {
			err = core.Init(dir, lockErr)
		}
		if err != nil {
			mygo.Dialog.Error("Clash Traffic 无法启动", err.Error())
			app.Quit()
			return
		}
		if runtime.GOOS == "darwin" {
			app.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
				{Role: mygo.RoleAppMenu},
				{Role: mygo.RoleEditMenu},
				{Role: mygo.RoleViewMenu},
				{Role: mygo.RoleWindowMenu},
			}))
		}
		setupTray(core)
		core.Start()
		// Started at login, it counts in the background, in the tray.
		if !app.WasOpenedAtLogin() {
			showMainWindow()
		}
	})
	// Closing the window keeps counting; the tray brings it back.
	app.OnWindowAllClosed(func() {})
	app.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			showMainWindow()
		}
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	core.Stop()
}

var (
	winMu   sync.Mutex
	mainWin *mygo.Window
)

func showMainWindow() {
	winMu.Lock()
	defer winMu.Unlock()
	if mainWin != nil && !mainWin.IsDestroyed() {
		if mainWin.IsMinimized() {
			mainWin.Restore()
		}
		mainWin.Show()
		mainWin.Focus()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:           "Clash Traffic",
		Width:           1200,
		Height:          800,
		MinWidth:        960,
		MinHeight:       620,
		TitleBarStyle:   mygo.TitleBarHidden,
		TitleBarHeight:  52,
		BackgroundColor: "light-dark(#f6f7f9, #111318)", // the page's --bg
		StateKey:        "main",
		URL:             "/",
	})
	w.OnClosed(func() {
		winMu.Lock()
		if mainWin == w {
			mainWin = nil
		}
		winMu.Unlock()
	})
	mainWin = w
}

// setupTray puts an icon in the menu bar (notification area) with today's
// traffic and the current speed.
func setupTray(core *Core) {
	today := &mygo.MenuItem{ID: "today", Label: "本机今日：—", Disabled: true}
	speed := &mygo.MenuItem{ID: "speed", Label: "实时：未连接", Disabled: true}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		today,
		speed,
		mygo.Separator(),
		{Label: "打开 Clash Traffic", Click: func(*mygo.MenuItem, *mygo.Window) { showMainWindow() }},
		{Label: "立即同步", Click: func(*mygo.MenuItem, *mygo.Window) { core.TriggerSync() }},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           icon.Tray(32),
		IconIsTemplate: true,
		ToolTip:        "Clash Traffic",
		Menu:           menu,
	})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	today, speed = menu.ItemByID("today"), menu.ItemByID("speed")

	var lastToday time.Time
	var title string
	core.onTick = func(live collector.Live) {
		speed.SetLabel(fmt.Sprintf("实时：↑ %s/s  ↓ %s/s", formatBytes(live.UpRate), formatBytes(live.DownRate)))
		t := ""
		if core.settings().TraySpeed {
			t = "↓" + formatBytes(live.DownRate) + "/s"
		}
		if t != title {
			title = t
			tray.SetTitle(t)
		}
		if time.Since(lastToday) >= 10*time.Second {
			lastToday = time.Now()
			now := time.Now()
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
			r := core.store.Query(store.Query{From: midnight.UnixMilli(), To: now.UnixMilli() + 1, Devices: []string{core.store.Self().ID}, Limit: 1})
			var proxied int64
			for _, it := range r.Routes {
				if it.Name == string(store.RouteProxy) {
					proxied = it.Up + it.Down
				}
			}
			today.SetLabel(fmt.Sprintf("本机今日：↑ %s  ↓ %s（代理 %s）", formatBytes(r.Up), formatBytes(r.Down), formatBytes(proxied)))
			tray.SetToolTip(fmt.Sprintf("Clash Traffic · 今日 %s", formatBytes(r.Up+r.Down)))
		}
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit || u == "TB" {
			if f < 10 {
				return fmt.Sprintf("%.2f %s", f, u)
			}
			if f < 100 {
				return fmt.Sprintf("%.1f %s", f, u)
			}
			return fmt.Sprintf("%.0f %s", f, u)
		}
	}
	return ""
}
