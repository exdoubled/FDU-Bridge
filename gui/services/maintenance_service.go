package services

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

// MaintenanceService exposes explicit, offline maintenance actions only.
type MaintenanceService struct{ b *Backend }

func NewMaintenanceService(b *Backend) *MaintenanceService { return &MaintenanceService{b} }

func (s *MaintenanceService) editable() error {
	if err := NewSettingsService(s.b).editable(); err != nil {
		return err
	}
	if s.b.runtime.Tool() != "" || updates.StartupPending() {
		return errBusy
	}
	if s.b.updater != nil {
		s.b.updater.mu.Lock()
		busy := s.b.updater.busy
		s.b.updater.mu.Unlock()
		if busy {
			return errBusy
		}
	}
	if view := s.b.currentView(); view != nil && view.Dirty {
		return errors.New("请先保存或放弃配置草稿")
	}
	return nil
}

// RemoveIntegration retains the journal on any failure, including client API errors.
func (s *MaintenanceService) RemoveIntegration() (string, error) {
	if !s.b.clashBusy.TryLock() {
		return "", errBusy
	}
	defer s.b.clashBusy.Unlock()
	if err := s.editable(); err != nil {
		return "", err
	}
	a := s.b.campusAttachment()
	if a == nil {
		return "", errors.New("没有可核验的 FDU 接入记录；未修改客户端")
	}
	root, err := s.b.campusRoot()
	if err != nil {
		return "", err
	}
	if root != a.Root {
		return "", errors.New("当前选择的客户端与接入记录不一致")
	}
	view := s.b.currentView()
	if view == nil {
		return "", errors.New("无法读取受管配置")
	}
	backup, err := s.b.conn.RemoveCampus(*a, view.Values)
	if err != nil {
		return "", err
	}
	if err = s.b.saveConfig(func(c *appconf.Config) { c.CampusConsent = false }); err != nil {
		return "", err
	}
	if err = os.Remove(filepath.Join(s.b.Dir, "campus-session.json")); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	s.b.mu.Lock()
	s.b.campus, s.b.plan, s.b.campusOwned = nil, nil, false
	s.b.mu.Unlock()
	s.b.emit(EventChanged, "clash")
	return backup, nil
}
