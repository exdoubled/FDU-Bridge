package services

import (
	"context"
	"errors"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/core"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// CoreService connects, disconnects and answers login challenges for the owned core.
type CoreService struct{ b *Backend }

func NewCoreService(b *Backend) *CoreService { return &CoreService{b} }

// ServiceStartup loads settings and runs auto-connect before the page first asks for them.
func (s *CoreService) ServiceStartup(context.Context, application.ServiceOptions) error {
	s.b.Start(true)
	return nil
}

func (s *CoreService) Status() CoreStatus { return s.b.Status() }

func (s *CoreService) PreviewConnect() (ConnectPreview, error) { return s.b.previewConnect() }

func (s *CoreService) Connect(approved bool) error { return s.b.connect(approved) }

// Disconnect reports false when the core did not exit in time; the UI may then ForceStop.
func (s *CoreService) Disconnect() (bool, error) { return s.b.Disconnect() }

func (s *CoreService) ForceStop() error { return s.b.forceStop() }

// PendingChallenge lets a reloaded page resume an unanswered login prompt.
func (s *CoreService) PendingChallenge() map[string]any {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.b.challenge
}

func (s *CoreService) Respond(answer map[string]any) error {
	s.b.mu.Lock()
	s.b.challenge = nil
	s.b.mu.Unlock()
	return s.b.runtime.Respond(answer)
}

// RunTool runs one aTrust maintenance action with the saved configuration.
func (s *CoreService) RunTool(action string) error {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	s.b.mu.Lock()
	updating := s.b.updating
	s.b.mu.Unlock()
	if updating || updates.StartupPending() {
		return errors.New("正在安装更新")
	}
	if !core.Tools[action] {
		return errors.New("不支持的客户端操作")
	}
	status := s.b.Status()
	if status.Running || status.Starting || status.Stopping {
		return errors.New("请先断开 FDU 再执行工具")
	}
	view := s.b.currentView()
	if view == nil || view.Dirty {
		return errors.New("请先保存设置")
	}
	if text(view.Values, "protocol") != "atrust" {
		return errors.New("该工具仅适用于 aTrust")
	}
	return s.b.runtime.Start(s.b.workspace.Target, view.Values, action)
}

func (s *CoreService) Logs() []string {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return append([]string(nil), s.b.logs...)
}

func (s *CoreService) ClearLogs() {
	s.b.mu.Lock()
	s.b.logs = nil
	s.b.mu.Unlock()
}

// ExportLogs asks for a destination and writes the (already redacted) log; "" means cancelled.
func (s *CoreService) ExportLogs() (string, error) {
	path, err := s.b.App.Dialog.SaveFile().SetFilename("fdu-log.txt").AddFilter("日志", "*.txt").PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return s.exportLogs(path)
}

func (s *CoreService) exportLogs(path string) (string, error) {
	files, directories := s.b.exportPaths()
	target, err := fsx.ExportPath(path, files, directories)
	if err != nil {
		return "", err
	}
	return path, fsx.AtomicWrite(target, []byte(strings.Join(s.Logs(), "\r\n")+"\r\n"))
}
