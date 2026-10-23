// FDU Connect is the desktop client for Windows, macOS and Linux: it runs the fdu-connect
// core as a child process and manages the Clash Verge integration, exit nodes and routing rules.
package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
	"github.com/mythologyli/zju-connect/gui/services"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/tray-on.png
var trayOn []byte

//go:embed assets/tray-off.png
var trayOff []byte

// The macOS menu bar wants a monochrome template image that the system tints.
//
//go:embed assets/tray-template.png
var trayTemplate []byte

func main() {
	if updates.RunHelper() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	dir := appconf.DataDir(filepath.Dir(exe))
	if override := os.Getenv("FDU_GUI_DIR"); override != "" {
		dir = override // lets `wails3 dev` use a prepared bin directory
	}
	minimized := slices.Contains(os.Args[1:], "--minimized")
	backend, err := services.NewBackend(dir, exe)
	if err != nil {
		log.Fatal(err)
	}
	clash := services.NewClashService(backend)
	var window *application.WebviewWindow
	var t *tray
	var quitting atomic.Bool
	app := application.New(application.Options{
		Name:        "FDU Connect",
		Description: "复旦大学 VPN 与分流管理",
		Services: []application.Service{
			application.NewService(services.NewCoreService(backend)),
			application.NewService(clash),
			application.NewService(services.NewRulesService(backend)),
			application.NewService(services.NewSettingsService(backend)),
			application.NewService(services.NewMaintenanceService(backend)),
			application.NewService(services.NewAppService(backend)),
			application.NewService(services.NewUpdateService(backend)),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "cn.edu.fudan.fdu-connect.gui",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { showWindow(window) },
		},
		// Closing the window keeps the app (and the core) running in the tray on every platform.
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "fdu-connect-gui"},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		// Native quit requests use the same restore-before-stop path as the tray.
		ShouldQuit: func() bool {
			if quitting.Load() {
				return true
			}
			if t != nil {
				go t.quit()
			}
			return false
		},
		OnShutdown: backend.Shutdown,
	})
	if runtime.GOOS == "darwin" {
		// Without an Edit menu, Cmd+C/V/X/A do nothing in the WebView's text fields.
		menu := app.Menu.New()
		menu.AddRole(application.AppMenu)
		menu.AddRole(application.EditMenu)
		menu.AddRole(application.WindowMenu)
		app.Menu.Set(menu)
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { showWindow(window) })
	}
	backend.App = app
	// Match the page background so the window never flashes the wrong colour while loading.
	background := application.NewRGB(244, 245, 247)
	if theme := services.NewAppService(backend).Config().Theme; theme == "dark" || (theme == "system" && app.Env.IsDarkMode()) {
		background = application.NewRGB(24, 25, 28)
	}
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "FDU Connect",
		Width:            980,
		Height:           660,
		MinWidth:         840,
		MinHeight:        560,
		Hidden:           minimized,
		URL:              "/",
		BackgroundColour: background,
	})
	backend.Window = window
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		if services.NewAppService(backend).Config().CloseToTray {
			window.Hide()
			return
		}
		app.Event.Emit(services.EventClose, "")
	})
	t = newTray(app, window, backend)
	backend.OnState = t.update
	backend.Quit = func(force bool) (bool, error) {
		if force {
			if err := services.NewCoreService(backend).ForceStop(); err != nil {
				return false, err
			}
		} else if stopped, err := backend.Disconnect(); err != nil || !stopped {
			return false, err
		}
		quitting.Store(true)
		app.Quit()
		return true, nil
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		t.start(backend.Status())
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
