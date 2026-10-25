package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

type ConnectPreview struct {
	NeedsConsent bool   `json:"needsConsent"`
	Message      string `json:"message"`
	Profile      string `json:"profile"`
}

func (b *Backend) campusRoot() (string, error) {
	b.mu.Lock()
	settings, root := b.config.Integration, b.config.ClashDir
	b.mu.Unlock()
	if settings.Mode == "proxy" {
		b.conn.ConfigureTarget("", nil)
		return "", nil
	}
	preferred := settings.RuntimeFile
	if settings.Mode == "manual" {
		target, err := clash.ManualTarget(preferred, settings.Controller, settings.Secret)
		if err != nil {
			return "", err
		}
		b.conn.ConfigureTarget(target.RuntimeFile, target.Controller)
		return target.Root, nil
	}
	var target clash.ClientTarget
	var err error
	if b.runtime.Running() && b.autoTarget != nil {
		var clients []clash.ClientTarget
		clients, err = discoverClients()
		if err == nil {
			target, err = followingClient(*b.autoTarget, clients)
		}
	} else {
		target, err = b.automaticTarget(preferred, root)
	}
	if err != nil {
		return "", err
	}
	if target.Kind == "verge" {
		b.conn.ConfigureTarget("", nil)
	} else {
		if b.runtime.Running() && b.autoTarget != nil {
			b.conn.ConfigureFollowingTarget(target.RuntimeFile, target.Controller)
		} else {
			b.conn.ConfigureTarget(target.RuntimeFile, target.Controller)
		}
	}
	b.autoTarget = &target
	return target.Root, nil
}

func (b *Backend) previewConnect() (ConnectPreview, error) {
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	view := b.currentView()
	if view == nil {
		return ConnectPreview{}, errors.New("未找到默认 VPN 配置，请在高级设置中导入")
	}
	if b.proxyOnly() {
		b.mu.Lock()
		consent := b.config.CampusConsent
		b.mu.Unlock()
		return ConnectPreview{NeedsConsent: !consent, Profile: "本地代理", Message: "启动本地校园代理，不操作其他客户端。请在应用或代理客户端中设置 SOCKS5/HTTP 代理与分流规则。"}, nil
	}
	root, err := b.campusRoot()
	if err != nil {
		return ConnectPreview{}, err
	}
	values, err := availableProxyPorts(view.Values)
	if err != nil {
		return ConnectPreview{}, err
	}
	plan, err := b.conn.PrepareCampus(root, b.workspace.Target, values)
	if err != nil {
		return ConnectPreview{}, err
	}
	b.mu.Lock()
	consent := b.config.CampusConsent
	b.mu.Unlock()
	return ConnectPreview{NeedsConsent: !consent, Profile: plan.Profile,
		Message: "将备份并为现有 Clash 添加校内访问，自动跟随全局/规则模式和订阅切换，保留外网设置。停用时恢复出口。\n不创建 FDU 虚拟网卡，不改 Clash TUN、系统代理或绕过列表，仅接管进入 Clash 的流量。"}, nil
}

// connect returns before browser authentication so the same switch can cancel it.
func (b *Backend) connect(approved bool) error {
	return b.connectWithLogin(approved, false)
}

func (b *Backend) connectWithLogin(approved, fresh bool) error {
	return b.beginConnect(approved, fresh, false)
}

func (b *Backend) beginConnect(approved, fresh, waitClash bool) error {
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	if b.runtime.Running() || b.runtime.ExternalRunning() {
		return errors.New("已有 FDU 在运行，请先关闭后再启用")
	}
	b.mu.Lock()
	if b.starting || b.stopping || b.updating || updates.StartupPending() {
		b.mu.Unlock()
		return errBusy
	}
	if !b.config.CampusConsent && !approved {
		b.mu.Unlock()
		return errors.New("请先确认校内访问接入提示")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.cancel, b.startDone = cancel, done
	b.starting, b.enabled, b.state, b.problem = true, false, "preparing", ""
	b.authRequired, b.freshLogin = false, fresh
	b.follow, b.followTarget, b.followRetry = true, nil, time.Time{}
	b.mu.Unlock()
	b.publish()
	go b.startCampus(ctx, done, approved, waitClash)
	return nil
}

func (b *Backend) loadCampusSession() {
	raw, err := fsx.ReadBounded(filepath.Join(b.Dir, "campus-session.json"), 65536)
	if os.IsNotExist(err) {
		return
	}
	var a clash.CampusAttachment
	if err != nil || json.Unmarshal(raw, &a) != nil || a.Root == "" || a.ProviderPath == "" {
		b.state, b.problem = "error", "无法读取上次接入记录，请在诊断中检查 campus-session.json"
		return
	}
	b.campus = &a
	if a.Active {
		b.state, b.problem = "paused", "上次校内访问未正常停用；再次启用时会先恢复出口"
	}
}

// Caller serializes changes with clashBusy. A failed journal write is never ignored.
func (b *Backend) rememberCampus(a clash.CampusAttachment) error {
	b.mu.Lock()
	b.campus = &a
	b.mu.Unlock()
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := fsx.AtomicWrite(filepath.Join(b.Dir, "campus-session.json"), raw); err != nil {
		return err
	}
	b.mu.Lock()
	settings := b.config.Integration
	b.mu.Unlock()
	if a.RuntimeFile != "" && (settings.Mode == "" || settings.Mode == "auto") && settings.RuntimeFile != "" && settings.RuntimeFile != a.RuntimeFile {
		return b.saveConfig(func(c *appconf.Config) { c.Integration.RuntimeFile = a.RuntimeFile })
	}
	return nil
}

func (b *Backend) campusAttachment() *clash.CampusAttachment {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.campus == nil {
		return nil
	}
	a := *b.campus
	return &a
}

func (b *Backend) ownsCampus() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.campusOwned
}

func (b *Backend) startCampus(ctx context.Context, done chan struct{}, approved, waitClash bool) {
	var err error
	if waitClash {
		err = b.waitForStartupClash(ctx)
		if err == nil {
			// Preferences can change while the readiness probe is in flight.
			err = b.startupAllowed(ctx)
		}
		if err == nil {
			b.connectionProblem("preparing", "")
		}
	}
	if err == nil {
		err = b.prepareCampus(ctx, approved)
	}
	if err == nil {
		err = b.waitForCampus(ctx)
	}
	cleanCancel := err == context.Canceled
	cleanupComplete := false
	if err != nil {
		b.clashBusy.Lock()
		var detachErr error
		if b.ownsCampus() || b.runtime.Running() {
			_, detachErr = b.detachForStop(false)
		}
		b.clashBusy.Unlock()
		if detachErr == nil {
			if stopped, stopErr := b.runtime.Stop(8 * time.Second); stopErr != nil {
				err = errors.Join(err, stopErr)
				cleanCancel = false
			} else if !stopped {
				err = errors.Join(err, errors.New("VPN 尚未退出，可再次停用或强制结束"))
				cleanCancel = false
			} else {
				cleanupComplete = true
			}
		} else {
			message := "接入恢复未完成：%w"
			if b.runtime.Running() {
				message = "出口恢复失败，已保留 VPN 以免留下失效路由：%w"
			}
			err = errors.Join(err, fmt.Errorf(message, detachErr))
			cleanCancel = false
		}
	}
	running := b.runtime.Running()
	b.mu.Lock()
	b.starting = false
	if err != nil {
		b.enabled = false
		b.follow = false
		b.connectedAt = time.Time{}
		if cleanCancel && !running {
			b.state, b.problem = "stopped", ""
		} else if b.authRequired {
			b.state, b.problem = "auth-required", "登录已失效，请重新登录"
		} else {
			b.state, b.problem = "error", err.Error()
		}
	}
	b.cancel = nil
	if cleanupComplete {
		b.startDone = nil
		b.campusOwned = false
	}
	close(done)
	b.mu.Unlock()
	if err != nil && !cleanCancel {
		b.appendLog("启用校内访问失败：" + err.Error())
	}
	b.publish()
}

func (b *Backend) prepareCampus(ctx context.Context, approved bool) error {
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.proxyOnly() {
		return b.prepareProxy(ctx, approved)
	}
	if approved {
		if err := b.saveConfig(func(c *appconf.Config) { c.CampusConsent = true }); err != nil {
			return err
		}
	}
	// Recovery only happens after an explicit enable, never merely on opening the app.
	root, err := b.campusRoot()
	if err != nil {
		return err
	}
	if a := b.campusAttachment(); a != nil {
		if err := b.detachCampus(); err != nil {
			return err
		}
	}
	view := b.currentView()
	if view == nil {
		return errors.New("未找到 VPN 配置")
	}
	values, err := availableProxyPorts(view.Values)
	if err != nil {
		return err
	}
	// Validate without writes before creating a default config or imported copies.
	if _, err := b.conn.PrepareCampus(root, b.workspace.Target, values); err != nil {
		return err
	}
	for _, key := range []string{"socks_bind", "http_bind"} {
		if values[key] != view.Values[key] {
			b.appendLog("自动选择本地代理端口：" + key + "=" + text(values, key))
		}
	}
	saved, err := b.workspace.Save(values)
	if err != nil {
		return err
	}
	b.setView(saved)
	plan, err := b.conn.PrepareCampus(root, b.workspace.Target, nil)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backup, attachment, err := b.conn.ImportCampus(plan)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.campusOwned = true
	b.mu.Unlock()
	if err := b.rememberCampus(attachment); err != nil {
		return err
	}
	b.appendLog("Clash 接入备份：" + backup)
	b.mu.Lock()
	b.plan, b.rulesPath = &plan, plan.Rules
	b.mu.Unlock()
	// The integration normalizes only the GUI-owned runtime config.
	loaded, err := b.workspace.Load(plan.Config, true)
	if err != nil {
		return err
	}
	b.setView(loaded)
	b.refreshSaved()
	if err := b.saveConfig(func(c *appconf.Config) { c.ConfigPath, c.FduConfig = plan.Config, plan.Config }); err != nil {
		return err
	}
	b.emit(EventChanged, "settings")
	b.emit(EventChanged, "rules")
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.startVPN(plan.Config, loaded.Values)
}

func (b *Backend) waitForCampus(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var readySince time.Time
	var pendingTarget *clash.CampusTarget
	// Login has no artificial timeout; cancellation remains available throughout.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if !b.runtime.Running() {
			return errors.New("校园 VPN 已退出，请查看诊断")
		}
		if !b.runtime.Connected() {
			readySince = time.Time{}
			continue
		}
		if b.proxyOnly() {
			b.mu.Lock()
			b.enabled, b.state, b.problem, b.coverage = true, "connected", "", "设置了 FDU 本地代理的应用"
			b.connectedAt = time.Now()
			b.mu.Unlock()
			return nil
		}
		if readySince.IsZero() {
			readySince = time.Now()
		}
		b.mu.Lock()
		b.state = "enabling"
		b.mu.Unlock()
		b.publish()
		b.clashBusy.Lock()
		a := b.campusAttachment()
		if a == nil {
			b.clashBusy.Unlock()
			return errors.New("缺少接入记录")
		}
		if err := ctx.Err(); err != nil {
			b.clashBusy.Unlock()
			return err
		}
		var target clash.CampusTarget
		var targetErr error
		if b.conn.RuntimePath() != "" {
			_, targetErr = b.campusRoot()
		}
		if targetErr == nil {
			target, targetErr = b.conn.CurrentCampus(a.Root)
		}
		if targetErr != nil || (target.Mode != "global" && target.Mode != "rule") {
			b.clashBusy.Unlock()
			b.connectionProblem("enabling", "等待 Clash 的全局或规则模式就绪")
			readySince = time.Time{}
			continue
		}
		changed := target.ProfileUID != a.ProfileUID || target.Mode != a.ClashMode || !target.Installed
		if changed && (pendingTarget == nil || *pendingTarget != target) {
			pendingTarget = &target
			b.clashBusy.Unlock()
			continue
		}
		var next clash.CampusAttachment
		var err error
		if changed {
			next, err = b.conn.FollowCampus(*a)
		} else {
			next, err = b.conn.ActivateCampus(*a)
		}
		if err == nil || !reflect.DeepEqual(next, *a) {
			err = errors.Join(err, b.rememberCampus(next))
		}
		coverage, coverageErr := b.conn.CampusCoverage(a.Root, a.Rules)
		b.clashBusy.Unlock()
		if err != nil {
			if errors.Is(err, clash.ErrClashChanging) {
				readySince = time.Time{}
				continue
			}
			// The core may report connected just before writing its first rules acknowledgement.
			if errors.Is(err, clash.ErrCampusPending) && time.Since(readySince) < 15*time.Second {
				continue
			}
			return err
		}
		if coverageErr != nil {
			coverage = "已接入，暂时读不到 Clash TUN 状态"
		}
		b.mu.Lock()
		b.enabled, b.state, b.problem, b.coverage = true, "connected", "", coverage
		b.connectedAt = time.Now()
		b.mu.Unlock()
		b.emit(EventChanged, "clash")
		return nil
	}
}

func (b *Backend) detachCampus() error {
	a := b.campusAttachment()
	if a == nil || (!a.Active && !b.ownsCampus()) {
		return nil
	}
	next, err := b.conn.DeactivateCampus(*a)
	if err == nil || next.ProviderHash != a.ProviderHash || next.Active != a.Active {
		err = errors.Join(err, b.rememberCampus(next))
	}
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.enabled, b.campusOwned = false, false
	b.mu.Unlock()
	return nil
}

// A missing controller cannot confirm restoration. Keep the journal pending,
// but do not make the lifetime of our child depend on another application.
func (b *Backend) detachForStop(force bool) (bool, error) {
	if !force {
		if err := b.detachCampus(); err == nil {
			return false, nil
		} else if !errors.Is(err, clash.ErrControllerUnavailable) {
			return false, err
		}
	}
	a := b.campusAttachment()
	if a == nil || (!a.Active && !b.ownsCampus()) {
		return false, nil
	}
	a.Active = true
	if err := b.rememberCampus(*a); err != nil {
		return false, err
	}
	b.appendLog("Clash 接入尚未恢复，已保留记录；下次启用时重试。")
	return true, nil
}

func (b *Backend) disconnect(force bool) (bool, error) {
	b.mu.Lock()
	wanted := b.follow
	b.follow = false
	b.authRequired, b.freshLogin = false, false
	startedHere := b.startDone != nil
	b.mu.Unlock()
	if !startedHere && !b.runtime.Running() {
		// Merely opening or closing the GUI must not repair a previous session.
		return true, nil
	}
	b.setStopping(true)
	defer b.setStopping(false)
	b.mu.Lock()
	cancel, done, starting := b.cancel, b.startDone, b.starting
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if starting {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			return false, errors.New("正在取消接入操作，请稍后再停用")
		}
	}
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	owned := b.ownsCampus()
	if !owned && !b.runtime.Running() {
		b.mu.Lock()
		b.startDone = nil
		b.mu.Unlock()
		return true, nil
	}
	deferred := false
	if startedHere || owned {
		var err error
		deferred, err = b.detachForStop(force)
		if err != nil {
			b.mu.Lock()
			b.follow = wanted
			b.mu.Unlock()
			message := "恢复 Clash 接入失败，尚有配置待恢复："
			if b.runtime.Running() {
				message = "恢复 Clash 出口失败，VPN 暂未关闭："
			}
			b.connectionProblem("error", message+err.Error())
			return false, err
		}
	}
	var stopped bool
	var err error
	if force {
		err = b.runtime.Kill()
		stopped = err == nil
	} else {
		stopped, err = b.runtime.Stop(12 * time.Second)
	}
	if stopped && err == nil {
		b.mu.Lock()
		b.authRequired, b.freshLogin = false, false
		b.state, b.problem, b.coverage = "stopped", "", ""
		if deferred {
			b.problem = "Clash 接入待下次启用时恢复"
		}
		b.connectedAt = time.Time{}
		b.startDone = nil
		b.campusOwned = false
		b.mu.Unlock()
		b.emit(EventChanged, "clash")
		b.publish()
	}
	return stopped, err
}

func (b *Backend) forceStop() error {
	_, err := b.disconnect(true)
	return err
}

func (b *Backend) connectionProblem(state, problem string) {
	b.mu.Lock()
	changed := b.state != state || b.problem != problem
	b.enabled, b.state, b.problem = false, state, problem
	b.connectedAt = time.Time{}
	b.mu.Unlock()
	if changed {
		if problem != "" {
			b.appendLog(problem)
		}
		b.publish()
	}
}

// Observe user changes without racing the explicit enable/disable transaction.
func (b *Backend) refreshCampus() {
	b.mu.Lock()
	wanted, starting, stopping := b.follow, b.starting, b.stopping
	b.mu.Unlock()
	if !wanted || starting || stopping || !b.clashBusy.TryLock() {
		return
	}
	defer b.clashBusy.Unlock()
	if b.proxyOnly() {
		if !b.runtime.Running() {
			b.mu.Lock()
			b.follow, b.startDone = false, nil
			required := b.authRequired
			b.mu.Unlock()
			if required {
				b.waitingForVPN()
			} else {
				b.connectionProblem("paused", "本地校园代理已退出")
			}
		} else if !b.runtime.Connected() {
			b.waitingForVPN()
		} else {
			b.mu.Lock()
			b.enabled, b.state, b.problem = true, "connected", ""
			if b.connectedAt.IsZero() {
				b.connectedAt = time.Now()
			}
			b.mu.Unlock()
			b.publish()
		}
		return
	}
	a := b.campusAttachment()
	if a == nil {
		return
	}
	if !b.runtime.Running() {
		_, detachErr := b.detachForStop(false)
		if detachErr == nil {
			b.mu.Lock()
			b.follow, b.startDone = false, nil
			b.campusOwned = false
			b.mu.Unlock()
		}
		b.mu.Lock()
		required := b.authRequired
		b.mu.Unlock()
		if required {
			b.connectionProblem("auth-required", errors.Join(errors.New("登录已失效，请重新登录"), detachErr).Error())
		} else {
			b.connectionProblem("paused", errors.Join(errors.New("校园 VPN 已退出"), detachErr).Error())
		}
		return
	}
	if !b.runtime.Connected() {
		b.waitingForVPN()
		return
	}
	var target clash.CampusTarget
	var err error
	if b.conn.RuntimePath() != "" {
		_, err = b.campusRoot()
	}
	if err == nil {
		target, err = b.conn.CurrentCampus(a.Root)
	}
	if err != nil {
		b.connectionProblem("paused", "等待 Clash 就绪："+err.Error())
		return
	}
	if target.Mode != "global" && target.Mode != "rule" {
		b.connectionProblem("paused", "等待 Clash 返回全局或规则模式，届时自动恢复")
		return
	}
	reattach := target.ProfileUID != a.ProfileUID || target.Mode != a.ClashMode || !target.Installed
	b.mu.Lock()
	stable := b.followTarget != nil && *b.followTarget == target
	if reattach && !stable {
		b.followTarget, b.followRetry = &target, time.Time{}
	}
	retry := b.followRetry
	b.mu.Unlock()
	if reattach && !stable {
		b.connectionProblem("adapting", "")
		return
	}
	if (reattach || !a.Active) && time.Now().Before(retry) {
		return
	}
	next := *a
	switch {
	case reattach:
		b.connectionProblem("adapting", "")
		next, err = b.conn.FollowCampus(*a)
	default:
		if err = b.conn.CheckCampus(*a); err != nil {
			b.connectionProblem("paused", err.Error())
			return
		}
		if a.Active {
			next, err = b.conn.SyncCampus(*a)
		} else {
			next, err = b.conn.ActivateCampus(*a)
		}
	}
	if !reflect.DeepEqual(next, *a) {
		err = errors.Join(err, b.rememberCampus(next))
	}
	if err != nil && (reattach || !a.Active) {
		b.mu.Lock()
		if !errors.Is(err, clash.ErrCampusPending) && !errors.Is(err, clash.ErrClashChanging) {
			b.followRetry = time.Now().Add(15 * time.Second)
		}
		b.mu.Unlock()
		b.connectionProblem("paused", "等待自动适配："+err.Error())
		return
	}
	coverage, _ := b.conn.CampusCoverage(a.Root, a.Rules)
	b.mu.Lock()
	b.enabled, b.state, b.coverage = true, "connected", coverage
	b.followTarget, b.followRetry = nil, time.Time{}
	if b.connectedAt.IsZero() {
		b.connectedAt = time.Now()
	}
	if reattach {
		b.plan, b.rulesPath = nil, next.Rules
	}
	if err != nil {
		b.problem = "新规则尚未生效：" + err.Error()
	} else {
		b.problem = ""
	}
	b.mu.Unlock()
	if reattach {
		b.emit(EventChanged, "clash")
	}
	b.publish()
}
