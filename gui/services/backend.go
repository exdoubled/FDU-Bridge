// Package services exposes the GUI backend to the frontend through Wails bindings.
package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/core"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	EventStatus    = "core:status"
	EventLogs      = "core:logs"
	EventChallenge = "core:challenge"
	EventChanged   = "app:changed"
	EventClose     = "app:close-requested"
	logLimit       = 5000
)

// CoreStatus is everything the home page and tray need to describe the connection.
type CoreStatus struct {
	State       string `json:"state"`
	Running     bool   `json:"running"`
	Connected   bool   `json:"connected"`
	External    bool   `json:"external"`
	Stopping    bool   `json:"stopping"`
	Tool        string `json:"tool"`
	Server      string `json:"server"`
	Protocol    string `json:"protocol"`
	Socks       string `json:"socks"`
	HTTP        string `json:"http"`
	ConnectedAt int64  `json:"connectedAt"`
	Admin       bool   `json:"admin"`
	Saved       bool   `json:"saved"` // a saved config exists, so Connect can run
	Starting    bool   `json:"starting"`
	Enabled     bool   `json:"enabled"`
	Coverage    string `json:"coverage"`
	Problem     string `json:"problem"`
}

func init() {
	application.RegisterEvent[CoreStatus](EventStatus)
	application.RegisterEvent[[]string](EventLogs)
	application.RegisterEvent[map[string]any](EventChallenge)
	application.RegisterEvent[string](EventChanged)
	application.RegisterEvent[string](EventClose)
}

// Backend holds the state shared by every bound service.
type Backend struct {
	Dir     string // user data: config, rules, login state
	exeDir  string // program files: core and example config
	App     *application.App
	Window  *application.WebviewWindow
	OnState func(CoreStatus)               // tray hook
	Quit    func(force bool) (bool, error) // set by main; stops the core then exits

	conf      *appconf.Store
	runtime   *core.Runtime
	workspace *settings.Workspace
	conn      *clash.Connection

	mu           sync.Mutex
	config       appconf.Config
	view         *settings.View
	saved        map[string]any // values of the saved config file, which Connect uses
	plan         *clash.Plan
	state        string
	connectedAt  time.Time
	stopping     bool
	external     bool
	rulesPath    string
	logs         []string
	pending      []string
	challenge    map[string]any
	starting     bool
	enabled      bool
	coverage     string
	problem      string
	campus       *clash.CampusAttachment
	cancel       context.CancelFunc
	startDone    chan struct{}
	follow       bool // the user's switch stays on while Clash changes or restarts
	followTarget *clash.CampusTarget
	followRetry  time.Time

	clashBusy sync.Mutex
	prefsBusy sync.Mutex
}

// NewBackend keeps user data in dir and expects the core beside the executable
// (or in dir, for development builds that point FDU_GUI_DIR at a prepared folder).
func NewBackend(dir, executable string) (*Backend, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	workspace, err := settings.New(dir)
	if err != nil {
		return nil, err
	}
	exeDir := filepath.Dir(executable)
	runtime := core.New(exeDir)
	if alternative := filepath.Join(dir, core.CoreName); !fileExists(runtime.Core) && fileExists(alternative) {
		runtime.Core = alternative
	}
	b := &Backend{Dir: dir, exeDir: exeDir, conf: appconf.New(dir, executable), runtime: runtime, workspace: workspace,
		conn: &clash.Connection{Dir: dir}, state: "idle"}
	b.config = b.conf.Load()
	b.loadCampusSession()
	b.runtime.OnLog = b.appendLog
	b.runtime.OnState = b.onCoreState
	b.runtime.OnChallenge = b.onChallenge
	return b, nil
}

func (b *Backend) emit(name string, data any) {
	if b.App != nil {
		b.App.Event.Emit(name, data)
	}
}

// Start loads settings and begins background refreshes; called once the app is running.
func (b *Backend) Start(autoConnect bool) {
	b.loadInitialSettings()
	go b.flushLogs()
	go b.watchExternal()
	if autoConnect && b.config.AutoConnect && b.config.CampusConsent {
		go func() {
			if b.runtime.ExternalRunning() {
				b.appendLog("检测到其他 FDU 正在运行，已跳过自动连接。")
				return
			}
			if err := b.connect(false); err != nil {
				b.appendLog("自动连接失败：" + err.Error())
			}
		}()
	}
}

// refreshSaved re-reads the saved config so status describes what Connect will use.
func (b *Backend) refreshSaved() {
	var values map[string]any
	if fileExists(b.workspace.Target) {
		if reply, err := clashsetup.ReadSettings(b.workspace.Target, b.Dir); err == nil {
			values = reply.Values
		}
	}
	b.mu.Lock()
	b.saved = values
	b.mu.Unlock()
}

func (b *Backend) loadInitialSettings() {
	defer b.refreshSaved()
	candidates := []struct {
		path    string
		managed bool
	}{{b.config.ConfigPath, true}, {b.workspace.Target, true},
		{filepath.Join(b.exeDir, "client.toml"), false},
		{filepath.Join(b.Dir, "config.toml.example"), false},
		{filepath.Join(b.exeDir, "config.toml.example"), false},
		{filepath.Join(b.exeDir, "..", "Resources", "config.toml.example"), false}} // macOS app bundle
	for _, c := range candidates {
		if c.path == "" {
			continue
		}
		if _, err := os.Stat(c.path); err != nil {
			continue
		}
		if c.managed && !b.workspace.Private(c.path) {
			continue
		}
		if view, err := b.workspace.Load(c.path, c.managed); err == nil {
			b.mu.Lock()
			b.view = &view
			b.mu.Unlock()
			b.bindRules(view)
			return
		}
	}
}

// bindRules follows the rules file named by the settings, as the old manager did.
func (b *Backend) bindRules(view settings.View) {
	path := filepath.Join(b.Dir, "routing-rules.json")
	if routing, ok := view.Values["routing"].(map[string]any); ok {
		if file, _ := routing["rules_file"].(string); file != "" {
			path = file
		}
	}
	b.mu.Lock()
	b.rulesPath = path
	b.mu.Unlock()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (b *Backend) saveConfig(update func(*appconf.Config)) error {
	b.prefsBusy.Lock()
	defer b.prefsBusy.Unlock()
	b.mu.Lock()
	next := b.config
	update(&next)
	b.mu.Unlock()
	if err := b.conf.Save(next); err != nil {
		return err
	}
	b.mu.Lock()
	b.config = next
	b.mu.Unlock()
	return nil
}

func (b *Backend) appendLog(line string) {
	b.mu.Lock()
	b.logs = append(b.logs, line)
	if len(b.logs) > logLimit {
		b.logs = append([]string(nil), b.logs[len(b.logs)-logLimit:]...)
	}
	b.pending = append(b.pending, line)
	b.mu.Unlock()
}

// flushLogs batches lines so a chatty core does not flood the event bridge.
func (b *Backend) flushLogs() {
	for range time.Tick(150 * time.Millisecond) {
		b.mu.Lock()
		batch := b.pending
		b.pending = nil
		b.mu.Unlock()
		if len(batch) > 0 {
			b.emit(EventLogs, batch)
		}
	}
}

func (b *Backend) watchExternal() {
	for range time.Tick(3 * time.Second) {
		external := !b.runtime.Running() && b.runtime.ExternalRunning()
		b.mu.Lock()
		changed := external != b.external
		b.external = external
		b.mu.Unlock()
		if changed {
			b.publish()
		}
		b.refreshCampus()
	}
}

func (b *Backend) onCoreState(state string) {
	b.mu.Lock()
	if b.follow && state == "connected" && !b.enabled {
		b.state = "enabling"
	} else if !b.starting && !b.enabled {
		b.state = state
	} else if state == "authentication" || state == "campus-unavailable" {
		b.state = state
	} else if state == "connected" && b.starting {
		b.state = "enabling"
	}
	if state != "connected" {
		b.connectedAt = time.Time{}
	}
	if state == "stopped" || state == "tool-finished" {
		b.challenge = nil
	}
	b.mu.Unlock()
	b.publish()
}

func (b *Backend) onChallenge(challenge map[string]any) {
	b.mu.Lock()
	b.challenge = challenge
	b.mu.Unlock()
	if b.Window != nil {
		b.Window.Show()
		b.Window.Restore()
		b.Window.Focus()
	}
	b.emit(EventChallenge, challenge)
}

func text(values map[string]any, key string) string {
	var current any = values
	for _, part := range strings.Split(key, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[part]
	}
	switch v := current.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return fmt.Sprint(current)
}

func (b *Backend) Status() CoreStatus {
	running, connected, tool := b.runtime.Running(), b.runtime.Connected(), b.runtime.Tool()
	b.mu.Lock()
	defer b.mu.Unlock()
	status := CoreStatus{State: b.state, Running: running, Connected: connected && b.enabled, External: b.external,
		Stopping: b.stopping, Tool: tool, Admin: sysproc.IsAdmin(), Starting: b.starting,
		Enabled: b.enabled, Coverage: b.coverage, Problem: b.problem}
	if !b.connectedAt.IsZero() {
		status.ConnectedAt = b.connectedAt.UnixMilli()
	}
	if v := b.saved; v != nil {
		status.Saved = true
		status.Server = text(v, "server_address")
		if port := text(v, "server_port"); port != "" {
			status.Server += ":" + port
		}
		status.Protocol, status.Socks, status.HTTP = text(v, "protocol"), text(v, "socks_bind"), text(v, "http_bind")
	}
	return status
}

func (b *Backend) publish() {
	status := b.Status()
	b.emit(EventStatus, status)
	if b.OnState != nil {
		b.OnState(status)
	}
}

var errEditingLocked = errors.New("请先断开本程序启动的 FDU")

// Shutdown follows the same detach-before-stop order as the power switch.
func (b *Backend) Shutdown() {
	defer b.conn.Close()
	if stopped, err := b.Disconnect(); err != nil {
		b.appendLog("退出时清理失败：" + err.Error())
	} else if !stopped {
		_ = b.forceStop()
	}
}

func (b *Backend) setStopping(value bool) {
	b.mu.Lock()
	b.stopping = value
	b.mu.Unlock()
	b.publish()
}

func (b *Backend) Disconnect() (bool, error) {
	return b.disconnect(false)
}

func (b *Backend) currentPlan() *clash.Plan {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.plan
}

func (b *Backend) picker() *clash.Picker {
	root, _ := b.campusRoot()
	p := &clash.Picker{ClashDir: root, Campus: true}
	if a := b.campusAttachment(); a != nil {
		p.Unsafe = clash.UnsafeSet(clash.Plan{UnsafeNodes: a.UnsafeNodes})
	} else if plan := b.currentPlan(); plan != nil {
		p.Front, p.Unsafe = plan.Mode == "fdu-first", clash.UnsafeSet(*plan)
	}
	return p
}

func (b *Backend) currentView() *settings.View {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.view
}

func (b *Backend) setView(view settings.View) {
	b.mu.Lock()
	b.view = &view
	b.mu.Unlock()
	b.bindRules(view)
}
