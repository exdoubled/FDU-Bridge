package services

import (
	"errors"

	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

func (b *Backend) startVPN(path string, values map[string]any) error {
	b.mu.Lock()
	fresh := b.freshLogin
	b.mu.Unlock()
	if fresh {
		return b.runtime.StartFresh(path, values)
	}
	return b.runtime.Start(path, values, "")
}

// Relogin replaces only this GUI's VPN authentication, leaving user data intact.
// Selective mode retains its public forwarding listeners during authentication.
func (s *CoreService) Relogin() error {
	b := s.b
	b.clashBusy.Lock()
	b.mu.Lock()
	blocked := b.stopping || b.updating || updates.StartupPending()
	protocol := text(b.saved, "protocol")
	routing, _ := b.saved["routing"].(map[string]any)
	selective := routing["enabled"] == true
	b.mu.Unlock()
	if blocked || b.runtime.Tool() != "" {
		b.clashBusy.Unlock()
		return errBusy
	}
	if protocol != "atrust" {
		b.clashBusy.Unlock()
		return errors.New("重新登录仅适用于 aTrust")
	}
	if b.runtime.ExternalRunning() {
		b.clashBusy.Unlock()
		return errors.New("其他 FDU 正在运行，本窗口不会接管或关闭它")
	}
	if b.runtime.Running() && selective {
		err := b.runtime.Respond(map[string]any{"operation": "relogin"})
		b.clashBusy.Unlock()
		return err
	}
	b.clashBusy.Unlock()
	if stopped, err := b.Disconnect(); err != nil {
		return err
	} else if !stopped {
		return errors.New("请先完成当前连接的关闭，再重新登录")
	}
	return b.connectWithLogin(false, true)
}

func (b *Backend) waitingForVPN() {
	b.mu.Lock()
	state, required := b.state, b.authRequired
	b.mu.Unlock()
	if required {
		b.connectionProblem("auth-required", "登录已失效，请重新登录")
		return
	}
	if state == "authentication" || state == "campus-unavailable" {
		return
	}
	b.connectionProblem("reconnecting", "正在等待网络恢复并自动重连")
}
