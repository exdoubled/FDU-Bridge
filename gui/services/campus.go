package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

type ConnectPreview struct {
	NeedsConsent bool   `json:"needsConsent"`
	Message      string `json:"message"`
	Profile      string `json:"profile"`
}

func (b *Backend) campusRoot() string {
	b.mu.Lock()
	root := b.config.ClashDir
	b.mu.Unlock()
	if root == "" {
		root = appconf.DefaultClashDir()
	}
	return root
}

func (b *Backend) previewConnect() (ConnectPreview, error) {
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	view := b.currentView()
	if view == nil {
		return ConnectPreview{}, errors.New("未找到默认 VPN 配置，请在高级设置中导入")
	}
	plan, err := b.conn.PrepareCampus(b.campusRoot(), b.workspace.Target, view.Values)
	if err != nil {
		return ConnectPreview{}, err
	}
	b.mu.Lock()
	consent := b.config.CampusConsent
	b.mu.Unlock()
	return ConnectPreview{NeedsConsent: !consent, Profile: plan.Profile,
		Message: "将备份并为当前 Clash 订阅添加校内访问，保留现有全局/规则模式和外网节点。停用时恢复外网出口。\n本程序不创建 FDU 虚拟网卡，不自动开启 Clash TUN，也不修改系统代理及绕过列表；仅接管进入 Clash 的流量。"}, nil
}

// connect returns before browser authentication so the same switch can cancel it.
func (b *Backend) connect(approved bool) error {
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	if b.runtime.Running() || b.runtime.ExternalRunning() {
		return errors.New("已有 FDU 在运行，请先关闭后再启用")
	}
	b.mu.Lock()
	if b.starting || b.stopping {
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
	b.mu.Unlock()
	b.publish()
	go b.startCampus(ctx, done, approved)
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
	return fsx.AtomicWrite(filepath.Join(b.Dir, "campus-session.json"), raw)
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

func (b *Backend) startCampus(ctx context.Context, done chan struct{}, approved bool) {
	err := b.prepareCampus(ctx, approved)
	if err == nil {
		err = b.waitForCampus(ctx)
	}
	cleanCancel := err == context.Canceled
	cleanupComplete := false
	if err != nil {
		b.clashBusy.Lock()
		detachErr := b.detachCampus()
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
			err = errors.Join(err, fmt.Errorf("出口恢复失败，已保留 VPN 以免留下失效路由：%w", detachErr))
			cleanCancel = false
		}
	}
	running := b.runtime.Running()
	b.mu.Lock()
	b.starting = false
	if err != nil {
		b.enabled = false
		b.connectedAt = time.Time{}
		if cleanCancel && !running {
			b.state, b.problem = "stopped", ""
		} else {
			b.state, b.problem = "error", err.Error()
		}
	}
	b.cancel = nil
	if cleanupComplete {
		b.startDone = nil
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
	if approved {
		if err := b.saveConfig(func(c *appconf.Config) { c.CampusConsent = true }); err != nil {
			return err
		}
	}
	// Recovery only happens after an explicit enable, never merely on opening the app.
	if a := b.campusAttachment(); a != nil {
		if err := b.detachCampus(); err != nil {
			return err
		}
	}
	view := b.currentView()
	if view == nil {
		return errors.New("未找到 VPN 配置")
	}
	root := b.campusRoot()
	// Validate without writes before creating a default config or imported copies.
	if _, err := b.conn.PrepareCampus(root, b.workspace.Target, view.Values); err != nil {
		return err
	}
	saved, err := b.workspace.Save(view.Values)
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
	return b.runtime.Start(plan.Config, loaded.Values, "")
}

func (b *Backend) waitForCampus(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var readySince time.Time
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
		next, err := b.conn.ActivateCampus(*a)
		if err == nil || next.ProviderHash != a.ProviderHash || next.Active != a.Active {
			err = errors.Join(err, b.rememberCampus(next))
		}
		coverage, coverageErr := b.conn.CampusCoverage(a.Root)
		b.clashBusy.Unlock()
		if err != nil {
			// The core may report connected just before writing its first rules acknowledgement.
			if errors.Is(err, clash.ErrCampusPending) && time.Since(readySince) < 15*time.Second {
				continue
			}
			return err
		}
		if coverageErr != nil {
			coverage = "已接入；暂时无法确认 Clash TUN 覆盖范围"
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
	if a == nil {
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
	b.enabled = false
	b.mu.Unlock()
	return nil
}

func (b *Backend) disconnect(force bool) (bool, error) {
	b.mu.Lock()
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
	if startedHere {
		if err := b.detachCampus(); err != nil {
			b.connectionProblem("error", "恢复 Clash 出口失败，VPN 暂未关闭："+err.Error())
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
		b.state, b.problem, b.coverage = "stopped", "", ""
		b.connectedAt = time.Time{}
		b.startDone = nil
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
	b.enabled, b.state, b.problem = false, state, problem
	b.connectedAt = time.Time{}
	b.mu.Unlock()
	b.appendLog(problem)
	b.publish()
}

// Observe user changes without racing the explicit enable/disable transaction.
func (b *Backend) refreshCampus() {
	b.mu.Lock()
	enabled, starting, stopping := b.enabled, b.starting, b.stopping
	b.mu.Unlock()
	if !enabled || starting || stopping || !b.clashBusy.TryLock() {
		return
	}
	defer b.clashBusy.Unlock()
	a := b.campusAttachment()
	if a == nil {
		return
	}
	err := b.conn.CheckCampus(*a)
	if err == nil && !b.runtime.Connected() {
		err = errors.New("校园 VPN 连接已中断")
	}
	if err != nil {
		detachErr := b.detachCampus()
		if detachErr == nil {
			_, _ = b.runtime.Stop(8 * time.Second)
		}
		b.connectionProblem("paused", errors.Join(err, detachErr).Error())
		return
	}
	next, err := b.conn.SyncCampus(*a)
	if next.ProviderHash != a.ProviderHash || next.RulesHash != a.RulesHash {
		err = errors.Join(err, b.rememberCampus(next))
	}
	coverage, _ := b.conn.CampusCoverage(a.Root)
	b.mu.Lock()
	b.coverage = coverage
	if err != nil {
		b.problem = "新规则尚未生效：" + err.Error()
	} else {
		b.problem = ""
	}
	b.mu.Unlock()
	b.publish()
}
