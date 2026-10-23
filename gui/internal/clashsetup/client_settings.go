package clashsetup

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/knadh/koanf/v2"
	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/routing"
	"github.com/pelletier/go-toml/v2"
)

type SettingField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
	Kind  string `json:"kind"`
}
type SettingCopy struct {
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	Hash            string `json:"hash"`
	DestinationHash string `json:"destination_hash"`
	Remove          bool   `json:"remove"`
}
type SettingsReply struct {
	Values   map[string]any `json:"values"`
	Fields   []SettingField `json:"fields"`
	Copies   []SettingCopy  `json:"copies"`
	Content  string         `json:"content"`
	Hash     string         `json:"hash"`
	Adjusted bool           `json:"adjusted"`
}

// Labels live beside the core configuration, rather than a second GUI config model.
const settingsSchema = `登录|protocol|协议|protocol
登录|server_address|服务器|text
登录|server_port|端口|number
登录|auth_type|认证方式|auth
登录|login_domain|登录域|text
登录|username|用户名|text
登录|password|密码|secret
登录|client_data_file|登录状态文件|file
本地代理|socks_bind|SOCKS5 监听|text
本地代理|socks_user|SOCKS5 用户名|text
本地代理|socks_passwd|SOCKS5 密码|secret
本地代理|http_bind|HTTP 监听|text
本地代理|shadowsocks_url|Shadowsocks URL|secret
本地代理|dial_direct_proxy|普通模式上游代理|text
端口转发|port_forwarding|TCP / UDP 转发|forwarding
DNS|disable_remote_dns|禁用远端 DNS|bool
DNS|dns_ttl|缓存秒数|number
DNS|remote_dns_server|远端 DNS|text
DNS|secondary_dns_server|备用 DNS|text
DNS|dns_server_bind|本地 DNS 监听|text
DNS|local_dns_server|VPN 域名解析 DNS|text
DNS|custom_dns|自定义解析|dns
网络|bind_interface|底层网卡|text
网络|auto_detect_interface|自动选择底层网卡|bool
网络|tun_mode|FDU TUN|bool
网络|add_route|添加 VPN 路由|bool
网络|dns_hijack|DNS 劫持|bool
网络|fake_ip|Fake IP|bool
网络|tcp_tunnel_mode|仅使用 TCP 隧道|bool
网络|proxy_all|所有目标使用 VPN|bool
网络|disable_server_config|忽略服务端策略|bool
网络|disable_keep_alive|关闭保活|bool
网络|keep_alive_url|HTTP 保活地址|text
aTrust|phone|短信登录手机号|text
aTrust|cas_ticket|CAS 票据|secret
aTrust|oauth2_code|OAuth2 code|secret
aTrust|update_best_nodes_interval|线路更新间隔（秒）|number
aTrust|session_refresh_interval|会话刷新间隔（秒）|number
aTrust|sid|SID|secret
aTrust|device_id|设备 ID|secret
aTrust|sign_key|签名密钥|secret
aTrust|resource_file|资源文件|file
登录|totp_secret|TOTP 密钥|secret
EasyConnect|cert_file|P12 证书|file
EasyConnect|cert_password|证书密码|secret
EasyConnect|disable_zju_config|关闭浙大专用配置|bool
EasyConnect|skip_domain_resource|忽略域名资源|bool
EasyConnect|disable_multi_line|关闭自动选线|bool
EasyConnect|custom_proxy_domain|强制代理域名|list
EasyConnect|twf_id|twfID|secret
分流|routing.enabled|启用选择性分流|bool
分流|routing.upstream_proxy|Clash 专用上游|text
分流|routing.rules_file|规则文件|file
分流|routing.domains|精确域名|list
分流|routing.domain_suffixes|域名及子域名|list
分流|routing.ip_cidrs|IPv4 / 网段|list
调试|graph_code_file|验证码输出文件|file
调试|debug_dump|调试日志|bool
调试|debug_pcap_file|PCAP 输出文件|file
调试|debug_tls_log_file|TLS 密钥输出文件|file`

func settingFields() []SettingField {
	var fields []SettingField
	for _, line := range strings.Split(settingsSchema, "\n") {
		p := strings.Split(line, "|")
		fields = append(fields, SettingField{p[1], p[2], p[0], p[3]})
	}
	return fields
}

func settingsValue(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Struct:
		m := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			if key := v.Type().Field(i).Tag.Get("koanf"); key != "" {
				m[key] = settingsValue(v.Field(i))
			}
		}
		return m
	case reflect.Slice:
		a := []any{}
		for i := 0; i < v.Len(); i++ {
			a = append(a, settingsValue(v.Index(i)))
		}
		return a
	default:
		return v.Interface()
	}
}
func settingsDefaults() map[string]any {
	return settingsValue(reflect.ValueOf(configs.Default())).(map[string]any)
}
func mergeSettings(to, from map[string]any) {
	for key, value := range from {
		if child, ok := value.(map[string]any); ok {
			mergeSettings(mapping(to, key), child)
		} else {
			to[key] = value
		}
	}
}
func normalizeSettingNumbers(m map[string]any) {
	for key, value := range m {
		switch n := value.(type) {
		case float64:
			if n == float64(int64(n)) {
				m[key] = int64(n)
			}
		case map[string]any:
			normalizeSettingNumbers(n)
		case []any:
			for _, item := range n {
				if child, ok := item.(map[string]any); ok {
					normalizeSettingNumbers(child)
				}
			}
		}
	}
}
func within(directory, path string) bool {
	rel, err := filepath.Rel(directory, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func clientSettings(r Request) (SettingsReply, error) {
	reply := SettingsReply{Values: settingsDefaults(), Fields: settingFields(), Copies: []SettingCopy{}}
	if r.Operation == "settings-render" {
		mergeSettings(reply.Values, r.Values)
		normalizeSettingAliases(reply.Values)
		normalizeSettingNumbers(reply.Values)
		if err := validateClientSettings(reply.Values); err != nil {
			return reply, err
		}
		b, err := toml.Marshal(reply.Values)
		reply.Content = string(b)
		return reply, err
	}
	path, err := filepath.Abs(r.Config)
	if err != nil {
		return reply, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return reply, err
	}
	if len(b) > 1024*1024 {
		return reply, errors.New("客户端配置超过 1 MiB")
	}
	var loaded map[string]any
	if err = toml.Unmarshal(b, &loaded); err != nil {
		return reply, err
	}
	reply.Adjusted = normalizeSettingAliases(loaded)
	mergeSettings(reply.Values, loaded)
	reply.Hash = digest(b)
	base := filepath.Dir(path)
	directory, err := filepath.Abs(r.Directory)
	if err != nil {
		return reply, err
	}
	for _, key := range []string{"cert_file", "resource_file", "client_data_file", "graph_code_file", "debug_pcap_file", "debug_tls_log_file"} {
		old := text(reply.Values[key])
		if old == "" {
			continue
		}
		if !filepath.IsAbs(old) {
			old = filepath.Join(base, old)
		}
		if key == "cert_file" || key == "resource_file" || within(directory, old) {
			reply.Values[key] = old
			continue
		}
		name := map[string]string{"client_data_file": "client_data.json", "graph_code_file": "captcha.png", "debug_pcap_file": "capture.pcap", "debug_tls_log_file": "tls-keys.log"}[key]
		destination := filepath.Join(directory, name)
		reply.Values[key] = destination
		reply.Adjusted = true
		if key == "client_data_file" {
			data, e := os.ReadFile(old)
			if e != nil && !os.IsNotExist(e) {
				return reply, e
			}
			destHash := ""
			if existing, e := os.ReadFile(destination); e == nil {
				destHash = digest(existing)
			}
			hash := ""
			if data != nil {
				hash = digest(data)
			}
			reply.Copies = append(reply.Copies, SettingCopy{old, destination, hash, destHash, os.IsNotExist(e)})
		}
	}
	route := mapping(reply.Values, "routing")
	if old := text(route["rules_file"]); old != "" {
		if !filepath.IsAbs(old) {
			old = filepath.Join(base, old)
		}
		route["rules_file"] = old
		if !within(directory, old) {
			destination := filepath.Join(directory, "routing-rules.json")
			route["rules_file"] = destination
			reply.Adjusted = true
			if data, e := os.ReadFile(old); e == nil {
				destHash := ""
				if existing, e := os.ReadFile(destination); e == nil {
					destHash = digest(existing)
				}
				reply.Copies = append(reply.Copies, SettingCopy{old, destination, digest(data), destHash, false})
			} else if !os.IsNotExist(e) {
				return reply, fmt.Errorf("无法导入规则文件：%w", e)
			}
		}
	}
	return reply, nil
}

func normalizeSettingAliases(values map[string]any) bool {
	changed := false
	for old, current := range map[string]string{"disable_zju_dns": "disable_remote_dns", "zju_dns_server": "remote_dns_server"} {
		if value, ok := values[old]; ok {
			values[current] = value
			delete(values, old)
			changed = true
		}
	}
	return changed
}

func validateClientSettings(m map[string]any) error {
	k := koanf.New(".")
	for key, value := range m {
		if err := k.Set(key, value); err != nil {
			return err
		}
	}
	c := configs.Default()
	if err := k.Unmarshal("", &c); err != nil {
		return err
	}
	if c.Protocol != "atrust" && c.Protocol != "easyconnect" {
		return errors.New("请选择 aTrust 或 EasyConnect")
	}
	if c.ServerAddress == "" || strings.Contains(c.ServerAddress, "://") || c.ServerPort < 1 || c.ServerPort > 65535 {
		return errors.New("请填写 VPN 主机名和有效端口")
	}
	for _, address := range []string{c.SocksBind, c.HTTPBind, c.DNSServerBind} {
		if address != "" {
			if err := validateSettingAddress(address, true); err != nil {
				return err
			}
		}
	}
	if c.UpdateBestNodesInterval < 0 || c.SessionRefreshInterval < 0 {
		return errors.New("刷新间隔不能为负数")
	}
	for _, f := range c.PortForwardingList {
		if f.NetworkType != "tcp" && f.NetworkType != "udp" {
			return errors.New("端口转发类型必须为 tcp 或 udp")
		}
		if err := validateSettingAddress(f.BindAddress, true); err != nil {
			return err
		}
		if err := validateSettingAddress(f.RemoteAddress, false); err != nil {
			return err
		}
	}
	for _, dns := range c.CustomDNSList {
		if dns.HostName == "" || net.ParseIP(dns.IP) == nil {
			return errors.New("请填写有效的自定义 DNS 域名和 IP")
		}
	}
	return routing.Validate(c)
}
func validateSettingAddress(address string, local bool) error {
	host, raw, err := net.SplitHostPort(address)
	port, e := strconv.Atoi(raw)
	if err != nil || e != nil || port < 0 || port > 65535 || (!local && (host == "" || port == 0)) {
		return fmt.Errorf("地址需为 主机:端口：%s", address)
	}
	return nil
}

// ReadSettings loads a client configuration as a draft owned by directory.
func ReadSettings(config, directory string) (SettingsReply, error) {
	return clientSettings(Request{Operation: "settings-read", Config: config, Directory: directory})
}

// RenderSettings validates edited values and renders the complete TOML document.
func RenderSettings(values map[string]any) (SettingsReply, error) {
	return clientSettings(Request{Operation: "settings-render", Values: values})
}
