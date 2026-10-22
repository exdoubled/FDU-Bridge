package main

import (
	"runtime"
	"sync/atomic"

	"github.com/mythologyli/zju-connect/gui/services"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var stateLabels = map[string]string{
	"idle": "未启用校内访问", "preparing": "正在准备校内访问…", "connecting": "正在连接校园 VPN…",
	"authentication": "等待登录验证", "enabling": "正在启用校内访问…", "connected": "校内访问已启用",
	"adapting": "正在适配 Clash…",
	"paused":   "校内访问已暂停", "error": "启用失败", "campus-unavailable": "校内连接不可用",
	"stopped": "已关闭校内访问", "tool-running": "正在执行工具…", "tool-finished": "工具已结束",
}

type tray struct {
	app     *application.App
	window  *application.WebviewWindow
	backend *services.Backend
	icon    *application.SystemTray
	menu    *application.Menu
	status  *application.MenuItem
	toggle  *application.MenuItem
	ready   atomic.Bool // set once the main loop runs; earlier updates are replayed then
}

func showWindow(w *application.WebviewWindow) {
	if w == nil {
		return
	}
	w.Show()
	w.Restore()
	w.Focus()
}

func newTray(app *application.App, window *application.WebviewWindow, backend *services.Backend) *tray {
	t := &tray{app: app, window: window, backend: backend}
	t.menu = app.NewMenu()
	t.status = t.menu.Add("FDU Connect · 未启用校内访问").SetEnabled(false)
	t.menu.AddSeparator()
	t.toggle = t.menu.Add("启用校内访问").OnClick(func(*application.Context) { go t.toggleConnection() })
	t.menu.AddSeparator()
	t.menu.Add("显示主窗口").OnClick(func(*application.Context) { showWindow(window) })
	t.menu.Add("退出").OnClick(func(*application.Context) { go t.quit() })
	t.icon = app.SystemTray.New()
	t.icon.SetIcon(trayOff)
	if runtime.GOOS == "darwin" {
		t.icon.SetTemplateIcon(trayTemplate)
	}
	t.icon.SetTooltip("FDU Connect")
	t.icon.SetMenu(t.menu)
	// macOS menu-bar items open their menu on click; elsewhere a click shows the window.
	if runtime.GOOS != "darwin" {
		t.icon.OnClick(func() { showWindow(window) })
		t.icon.OnRightClick(func() { t.icon.OpenMenu() })
	}
	return t
}

// update may be called from any goroutine; menu changes are marshalled to the main thread.
func (t *tray) update(status services.CoreStatus) {
	if t.ready.Load() {
		application.InvokeSync(func() { t.render(status) })
	}
}

func (t *tray) start(status services.CoreStatus) {
	t.ready.Store(true)
	t.update(status)
}

func (t *tray) render(status services.CoreStatus) {
	label := stateLabels[status.State]
	if label == "" {
		label = status.State
	}
	active := status.Running || status.Starting
	if status.Stopping {
		label = "正在关闭校内访问…"
	} else if status.External && !active {
		label = "其他 FDU 正在运行"
	}
	t.status.SetLabel("FDU Connect · " + label)
	if status.Starting {
		t.toggle.SetLabel("取消启用")
	} else if status.Running {
		t.toggle.SetLabel("关闭校内访问")
	} else {
		t.toggle.SetLabel("启用校内访问")
	}
	t.toggle.SetEnabled(!status.Stopping && (active || !status.External))
	// The macOS template icon is tinted by the system, so state shows in the menu instead.
	if runtime.GOOS != "darwin" {
		if status.Connected {
			t.icon.SetIcon(trayOn)
		} else {
			t.icon.SetIcon(trayOff)
		}
	}
	t.icon.SetTooltip("FDU Connect - " + label)
	t.menu.Update()
}

func (t *tray) toggleConnection() {
	core := services.NewCoreService(t.backend)
	status := core.Status()
	var err error
	if status.Running || status.Starting {
		var stopped bool
		if stopped, err = core.Disconnect(); err == nil && !stopped {
			t.confirm("关闭超时", "校园 VPN 尚未退出，强制结束本程序启动的进程？", func() {
				go func() { t.showError(core.ForceStop()) }()
			})
		}
	} else {
		var preview services.ConnectPreview
		if preview, err = core.PreviewConnect(); err == nil {
			if preview.NeedsConsent {
				t.confirm("启用校内访问", preview.Message, func() {
					go func() { t.showError(core.Connect(true)) }()
				})
				return
			}
			err = core.Connect(false)
		}
	}
	t.showError(err)
}

func (t *tray) showError(err error) {
	if err != nil {
		t.app.Dialog.Error().SetTitle("FDU Connect").SetMessage(err.Error()).Show()
	}
}

func (t *tray) confirm(title, message string, yes func()) {
	dialog := t.app.Dialog.Question().SetTitle(title).SetMessage(message)
	dialog.AddButton("确定").OnClick(yes)
	dialog.AddButton("取消").SetAsDefault().SetAsCancel()
	dialog.Show()
}

func (t *tray) quit() {
	ok, err := t.backend.Quit(false)
	if err != nil {
		t.confirm("接入未恢复", "强制退出可能影响当前代理。保留记录并退出 FDU？下次启用时再尝试恢复。", func() {
			go func() { _, err := t.backend.Quit(true); t.showError(err) }()
		})
		return
	}
	if !ok {
		t.confirm("退出", "强制结束 FDU 并退出？未恢复的 Clash 接入会保留待下次处理。", func() {
			go func() {
				_, err := t.backend.Quit(true)
				t.showError(err)
			}()
		})
	}
}
