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
	"clashtraffic/internal/i18n"
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
	lang := ""
	if os.Getenv("MYGO_GENERATE") == "" {
		dir, err := app.Path(mygo.PathUserData)
		if err == nil {
			dataLock, err = lockDataDir(dir, 15*time.Second)
			lang = savedLanguage(dir)
		}
		lockErr = err
	}

	core := &Core{}
	useUpdater(core, lang)
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
			mygo.Dialog.Error(i18n.T("Clash Traffic cannot start"), err.Error())
			app.Quit()
			return
		}
		if runtime.GOOS == "darwin" {
			app.SetMenu(appMenu())
			onLanguage(func() { app.SetMenu(appMenu()) })
		}
		onLanguage(core.relanguage)
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
	err := app.Run()
	// Saves the counters and syncs them even when the app failed.
	core.Stop()
	if err != nil {
		log.Fatal(err)
	}
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
		Title:          "Clash Traffic",
		Width:          1200,
		Height:         800,
		MinWidth:       960,
		MinHeight:      620,
		TitleBarStyle:  mygo.TitleBarHidden,
		TitleBarHeight: 52,
		// macOS keeps its own title bar height, which leaves the traffic
		// lights hard against the top: lower them into a title bar as tall
		// as the other platforms' (the button's height plus twice Y).
		TrafficLightPosition: &mygo.Point{X: 18, Y: 18},
		BackgroundColor:      "light-dark(#f6f7f9, #111318)", // the page's --bg
		StateKey:             "main",
		URL:                  "/",
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

// appMenu is the menu bar of macOS, in the language of the app: MyGo's
// roles label their items in English.
func appMenu() *mygo.Menu {
	item := func(role mygo.MenuRole, label string) *mygo.MenuItem {
		return &mygo.MenuItem{Role: role, Label: label}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Clash Traffic", Submenu: []*mygo.MenuItem{
			item(mygo.RoleAbout, i18n.T("About Clash Traffic")),
			updateMenuItem(),
			mygo.Separator(),
			item(mygo.RoleServices, i18n.T("Services")),
			mygo.Separator(),
			item(mygo.RoleHide, i18n.T("Hide Clash Traffic")),
			item(mygo.RoleHideOthers, i18n.T("Hide Others")),
			item(mygo.RoleUnhide, i18n.T("Show All")),
			mygo.Separator(),
			item(mygo.RoleQuit, i18n.T("Quit Clash Traffic")),
		}},
		{Label: i18n.T("Edit"), Submenu: []*mygo.MenuItem{
			item(mygo.RoleUndo, i18n.T("Undo")),
			item(mygo.RoleRedo, i18n.T("Redo")),
			mygo.Separator(),
			item(mygo.RoleCut, i18n.T("Cut")),
			item(mygo.RoleCopy, i18n.T("Copy")),
			item(mygo.RolePaste, i18n.T("Paste")),
			item(mygo.RolePasteAndMatchStyle, i18n.T("Paste and Match Style")),
			item(mygo.RoleDelete, i18n.T("Delete")),
			item(mygo.RoleSelectAll, i18n.T("Select All")),
			mygo.Separator(),
			{Label: i18n.T("Speech"), Submenu: []*mygo.MenuItem{
				item(mygo.RoleStartSpeaking, i18n.T("Start Speaking")),
				item(mygo.RoleStopSpeaking, i18n.T("Stop Speaking")),
			}},
		}},
		{Label: i18n.T("View"), Submenu: []*mygo.MenuItem{
			item(mygo.RoleReload, i18n.T("Reload")),
			item(mygo.RoleForceReload, i18n.T("Force Reload")),
			item(mygo.RoleToggleDevTools, i18n.T("Toggle Developer Tools")),
			mygo.Separator(),
			item(mygo.RoleResetZoom, i18n.T("Actual Size")),
			item(mygo.RoleZoomIn, i18n.T("Zoom In")),
			item(mygo.RoleZoomOut, i18n.T("Zoom Out")),
			mygo.Separator(),
			item(mygo.RoleToggleFullScreen, i18n.T("Toggle Full Screen")),
		}},
		{Label: i18n.T("Window"), Role: mygo.RoleWindow, Submenu: []*mygo.MenuItem{
			item(mygo.RoleMinimize, i18n.T("Minimize")),
			item(mygo.RoleZoom, i18n.T("Zoom")),
			mygo.Separator(),
			item(mygo.RoleFront, i18n.T("Bring All to Front")),
		}},
	})
}

// setupTray puts an icon in the menu bar (notification area) with today's
// traffic and the current speed.
func setupTray(core *Core) {
	today := &mygo.MenuItem{ID: "today", Label: i18n.T("This computer today: —"), Disabled: true}
	speed := &mygo.MenuItem{ID: "speed", Label: i18n.T("Live: not connected"), Disabled: true}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		today,
		speed,
		mygo.Separator(),
		{ID: "open", Label: i18n.T("Open Clash Traffic"), Click: func(*mygo.MenuItem, *mygo.Window) { showMainWindow() }},
		{ID: "sync", Label: i18n.T("Sync now"), Click: func(*mygo.MenuItem, *mygo.Window) { core.TriggerSync() }},
		updateMenuItem(),
		mygo.Separator(),
		{ID: "quit", Label: i18n.T("Quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
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
	// showToday puts today's traffic of this computer in the menu and the
	// tooltip.
	showToday := func() {
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		r := core.store.Query(store.Query{From: midnight.UnixMilli(), To: now.UnixMilli() + 1, Devices: []string{core.store.Self().ID}, Limit: 1})
		var proxied int64
		for _, it := range r.Routes {
			if it.Name == string(store.RouteProxy) {
				proxied = it.Up + it.Down
			}
		}
		today.SetLabel(i18n.T("This computer today: ↑ %s  ↓ %s (proxy %s)", formatBytes(r.Up), formatBytes(r.Down), formatBytes(proxied)))
		tray.SetToolTip(i18n.T("Clash Traffic · today %s", formatBytes(r.Up+r.Down)))
	}
	onLanguage(func() {
		for id, label := range map[string]string{
			"open":          i18n.T("Open Clash Traffic"),
			"sync":          i18n.T("Sync now"),
			"check-updates": i18n.T("Check for Updates…"),
			"quit":          i18n.T("Quit"),
		} {
			if it := menu.ItemByID(id); it != nil {
				it.SetLabel(label)
			}
		}
		// The speed shows again at the next tick, which needs the core.
		if !core.Status().Connected {
			speed.SetLabel(i18n.T("Live: not connected"))
		}
		showToday()
	})

	var lastToday time.Time
	var title string
	core.onTick = func(live collector.Live) {
		speed.SetLabel(i18n.T("Live: ↑ %s/s  ↓ %s/s", formatBytes(live.UpRate), formatBytes(live.DownRate)))
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
			showToday()
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
