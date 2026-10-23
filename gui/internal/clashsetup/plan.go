// Package clashsetup only prepares changes. Callers own explicit application and rollback.
package clashsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mythologyli/zju-connect/routing"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type Request struct {
	Operation        string         `json:"operation"`
	Values           map[string]any `json:"values"`
	Campus           bool           `json:"campus"`
	CampusURL        string         `json:"-"`
	ClashMode        string         `json:"clash_mode"`
	Outside          string         `json:"outside"`
	Mode             string         `json:"mode"`
	Root             string         `json:"root"`
	RuntimeFile      string         `json:"runtime_file,omitempty"`
	RuntimeOverrides map[string]any `json:"-"`
	Config           string         `json:"config"`
	Directory        string         `json:"directory"`
	OccupiedPorts    []int          `json:"occupied_ports"`
}

type Change struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Content string `json:"content"`
}

type Plan struct {
	Campus        bool              `json:"campus"`
	ClashMode     string            `json:"clash_mode"`
	ProviderPath  string            `json:"provider_path"`
	LegacyFront   bool              `json:"legacy_front"`
	Mode          string            `json:"mode"`
	Profile       string            `json:"profile"`
	ProfileUID    string            `json:"profile_uid"`
	RuntimeFile   string            `json:"runtime_file,omitempty"`
	RuntimeHash   string            `json:"runtime_hash,omitempty"`
	Config        string            `json:"config"`
	Rules         string            `json:"rules"`
	SocksPort     int               `json:"socks_port"`
	HTTPPort      int               `json:"http_port"`
	UpstreamPort  int               `json:"upstream_port"`
	MixedPort     int               `json:"mixed_port"`
	SystemProxy   bool              `json:"system_proxy"`
	ProxyGuard    bool              `json:"proxy_guard"`
	NeedsFduStart bool              `json:"needs_fdu_start"`
	UnsafeNodes   []string          `json:"unsafe_nodes"`
	Inputs        map[string]string `json:"inputs"`
	Files         []Change          `json:"files"`
	Payload       string            `json:"payload"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func read(path string, plan *Plan) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		plan.Inputs[path] = ""
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if len(b) > 8*1024*1024 {
		return nil, fmt.Errorf("配置文件 %s 超过 8 MiB", path)
	}
	if err == nil {
		plan.Inputs[path] = digest(b)
	}
	return b, err
}

func document(path string, plan *Plan) (map[string]any, error) {
	var b []byte
	for _, change := range plan.Files {
		if change.Path == path {
			b = []byte(change.Content)
			break
		}
	}
	if b == nil {
		var err error
		b, err = read(path, plan)
		if err != nil {
			return nil, err
		}
	}
	if b == nil {
		return nil, fmt.Errorf("未找到配置文件：%s", filepath.Base(path))
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("无法读取 %s：%w", filepath.Base(path), err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func planned(path string, b []byte, plan *Plan) error {
	for i, change := range plan.Files {
		if change.Path == path {
			if change.Hash == digest(b) {
				plan.Files = append(plan.Files[:i], plan.Files[i+1:]...)
			} else {
				plan.Files[i].Content = string(b)
			}
			return nil
		}
	}
	old, err := read(path, plan)
	if err != nil {
		return err
	}
	if string(old) != string(b) {
		plan.Files = append(plan.Files, Change{path, plan.Inputs[path], string(b)})
	}
	return nil
}

func plannedYAML(path string, m map[string]any, plan *Plan) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return planned(path, b, plan)
}

func text(v any) string { s, _ := v.(string); return s }
func number(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
func list(m map[string]any, key string) []any { a, _ := m[key].([]any); return a }
func mapping(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	if v == nil {
		v = map[string]any{}
		m[key] = v
	}
	return v
}

func scoped(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != filepath.Base(name) {
		return "", errors.New("扩展文件必须位于当前 profiles 目录内")
	}
	path := filepath.Join(root, name)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("不支持链接形式的扩展文件")
	}
	return path, nil
}

func extension(meta, active map[string]any, kind, root string, plan *Plan) (string, map[string]any, error) {
	options := mapping(active, "option")
	uid := text(options[kind])
	for _, entry := range list(meta, "items") {
		item, ok := entry.(map[string]any)
		if ok && uid != "" && text(item["uid"]) == uid {
			if text(item["type"]) != kind {
				return "", nil, errors.New("订阅扩展类型不匹配")
			}
			path, err := scoped(filepath.Join(root, "profiles"), text(item["file"]))
			if err != nil {
				return "", nil, err
			}
			data, err := document(path, plan)
			return path, data, err
		}
	}
	if uid != "" {
		return "", nil, errors.New("当前订阅引用的扩展不存在")
	}
	uid = "fdu-" + digest([]byte(text(active["uid"]) + kind))[:12]
	path, err := scoped(filepath.Join(root, "profiles"), uid+".yaml")
	if err != nil {
		return "", nil, err
	}
	b, err := read(path, plan)
	if err != nil {
		return "", nil, err
	}
	if b != nil {
		return "", nil, errors.New("待创建的 FDU 扩展文件已存在，请先检查现有配置")
	}
	options[kind] = uid
	meta["items"] = append(list(meta, "items"), map[string]any{"uid": uid, "type": kind, "file": uid + ".yaml", "name": "FDU " + kind, "desc": "Managed by FDU node and rule manager"})
	data := map[string]any{}
	if kind != "merge" {
		data = map[string]any{"prepend": []any{}, "append": []any{}, "delete": []any{}}
	}
	return path, data, nil
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func proxyPort(address string) (int, error) {
	host, raw, err := net.SplitHostPort(address)
	port, e := strconv.Atoi(raw)
	if err != nil || e != nil || host != "127.0.0.1" || port < 1 || port > 65535 {
		return 0, errors.New("两种模式要求 FDU SOCKS 和 HTTP 代理监听 127.0.0.1 及有效端口，请选择符合要求的配置")
	}
	return port, nil
}

func upsert(m map[string]any, key string, entry map[string]any, check func(map[string]any) bool) error {
	items := list(m, key)
	var kept []any
	for _, existing := range items {
		node, ok := existing.(map[string]any)
		if ok && text(node["name"]) == text(entry["name"]) {
			if !check(node) {
				return fmt.Errorf("已有同名配置 %s，内容与 FDU 接入不匹配；未覆盖", text(entry["name"]))
			}
			continue
		}
		kept = append(kept, existing)
	}
	m[key] = append(kept, entry)
	return nil
}

func managedExtension(m map[string]any, entry map[string]any, check func(map[string]any) bool) error {
	for _, key := range []string{"prepend", "append"} {
		if err := upsert(m, key, entry, check); err != nil {
			return err
		}
		if key == "append" {
			items := list(m, key)
			m[key] = items[:len(items)-1]
		}
	}
	var deletes []any
	for _, name := range list(m, "delete") {
		if name != entry["name"] {
			deletes = append(deletes, name)
		}
	}
	if deletes == nil {
		deletes = []any{}
	}
	m["delete"] = deletes
	return nil
}

func Prepare(r Request) (Plan, error) {
	return prepare(r, false)
}

func prepare(r Request, campusEnabled bool) (Plan, error) {
	if r.RuntimeFile != "" {
		r.Campus, r.Mode = true, "clash-first"
		if r.CampusURL == "" {
			return Plan{}, errors.New("缺少本机校园规则接口")
		}
	}
	p := Plan{Campus: r.Campus, Inputs: map[string]string{}, Files: []Change{}}
	var err error
	if r.RuntimeFile != "" {
		r.RuntimeFile, err = filepath.Abs(r.RuntimeFile)
		if err != nil {
			return p, err
		}
		if r.Root == "" {
			r.Root = filepath.Dir(r.RuntimeFile)
		}
	}
	for _, target := range []*string{&r.Root, &r.Config, &r.Directory} {
		if *target == "" {
			return p, errors.New("请选择 Clash 配置目录和 FDU 配置文件")
		}
		*target, err = filepath.Abs(*target)
		if err != nil {
			return p, err
		}
	}
	metaPath := filepath.Join(r.Root, "profiles.yaml")
	var meta, runtime map[string]any
	var active map[string]any
	runtimePath := filepath.Join(r.Root, "clash-verge.yaml")
	if r.RuntimeFile != "" {
		runtimePath = r.RuntimeFile
		runtime, err = runtimeDocument(runtimePath, &p)
		if err == nil {
			for key, value := range r.RuntimeOverrides {
				runtime[key] = value
			}
		}
	} else {
		meta, err = document(metaPath, &p)
		if err != nil {
			return p, err
		}
		for _, entry := range list(meta, "items") {
			item, ok := entry.(map[string]any)
			if ok && item["uid"] == meta["current"] {
				active = item
				break
			}
		}
		if active == nil || (active["type"] != "remote" && active["type"] != "local") {
			return p, errors.New("请先在 Clash 中选择一个订阅")
		}
		p.Profile, p.ProfileUID = text(active["name"]), text(active["uid"])
		runtime, err = document(runtimePath, &p)
	}
	if err != nil {
		return p, err
	}
	p.Mode = "clash-first"
	for _, entry := range list(runtime, "listeners") {
		if node, ok := entry.(map[string]any); ok && node["name"] == "FDU-FALLBACK" && node["proxy"] == "GLOBAL" {
			p.Mode = "fdu-first"
			p.LegacyFront = true
		}
	}
	if r.Campus {
		p.ClashMode = strings.ToLower(strings.TrimSpace(text(runtime["mode"])))
		if r.ClashMode != "" || r.RuntimeFile != "" {
			p.ClashMode = strings.ToLower(strings.TrimSpace(r.ClashMode))
		}
		if p.ClashMode != "global" && p.ClashMode != "rule" {
			return p, errors.New("校园接入需要 Clash 使用全局或规则模式")
		}
		runtime["mode"] = p.ClashMode
		r.Mode = "clash-first"
	}
	if r.Mode != "" {
		if r.Mode != "clash-first" && r.Mode != "fdu-first" {
			return p, errors.New("未知的连接模式")
		}
		p.Mode = r.Mode
	}
	if r.RuntimeFile == "" {
		verge, err := document(filepath.Join(r.Root, "verge.yaml"), &p)
		if err != nil {
			return p, err
		}
		p.SystemProxy, _ = verge["enable_system_proxy"].(bool)
		p.ProxyGuard, _ = verge["enable_proxy_guard"].(bool)
	}
	p.MixedPort = number(runtime["mixed-port"])
	if p.MixedPort == 0 {
		p.MixedPort = number(runtime["port"])
	}
	if p.MixedPort == 0 {
		return p, errors.New("Clash 未启用 HTTP 或混合代理端口")
	}
	b, err := read(r.Config, &p)
	if err != nil {
		return p, err
	}
	if r.Campus && r.Values != nil {
		b, err = toml.Marshal(r.Values)
		if err != nil {
			return p, fmt.Errorf("FDU 配置无效：%w", err)
		}
	} else if b == nil {
		return p, errors.New("未找到 FDU 配置文件")
	}
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return p, fmt.Errorf("FDU TOML 无效：%w", err)
	}
	if r.Campus {
		normalizeSettingNumbers(cfg)
	}
	if cfg["protocol"] != "atrust" {
		return p, errors.New("两种分流模式均需要 aTrust 配置")
	}
	defaultsChanged := r.Campus && r.Values != nil
	if text(cfg["socks_bind"]) == "" {
		cfg["socks_bind"] = "127.0.0.1:1080"
		defaultsChanged = true
	}
	if text(cfg["http_bind"]) == "" {
		cfg["http_bind"] = "127.0.0.1:1081"
		defaultsChanged = true
	}
	p.SocksPort, err = proxyPort(text(cfg["socks_bind"]))
	if err != nil {
		return p, err
	}
	p.HTTPPort, err = proxyPort(text(cfg["http_bind"]))
	if err != nil {
		return p, err
	}
	if p.SocksPort == p.HTTPPort || p.SocksPort == p.MixedPort || p.HTTPPort == p.MixedPort {
		return p, errors.New("FDU 和 Clash 的代理端口不能相同")
	}
	route := mapping(cfg, "routing")
	u, parseError := url.Parse(text(route["upstream_proxy"]))
	upstreamReady := parseError == nil && u != nil && u.Scheme == "socks5" && u.Hostname() == "127.0.0.1" &&
		u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	if route["enabled"] == true && upstreamReady {
		p.UpstreamPort, _ = strconv.Atoi(u.Port())
	}
	upstreamReady = upstreamReady && p.UpstreamPort > 0 && p.UpstreamPort <= 65535
	if !upstreamReady {
		p.UpstreamPort = 0
	}
	fallbackPort := func(n map[string]any) int {
		port := number(n["port"])
		if n["name"] == "FDU-FALLBACK" && n["type"] == "mixed" && loopback(text(n["listen"])) &&
			(n["proxy"] == "INTERNET" || n["proxy"] == "GLOBAL") && port > 0 && port <= 65535 {
			return port
		}
		return 0
	}
	if r.Campus && p.UpstreamPort == 0 {
		// Resetting GUI data must not allocate a new port over an existing
		// managed listener, which Clash may already have open.
		for _, entry := range list(runtime, "listeners") {
			if node, ok := entry.(map[string]any); ok && fallbackPort(node) != 0 {
				p.UpstreamPort = fallbackPort(node)
				break
			}
		}
	}
	if p.UpstreamPort == 0 {
		p.UpstreamPort = 17902
		occupied := map[int]bool{p.MixedPort: true, p.SocksPort: true, p.HTTPPort: true}
		for _, port := range r.OccupiedPorts {
			occupied[port] = true
		}
		for occupied[p.UpstreamPort] && p.UpstreamPort < 18000 {
			p.UpstreamPort++
		}
		if p.UpstreamPort == 18000 {
			return p, errors.New("找不到可用的专用监听端口")
		}
	}
	if p.UpstreamPort < 1 || p.UpstreamPort > 65535 || p.UpstreamPort == p.MixedPort || p.UpstreamPort == p.SocksPort || p.UpstreamPort == p.HTTPPort {
		return p, errors.New("FDU 上游需使用独立的专用监听端口")
	}
	p.Rules = text(route["rules_file"])
	if p.Rules == "" {
		p.Rules = filepath.Join(r.Directory, "routing-rules.json")
	} else if !filepath.IsAbs(p.Rules) {
		p.Rules = filepath.Join(filepath.Dir(r.Config), p.Rules)
	}
	if b, err := read(p.Rules, &p); err != nil {
		return p, err
	} else if b == nil {
		seed := map[string]any{"version": 2, "disabled": map[string][]string{"domains": {}, "domain_suffixes": {}, "ip_cidrs": {}}}
		for _, key := range []string{"domains", "domain_suffixes", "ip_cidrs"} {
			seed[key] = []any{}
			if values := list(route, key); values != nil {
				seed[key] = values
			}
		}
		data, err := json.Marshal(seed)
		if err != nil {
			return p, err
		}
		canonical, err := routing.ParseRules(data)
		if err != nil {
			return p, fmt.Errorf("预设分流规则无效：%w", err)
		}
		data, err = json.Marshal(canonical)
		if err != nil {
			return p, err
		}
		if err := planned(p.Rules, data, &p); err != nil {
			return p, err
		}
	}
	p.Config = r.Config
	underlayReady := text(cfg["bind_interface"]) != "" || cfg["auto_detect_interface"] == true
	for _, key := range []string{"tun_mode", "add_route", "dns_hijack", "fake_ip", "proxy_all", "tcp_tunnel_mode", "disable_server_config"} {
		if cfg[key] == true {
			if r.Campus {
				switch key {
				case "tun_mode", "add_route", "dns_hijack", "fake_ip":
				default:
					return p, fmt.Errorf("校园分流与 %s 不兼容，请先在客户端设置中关闭后重试", key)
				}
			}
			defaultsChanged = true
		}
	}
	for _, key := range []string{"dial_direct_proxy", "shadowsocks_url", "dns_server_bind"} {
		if text(cfg[key]) != "" {
			if r.Campus {
				return p, fmt.Errorf("校园分流与 %s 不兼容，请先在客户端设置中清空后重试", key)
			}
			defaultsChanged = true
		}
	}
	if len(list(cfg, "port_forwarding")) != 0 || len(list(cfg, "custom_proxy_domain")) != 0 {
		if r.Campus {
			return p, errors.New("校园分流与端口转发或强制代理域名不兼容，请先在客户端设置中移除后重试")
		}
		defaultsChanged = true
	}
	if route["enabled"] != true || !upstreamReady || !underlayReady || text(route["rules_file"]) != p.Rules || defaultsChanged {
		p.NeedsFduStart = true
		if !r.Campus {
			p.Config = filepath.Join(r.Directory, "fdu-routing.toml")
		}
		if p.Config != r.Config {
			if existing, err := read(p.Config, &p); err != nil {
				return p, err
			} else if existing != nil {
				return p, errors.New("已有 fdu-routing.toml，请在窗口中选择该配置后重新检查")
			}
		}
		for _, key := range []string{"tun_mode", "add_route", "dns_hijack", "fake_ip", "proxy_all", "tcp_tunnel_mode", "disable_server_config"} {
			cfg[key] = false
		}
		for _, key := range []string{"dial_direct_proxy", "shadowsocks_url", "dns_server_bind"} {
			cfg[key] = ""
		}
		cfg["port_forwarding"], cfg["custom_proxy_domain"] = []any{}, []any{}
		if !underlayReady {
			cfg["auto_detect_interface"] = true
		}
		route["enabled"], route["rules_file"], route["upstream_proxy"] = true, p.Rules, fmt.Sprintf("socks5://127.0.0.1:%d", p.UpstreamPort)
		for _, key := range []string{"domains", "domain_suffixes", "ip_cidrs"} {
			route[key] = []any{}
		}
		if file := text(cfg["client_data_file"]); file != "" && !filepath.IsAbs(file) {
			cfg["client_data_file"] = filepath.Join(filepath.Dir(r.Config), file)
		}
		out, err := toml.Marshal(cfg)
		if err != nil {
			return p, err
		}
		if err := planned(p.Config, out, &p); err != nil {
			return p, err
		}
	}
	unsafe := map[string]bool{"FDU": true, "FDU-ROUTER": true, "GLOBAL": true, "INTERNET": true}
	for _, entry := range list(runtime, "proxies") {
		if node, ok := entry.(map[string]any); ok && loopback(text(node["server"])) {
			port := number(node["port"])
			if port == p.SocksPort || port == p.HTTPPort || port == p.UpstreamPort || port == p.MixedPort {
				unsafe[text(node["name"])] = true
			}
		}
	}
	// A renamed or chained local proxy can lead straight back into FDU too.
	for changed := true; changed; {
		changed = false
		for _, entry := range list(runtime, "proxy-groups") {
			if group, ok := entry.(map[string]any); ok && !unsafe[text(group["name"])] {
				bad := group["include-all"] == true || group["include-all-proxies"] == true
				for _, member := range list(group, "proxies") {
					bad = bad || unsafe[text(member)]
				}
				if bad {
					unsafe[text(group["name"])], changed = true, true
				}
			}
		}
		for _, entry := range list(runtime, "proxies") {
			if node, ok := entry.(map[string]any); ok && unsafe[text(node["dialer-proxy"])] && !unsafe[text(node["name"])] {
				unsafe[text(node["name"])], changed = true, true
			}
		}
	}
	var excluded []string
	for name := range unsafe {
		if name != "FDU" && name != "FDU-ROUTER" && name != "GLOBAL" && name != "INTERNET" {
			p.UnsafeNodes = append(p.UnsafeNodes, name)
		}
	}
	sort.Strings(p.UnsafeNodes)
	for _, name := range append([]string{"FDU", "FDU-ROUTER", "GLOBAL", "INTERNET"}, p.UnsafeNodes...) {
		excluded = append(excluded, regexp.QuoteMeta(name))
	}
	var firstNode string
	for _, entry := range list(runtime, "proxies") {
		node, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, typ := text(node["name"]), strings.ToLower(text(node["type"]))
		if !unsafe[name] && typ != "direct" && typ != "reject" && typ != "pass" {
			firstNode = name
			break
		}
	}
	if r.RuntimeFile != "" {
		firstNode = runtimeFirstNode(runtime, unsafe)
	}
	if firstNode == "" {
		return p, errors.New("当前订阅中未找到可用的外网节点")
	}
	proxy := map[string]any{"name": "FDU-ROUTER", "type": "socks5", "server": "127.0.0.1", "port": p.SocksPort, "udp": true}
	if text(cfg["socks_user"]) != "" {
		proxy["username"], proxy["password"] = cfg["socks_user"], cfg["socks_passwd"]
	}
	proxyCheck := func(n map[string]any) bool {
		return n["type"] == "socks5" && loopback(text(n["server"])) && number(n["port"]) == p.SocksPort
	}
	group := map[string]any{"name": "INTERNET", "type": "select", "proxies": []any{firstNode}, "include-all": true, "exclude-filter": "^(" + strings.Join(excluded, "|") + ")$"}
	if r.Campus {
		choices := []any{firstNode, "DIRECT"}
		if r.Outside != "" && r.Outside != firstNode && r.Outside != "DIRECT" {
			choices = append(choices, r.Outside)
		}
		group["proxies"] = choices
	}
	groupCheck := func(n map[string]any) bool { return n["type"] == "select" }
	if r.RuntimeFile != "" {
		group = runtimeInternet(runtime, group, firstNode, r.Outside, unsafe)
		groupCheck = func(n map[string]any) bool {
			return n["type"] == "select" && n["include-all-proxies"] == true && strings.HasPrefix(text(n["exclude-filter"]), "^(FDU|FDU-ROUTER|GLOBAL|INTERNET")
		}
	}
	exit := "INTERNET"
	if p.Mode == "fdu-first" {
		exit = "GLOBAL"
	}
	listener := map[string]any{"name": "FDU-FALLBACK", "type": "mixed", "listen": "127.0.0.1", "port": p.UpstreamPort, "proxy": exit}
	listenerCheck := func(n map[string]any) bool {
		return fallbackPort(n) == p.UpstreamPort
	}
	for _, target := range []struct {
		kind, key string
		entry     map[string]any
		check     func(map[string]any) bool
	}{{"proxies", "proxies", proxy, proxyCheck}, {"groups", "proxy-groups", group, groupCheck}, {"merge", "listeners", listener, listenerCheck}} {
		if target.kind == "merge" {
			for _, entry := range list(runtime, "listeners") {
				n, ok := entry.(map[string]any)
				if ok && number(n["port"]) == p.UpstreamPort && (n["name"] != "FDU-FALLBACK" || !listenerCheck(n)) {
					return p, errors.New("专用监听端口已被其他监听使用")
				}
			}
		}
		if err := upsert(runtime, target.key, target.entry, target.check); err != nil {
			return p, err
		}
		if r.RuntimeFile != "" {
			continue
		}
		path, data, err := extension(meta, active, target.kind, r.Root, &p)
		if err != nil {
			return p, err
		}
		if target.kind == "merge" {
			err = upsert(data, "listeners", target.entry, target.check)
		} else {
			err = managedExtension(data, target.entry, target.check)
		}
		if err != nil {
			return p, err
		}
		if err := plannedYAML(path, data, &p); err != nil {
			return p, err
		}
	}
	if r.Campus {
		var err error
		if r.RuntimeFile != "" {
			err = planRuntimeCampus(r, runtime, campusEnabled, &p)
		} else {
			err = planCampus(r, meta, active, runtime, campusEnabled, &p)
		}
		if err != nil {
			return p, err
		}
	}
	if r.RuntimeFile == "" {
		if err := plannedYAML(metaPath, meta, &p); err != nil {
			return p, err
		}
	}
	out, err := yaml.Marshal(runtime)
	if err != nil {
		return p, err
	}
	p.Payload = string(out)
	if r.RuntimeFile == "" {
		if err := planned(runtimePath, out, &p); err != nil {
			return p, err
		}
	}
	return p, nil
}
