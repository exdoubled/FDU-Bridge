package services

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

// MaintenanceService exposes explicit, offline maintenance actions only.
type MaintenanceService struct {
	b         *Backend
	migration *settings.Migration // guarded by clashBusy, never persisted
}

func NewMaintenanceService(b *Backend) *MaintenanceService { return &MaintenanceService{b: b} }

func (s *MaintenanceService) editable(migration bool) error {
	if err := NewSettingsService(s.b).editable(); err != nil {
		return err
	}
	if updates.StartupPending() {
		return errBusy
	}
	if s.b.Status().External || s.b.runtime.ExternalRunning() {
		return errors.New("检测到外部 FDU，请先退出后再维护配置")
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
		initialExample := false
		if migration && !view.Saved {
			for _, path := range []string{filepath.Join(s.b.Dir, "config.toml.example"), filepath.Join(s.b.exeDir, "config.toml.example"), filepath.Join(s.b.exeDir, "..", "Resources", "config.toml.example")} {
				initialExample = initialExample || strings.EqualFold(filepath.Clean(path), filepath.Clean(view.Source))
			}
		}
		if !initialExample {
			return errors.New("请先保存或放弃配置草稿")
		}
	}
	return nil
}

// RemoveIntegration retains the journal on any failure, including client API errors.
func (s *MaintenanceService) RemoveIntegration() (string, error) {
	if !s.b.clashBusy.TryLock() {
		return "", errBusy
	}
	defer s.b.clashBusy.Unlock()
	if err := s.editable(false); err != nil {
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
	s.b.mu.Lock()
	consent := s.b.config.CampusConsent
	s.b.mu.Unlock()
	backup, err := s.b.conn.RemoveCampus(*a, view.Values, func() error {
		if err := s.b.saveConfig(func(c *appconf.Config) { c.CampusConsent = false }); err != nil {
			return err
		}
		err := os.Remove(filepath.Join(s.b.Dir, "campus-session.json"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			err = errors.Join(err, s.b.saveConfig(func(c *appconf.Config) { c.CampusConsent = consent }))
		}
		return err
	})
	if err != nil {
		return "", err
	}
	s.b.mu.Lock()
	s.b.campus, s.b.plan, s.b.campusOwned = nil, nil, false
	s.b.mu.Unlock()
	s.b.emit(EventChanged, "clash")
	return backup, nil
}

type MigrationPreview struct {
	Directory string   `json:"directory"`
	Files     []string `json:"files"`
	Conflicts []string `json:"conflicts"`
}

// PreviewMigration opens a directory picker only after the explicit UI action.
func (s *MaintenanceService) PreviewMigration() (*MigrationPreview, error) {
	if err := s.editable(true); err != nil {
		return nil, err
	}
	directory, err := s.b.App.Dialog.OpenFile().SetTitle("选择旧 GUI 配置目录").CanChooseDirectories(true).CanChooseFiles(false).PromptForSingleSelection()
	if err != nil || directory == "" {
		return nil, err
	}
	if !s.b.clashBusy.TryLock() {
		return nil, errBusy
	}
	defer s.b.clashBusy.Unlock()
	return s.previewDirectory(directory)
}

func (s *MaintenanceService) previewDirectory(directory string) (*MigrationPreview, error) {
	s.migration = nil
	if err := s.editable(true); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(s.b.Dir, "campus-session.json")); !os.IsNotExist(err) {
		return nil, errors.New("请先移除当前目录的 FDU 接入并完成恢复，再迁移旧数据")
	}
	s.b.mu.Lock()
	autoStart := s.b.config.AutoStart
	s.b.mu.Unlock()
	p, err := settings.PreviewMigration(directory, s.b.Dir, autoStart)
	if err != nil {
		return nil, err
	}
	s.migration = p
	return &MigrationPreview{p.Directory, p.Files, p.Conflicts}, nil
}

func (s *MaintenanceService) Migrate() (settings.View, error) {
	if !s.b.clashBusy.TryLock() {
		return settings.View{}, errBusy
	}
	defer s.b.clashBusy.Unlock()
	if err := s.editable(true); err != nil {
		return settings.View{}, err
	}
	p := s.migration
	s.migration = nil
	if p == nil {
		return settings.View{}, errors.New("请先选择并预览旧 GUI 目录")
	}
	if _, err := os.Lstat(filepath.Join(s.b.Dir, "campus-session.json")); !os.IsNotExist(err) {
		return settings.View{}, errors.New("当前目录存在 Clash 接入恢复记录，未迁移")
	}
	s.b.prefsBusy.Lock()
	defer s.b.prefsBusy.Unlock()
	view, prefs, err := p.Apply()
	if err != nil {
		return settings.View{}, err
	}
	workspace, err := settings.New(s.b.Dir)
	if err != nil {
		return settings.View{}, err
	}
	view, err = workspace.Load(view.Path, true)
	if err != nil {
		return settings.View{}, err
	}
	s.b.mu.Lock()
	s.b.config = prefs
	s.b.workspace = workspace
	s.b.plan = nil
	s.b.mu.Unlock()
	s.b.setView(view)
	s.b.refreshSaved()
	s.b.publish()
	s.b.emit(EventChanged, "settings")
	return view, nil
}
