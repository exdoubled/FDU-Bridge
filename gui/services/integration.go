package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"gopkg.in/yaml.v3"
)

func (b *Backend) proxyOnly() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config.Integration.Mode == "proxy"
}

func (s *ClashService) Integration() appconf.Integration {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	settings := s.b.config.Integration
	if settings.Mode == "" {
		settings.Mode = "auto"
	}
	return settings
}

func (s *ClashService) Clients() ([]clash.ClientTarget, error) { return clash.DiscoverClients() }

// A running session follows its original client, never whichever app appears first.
func followingClient(previous clash.ClientTarget, clients []clash.ClientTarget) (clash.ClientTarget, error) {
	var matches []clash.ClientTarget
	for _, target := range clients {
		if target.Kind != previous.Kind {
			continue
		}
		match := filepath.Clean(target.RuntimeFile) == filepath.Clean(previous.RuntimeFile)
		if previous.Executable != "" {
			match = strings.EqualFold(filepath.Clean(target.Executable), filepath.Clean(previous.Executable))
		}
		if match {
			matches = append(matches, target)
		}
	}
	if len(matches) != 1 {
		return clash.ClientTarget{}, errors.New("无法唯一确认原代理客户端的当前运行配置，已暂停自动接入")
	}
	return matches[0], nil
}

func (b *Backend) automaticTarget(preferred, oldVerge string) (clash.ClientTarget, error) {
	clients, err := clash.DiscoverClients()
	if err != nil {
		return clash.ClientTarget{}, err
	}
	var matching []clash.ClientTarget
	for _, target := range clients {
		path := target.RuntimeFile
		if target.Kind == "verge" {
			path = target.Root
		}
		if (preferred != "" && strings.EqualFold(filepath.Clean(path), filepath.Clean(preferred))) || (preferred == "" && target.Kind == "verge" && strings.EqualFold(filepath.Clean(target.Root), filepath.Clean(oldVerge))) {
			matching = append(matching, target)
		}
	}
	if len(matching) > 1 {
		return clash.ClientTarget{}, errors.New("同一配置对应多个控制接口，请使用手动接入指定接口")
	}
	if len(matching) == 1 {
		return matching[0], nil
	}
	if preferred != "" {
		target, err := clash.ResolveClientTarget(preferred)
		if err == nil && target.Kind == "runtime" {
			return clash.ClientTarget{}, errors.New("已保存的运行配置不属于当前可确认的内核，请重新选择客户端或改用手动接入")
		}
		return target, err
	}
	if len(clients) == 1 {
		return clients[0], nil
	}
	if len(clients) > 1 {
		return clash.ClientTarget{}, errors.New("检测到多个代理客户端，请在设置中选择要接入的一项")
	}
	if selected, err := appconf.ResolveClashDir(oldVerge); err == nil {
		return clash.ClientTarget{Kind: "verge", Root: selected, Name: "Clash Verge Rev"}, nil
	}
	return clash.ResolveClientTarget("")
}

// Saving a binding never installs anything into the selected client.
func (s *ClashService) SaveIntegration(mode, file, controller, secret string) (appconf.Integration, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := NewSettingsService(s.b).editable(); err != nil {
		return appconf.Integration{}, err
	}
	settings := appconf.Integration{Mode: mode, RuntimeFile: file, Controller: controller, Secret: secret}
	switch mode {
	case "auto":
		settings.Controller, settings.Secret = "", ""
		if file != "" {
			if _, err := s.b.automaticTarget(file, ""); err != nil {
				return settings, err
			}
		}
	case "manual":
		if file == "" {
			return settings, errors.New("请选择客户端实际使用的运行配置 YAML")
		}
		target, err := clash.ManualTarget(file, controller, secret)
		if err != nil {
			return settings, err
		}
		if target.RuntimeFile == "" {
			return settings, errors.New("手动接入请选择运行配置文件，配置目录可在自动接入中选择")
		}
		settings.RuntimeFile = target.RuntimeFile
		if controller != "" {
			if _, err := clash.ParseController(controller, secret); err != nil {
				return settings, err
			}
		}
	case "proxy":
		settings.RuntimeFile, settings.Controller, settings.Secret = "", "", ""
	default:
		return settings, errors.New("未知接入方式")
	}
	if settings.RuntimeFile != "" {
		settings.RuntimeFile, _ = filepath.Abs(settings.RuntimeFile)
	}
	old := s.Integration()
	if old != settings {
		if a := s.b.campusAttachment(); (a != nil && a.Active) || s.b.ownsCampus() {
			return settings, errors.New("上次接入尚未恢复，请先启用后停用，再更换客户端")
		}
	}
	if err := s.b.saveConfig(func(c *appconf.Config) {
		if c.Integration != settings {
			c.CampusConsent = false
		}
		c.Integration = settings
	}); err != nil {
		return settings, err
	}
	s.b.emit(EventChanged, "clash")
	return settings, nil
}

type IntegrationExport struct {
	Script  string `json:"script"`
	YAML    string `json:"yaml"`
	Socks   string `json:"socks"`
	HTTP    string `json:"http"`
	Message string `json:"message"`
}

func (s *ClashService) ExportIntegration() (IntegrationExport, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	view := s.b.currentView()
	if view == nil {
		return IntegrationExport{}, errors.New("未找到 VPN 配置")
	}
	result := IntegrationExport{Socks: text(view.Values, "socks_bind"), HTTP: text(view.Values, "http_bind")}
	if s.b.proxyOnly() {
		result.Message = "在外部客户端添加本地代理并设置分流；此方式不自动切换节点或恢复出口。"
		result.YAML = "SOCKS5: " + result.Socks + "\nHTTP: " + result.HTTP + "\n"
		return result, nil
	}
	_, plan := s.inspect()
	if plan == nil {
		return result, errors.New("请先保存接入设置并检查连接，再导出扩展")
	}
	script, fragment, err := s.b.conn.ExportPatch(*plan)
	if err != nil {
		return result, err
	}
	result.Script, result.YAML = script, fragment
	result.Message = "本窗口手动接入无需安装脚本。导出的 JS 供支持 main(config) 的扩展使用；YAML 为配置片段。"
	return result, nil
}

func (b *Backend) prepareProxy(ctx context.Context, approved bool) error {
	if a := b.campusAttachment(); (a != nil && a.Active) || b.ownsCampus() {
		return errors.New("仍有未恢复的客户端接入，请先恢复原接入")
	}
	view := b.currentView()
	if view == nil {
		return errors.New("未找到 VPN 配置")
	}
	// Round-trip the draft so connection defaults never mutate an unsaved settings view.
	raw, err := yaml.Marshal(view.Values)
	if err != nil {
		return err
	}
	var values map[string]any
	if err = yaml.Unmarshal(raw, &values); err != nil {
		return err
	}
	if values["socks_bind"] == "" && values["http_bind"] == "" {
		values["socks_bind"], values["http_bind"] = "127.0.0.1:1080", "127.0.0.1:1081"
	}
	for _, key := range []string{"socks_bind", "http_bind"} {
		if address := text(values, key); address != "" && !clash.LocalProxyAddress(address) {
			return errors.New("本地代理需监听本机 IP 和有效端口")
		}
	}
	route, _ := values["routing"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		values["routing"] = route
	}
	route["enabled"] = false
	values["tun_mode"], values["add_route"], values["dns_hijack"], values["fake_ip"] = false, false, false, false
	if err = ctx.Err(); err != nil {
		return err
	}
	saved, err := b.workspace.Save(values)
	if err != nil {
		return err
	}
	b.setView(saved)
	b.refreshSaved()
	if err = b.saveConfig(func(c *appconf.Config) {
		c.CampusConsent = approved || c.CampusConsent
		c.ConfigPath, c.FduConfig = saved.Path, saved.Path
	}); err != nil {
		return err
	}
	b.mu.Lock()
	b.campus, b.plan = nil, nil
	b.mu.Unlock()
	// A stale inactive journal must not be loaded as an owned session on next launch.
	if err = os.Remove(filepath.Join(b.Dir, "campus-session.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	b.emit(EventChanged, "settings")
	return b.startVPN(saved.Path, saved.Values)
}
