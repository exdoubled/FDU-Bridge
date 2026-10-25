package services

import (
	"context"
	"errors"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

// Startup probes are read-only: old recovery records belong to preparation.
func (b *Backend) waitForStartupClash(ctx context.Context) error {
	for {
		err := b.startupClashReady(ctx)
		if !errors.Is(err, clash.ErrControllerUnavailable) && !errors.Is(err, clash.ErrClientNotRunning) {
			return err
		}
		b.connectionProblem("preparing", "正在等待 Clash 启动，停用校内访问可取消")
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *Backend) startupClashReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	authorized := b.config.AutoConnect && b.config.CampusConsent && !b.updating && !b.stopping
	b.mu.Unlock()
	if !authorized || updates.StartupPending() {
		return context.Canceled
	}
	if b.runtime.ExternalRunning() {
		return errors.New("检测到其他 FDU 正在运行，已停止自动连接")
	}
	if b.proxyOnly() {
		return nil
	}
	b.clashBusy.Lock()
	defer b.clashBusy.Unlock()
	root, err := b.campusRoot()
	if err != nil {
		return err
	}
	_, err = b.conn.CurrentCampus(root)
	return err
}
