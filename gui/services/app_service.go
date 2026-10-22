package services

import (
	"errors"
	"net/url"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
)

// AppService covers preferences, native dialogs and the window lifecycle.
type AppService struct{ b *Backend }

func NewAppService(b *Backend) *AppService { return &AppService{b: b} }

func (s *AppService) Config() appconf.Config {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.b.config
}

// SavePreferences updates only the user-facing preferences, never the managed paths.
func (s *AppService) SavePreferences(theme string, autoStart, autoConnect, closeToTray bool) (appconf.Config, error) {
	err := s.b.saveConfig(func(c *appconf.Config) {
		c.Theme, c.AutoStart, c.AutoConnect, c.CloseToTray = theme, autoStart, autoConnect, closeToTray
	})
	return s.Config(), err
}

func (s *AppService) PickFolder(title, initial string) (string, error) {
	return s.b.App.Dialog.OpenFile().SetTitle(title).SetDirectory(initial).CanChooseDirectories(true).CanChooseFiles(false).PromptForSingleSelection()
}

func (s *AppService) PickConfig(title string) (string, error) {
	return s.b.App.Dialog.OpenFile().SetTitle(title).AddFilter("FDU TOML 配置", "*.toml;*.example").AddFilter("所有文件", "*.*").PromptForSingleSelection()
}

func (s *AppService) PickRuntimeConfig() (string, error) {
	return s.b.App.Dialog.OpenFile().SetTitle("选择客户端实际使用的运行配置").AddFilter("Mihomo 运行配置", "*.yaml;*.yml").PromptForSingleSelection()
}

// OpenURL only opens web pages, so a challenge payload cannot launch local programs.
func (s *AppService) OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("登录地址无效")
	}
	return s.b.App.Browser.OpenURL(u.String())
}

func (s *AppService) HideWindow() {
	if s.b.Window != nil {
		s.b.Window.Hide()
	}
}

// QuitApp stops the owned core first; it returns false if the core ignored the stop request.
func (s *AppService) QuitApp(force bool) (bool, error) {
	if s.b.Quit == nil {
		return false, errors.New("退出不可用")
	}
	return s.b.Quit(force)
}
