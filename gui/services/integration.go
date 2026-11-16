package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/clash"
	"github.com/mythologyli/zju-connect/gui/internal/settings"
	"gopkg.in/yaml.v3"
)

// discoverClients is the only process scan behind client selection; tests replace
// it so Clash clients running on the developer's machine cannot change results.
var discoverClients = clash.DiscoverClients

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

// SaveUsage changes only preferences; connection setup applies them after consent.
func (s *ClashService) SaveUsage(mode, routingMode string, tun bool) (appconf.Config, error) {
	s.b.clashBusy.Lock()
	defer s.b.clashBusy.Unlock()
	if err := NewSettingsService(s.b).editable(); err != nil {
		return appconf.Config{}, err
	}
	if s.b.runtime.ExternalRunning() {
		return appconf.Config{}, errors.New("请先关闭其他 FDU")
	}
	if mode != "clash" && mode != "proxy" {
		return appconf.Config{}, errors.New("未知使用方式")
	}
	if routingMode != "rule" && routingMode != "global" {
		return appconf.Config{}, errors.New("请选择规则或全局模式")
	}
	if (mode == "proxy") != s.b.proxyOnly() {
		if a := s.b.campusAttachment(); (a != nil && a.Active) || s.b.ownsCampus() {
			return appconf.Config{}, errors.New("上次 Clash 接入尚未恢复，请先启用后停用")
		}
	}
	err := s.b.saveConfig(func(c *appconf.Config) {
		if mode == "proxy" && c.Integration.Mode != "proxy" {
			c.ClashIntegration = c.Integration
			c.Integration = appconf.Integration{Mode: "proxy"}
			c.CampusConsent = false
		} else if mode == "clash" && c.Integration.Mode == "proxy" {
			c.Integration = c.ClashIntegration
			if c.Integration.Mode != "manual" {
				c.Integration.Mode = "auto"
			}
			c.CampusConsent = false
		}
		if c.StandaloneMode != routingMode || c.StandaloneTUN != tun {
			c.CampusConsent = false
		}
		c.StandaloneMode, c.StandaloneTUN = routingMode, tun
	})
	if err != nil {
		return appconf.Config{}, err
	}
	s.b.emit(EventChanged, "clash")
	return NewAppService(s.b).Config(), nil
}

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
	clients, err := discoverClients()
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
		target, err := clash.ResolveDiscoveredTarget(preferred, clients)
		if (err == nil && target.Kind == "runtime") || errors.Is(err, clash.ErrClientNotRunning) {
			for _, client := range clients {
				if client.Kind == "runtime" {
					return clash.ClientTarget{}, errors.New("已保存的运行配置不属于当前可确认的内核，请重新选择客户端或改用手动接入")
				}
			}
			return clash.ClientTarget{}, clash.ErrClientNotRunning
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
	return clash.ResolveDiscoveredTarget("", clients)
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
		result.Message = "应用使用本地代理；规则模式下校园规则走 FDU，其余直连。"
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
	saved, err := b.saveProxyConfig(ctx)
	if err != nil {
		return err
	}
	if err = b.saveConfig(func(c *appconf.Config) {
		c.CampusConsent = approved || c.CampusConsent
		c.ConfigPath, c.FduConfig = saved.Path, saved.Path
	}); err != nil {
		return err
	}
	b.mu.Lock()
	b.campus, b.plan = nil, nil
	b.mu.Unlock()
	if err = os.Remove(filepath.Join(b.Dir, "campus-session.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	b.emit(EventChanged, "settings")
	return b.startVPN(saved.Path, saved.Values)
}

func (b *Backend) saveProxyConfig(ctx context.Context) (settings.View, error) {
	if a := b.campusAttachment(); (a != nil && a.Active) || b.ownsCampus() {
		return settings.View{}, errors.New("仍有未恢复的客户端接入，请先恢复原接入")
	}
	view := b.currentView()
	if view == nil {
		return settings.View{}, errors.New("未找到 VPN 配置")
	}
	// Round-trip the draft so connection defaults never mutate an unsaved settings view.
	raw, err := yaml.Marshal(view.Values)
	if err != nil {
		return settings.View{}, err
	}
	var values map[string]any
	if err = yaml.Unmarshal(raw, &values); err != nil {
		return settings.View{}, err
	}
	if values["socks_bind"] == "" && values["http_bind"] == "" {
		values["socks_bind"], values["http_bind"] = "127.0.0.1:1080", "127.0.0.1:1081"
	}
	for _, key := range []string{"socks_bind", "http_bind"} {
		if address := text(values, key); address != "" && !clash.LocalProxyAddress(address) {
			return settings.View{}, errors.New("本地代理需监听本机 IP 和有效端口")
		}
	}
	route, _ := values["routing"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		values["routing"] = route
	}
	b.mu.Lock()
	mode, tun := b.config.StandaloneMode, b.config.StandaloneTUN
	b.mu.Unlock()
	if mode == "" {
		mode = "rule"
	}
	rulesService := NewRulesService(b)
	ruleView, err := rulesService.Load()
	if err != nil {
		return settings.View{}, err
	}
	route["enabled"], route["upstream_proxy"], route["mode"], route["rules_file"] = true, "direct", mode, ruleView.Path
	values["tun_mode"], values["add_route"], values["dns_hijack"], values["fake_ip"] = tun, false, false, false
	if text(values, "bind_interface") == "" {
		values["auto_detect_interface"] = true
	}
	if err = ctx.Err(); err != nil {
		return settings.View{}, err
	}
	saved, err := b.workspace.Save(values)
	if err != nil {
		return settings.View{}, err
	}
	b.setView(saved)
	b.refreshSaved()
	// Save may materialize imported rules; never overwrite those with the draft seed.
	store, err := rulesService.store()
	if err != nil {
		return settings.View{}, err
	}
	snapshot, err := store.Load()
	if err != nil {
		return settings.View{}, err
	}
	if snapshot.Missing {
		if _, err = store.Save(ruleView.Snapshot.Document, ""); err != nil {
			return settings.View{}, err
		}
	}
	return saved, nil
}
