package services

import (
	"errors"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
)

// SettingsService edits the GUI-owned client configuration.
type SettingsService struct{ b *Backend }

func NewSettingsService(b *Backend) *SettingsService { return &SettingsService{b} }

func (s *SettingsService) editable() error {
	s.b.mu.Lock()
	updating := s.b.updating
	s.b.mu.Unlock()
	if updating {
		return errors.New("正在安装更新")
	}
	status := s.b.Status()
	if status.Running || status.Starting || status.Stopping {
		return errEditingLocked
	}
	return nil
}

// Current returns the draft the page should show; nil means no config or example was found.
func (s *SettingsService) Current() *settings.View { return s.b.currentView() }

// Reload discards the draft and reads the saved config again.
func (s *SettingsService) Reload() (settings.View, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := s.editable(); err != nil {
		return settings.View{}, err
	}
	view, err := s.b.workspace.Load(s.b.workspace.Target, true)
	if err != nil {
		return settings.View{}, err
	}
	s.b.setView(view)
	return view, nil
}

// Import reads another TOML file as a draft; it is not saved until Save.
func (s *SettingsService) Import() (*settings.View, error) {
	if err := s.editable(); err != nil {
		return nil, err
	}
	path, err := s.b.App.Dialog.OpenFile().SetTitle("导入 FDU 配置").AddFilter("FDU 配置", "*.toml;*.example").AddFilter("所有文件", "*.*").PromptForSingleSelection()
	if err != nil || path == "" {
		return nil, err
	}
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := s.editable(); err != nil {
		return nil, err
	}
	view, err := s.b.workspace.Load(path, false)
	if err != nil {
		return nil, err
	}
	s.b.setView(view)
	return &view, nil
}

func (s *SettingsService) Save(values map[string]any) (settings.View, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := s.editable(); err != nil {
		return settings.View{}, err
	}
	if values == nil {
		return settings.View{}, errors.New("没有可保存的设置")
	}
	view, err := s.b.workspace.Save(values)
	if err != nil {
		return settings.View{}, err
	}
	s.b.setView(view)
	s.b.refreshSaved()
	// Clash integration follows the config just saved, as the old manager did.
	_ = s.b.saveConfig(func(c *appconf.Config) { c.ConfigPath, c.FduConfig = view.Path, view.Path })
	s.b.publish()
	s.b.emit(EventChanged, "settings")
	return view, nil
}
