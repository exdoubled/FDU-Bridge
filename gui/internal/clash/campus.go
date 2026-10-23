package clash

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/clashsetup"
	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/gui/internal/sysproc"
	"gopkg.in/yaml.v3"
)

const campusProviderAPI = "/providers/rules/" + clashsetup.CampusProvider

var ErrCampusPending = errors.New("等待 FDU 加载校园规则")

// CampusAttachment contains only ownership and routing state, never credentials.
type CampusAttachment struct {
	Root         string   `json:"root"`
	ProfileUID   string   `json:"profile_uid"`
	Config       string   `json:"config"`
	Rules        string   `json:"rules"`
	ProviderPath string   `json:"provider_path"`
	ProviderHash string   `json:"provider_hash"`
	RulesHash    string   `json:"rules_hash"`
	ClashMode    string   `json:"clash_mode"`
	Global       string   `json:"global"`
	Outside      string   `json:"outside"`
	MixedPort    int      `json:"mixed_port"`
	SocksPort    int      `json:"socks_port,omitempty"`
	HTTPPort     int      `json:"http_port,omitempty"`
	UpstreamPort int      `json:"upstream_port,omitempty"`
	UnsafeNodes  []string `json:"unsafe_nodes"`
	Active       bool     `json:"active"`
	RuntimeFile  string   `json:"runtime_file,omitempty"`
	RuntimeHash  string   `json:"runtime_hash,omitempty"`
}

// SafeCampusTarget additionally permits DIRECT for the preserved outside exit.
func SafeCampusTarget(proxies Proxies, name string, visiting, unsafe map[string]bool) bool {
	if reserved(name) || unsafe[name] || visiting[name] {
		return false
	}
	node := proxies[name]
	if node == nil || node.Type == "" {
		return false
	}
	if name == "DIRECT" && strings.EqualFold(node.Type, "Direct") {
		return true
	}
	switch strings.ToLower(node.Type) {
	case "direct", "reject", "rejectdrop", "pass", "compatible":
		return false
	}
	if node.All == nil {
		return true
	}
	if len(node.All) == 0 {
		return false
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, child := range node.All {
		if !SafeCampusTarget(proxies, child, visiting, unsafe) {
			return false
		}
	}
	return true
}

func campusOutside(proxies Proxies, mode string, unsafe map[string]bool) (string, error) {
	global, internet := proxies["GLOBAL"], proxies["INTERNET"]
	if global == nil {
		return "", errors.New("Clash 缺少 GLOBAL 选择器")
	}
	outside := global.Now
	if outside == "FDU-ROUTER" || outside == "INTERNET" || mode == "rule" {
		if internet != nil && SafeCampusTarget(proxies, internet.Now, map[string]bool{}, unsafe) {
			outside = internet.Now
		} else if mode == "rule" && SafeCampusTarget(proxies, global.Now, map[string]bool{}, unsafe) {
			outside = global.Now
		} else if mode == "rule" {
			outside = "DIRECT"
		}
	}
	if !SafeCampusTarget(proxies, outside, map[string]bool{}, unsafe) {
		return "", errors.New("当前外网选择无效或可能返回 FDU，请先在 Clash 中选择安全的外网节点或 DIRECT")
	}
	return outside, nil
}

// PrepareCampus reads Clash's actual mode and prepares its local rule endpoint.
func (c *Connection) PrepareCampus(root, config string, values map[string]any) (Plan, error) {
	api, err := c.api(root)
	if err != nil {
		return Plan{}, err
	}
	var live map[string]any
	var response proxyResponse
	if err = api.Do("GET", "/configs", nil, &live); err != nil {
		return Plan{}, err
	}
	if err = api.Do("GET", "/proxies", nil, &response); err != nil {
		return Plan{}, err
	}
	mode := strings.ToLower(text(live["mode"]))
	if mode != "global" && mode != "rule" {
		return Plan{}, errors.New("校园接入需要 Clash 使用全局或规则模式")
	}
	// The first pass discovers local aliases before accepting an outside group.
	feed, err := c.campusFeed()
	if err != nil {
		return Plan{}, err
	}
	r := clashsetup.Request{Campus: true, CampusURL: feed.base(), Root: root, RuntimeFile: c.RuntimePath(), Config: config, Directory: c.Dir, Values: values, ClashMode: mode, OccupiedPorts: sysproc.UsedPorts(17902, 18000)}
	if r.RuntimeFile != "" {
		r.RuntimeOverrides = runtimeOverrides(live, api)
	}
	plan, err := clashsetup.PrepareCampus(r, false)
	if err != nil {
		return plan, err
	}
	feed.url(plan.ProviderPath)
	port := number(live["mixed-port"])
	if port == 0 {
		port = number(live["port"])
	}
	if port != plan.MixedPort {
		return plan, errors.New("所选目录与当前 Clash 控制接口不匹配")
	}
	outside, err := campusOutside(response.Proxies, mode, UnsafeSet(plan))
	if err != nil {
		return plan, err
	}
	if plan.LegacyFront {
		proxy, proxyErr := CaptureProxy()
		if proxyErr != nil || proxy.pointsAt(plan.HTTPPort) {
			return plan, errors.New("请先将系统代理恢复到 Clash，并关闭旧版 FDU 前置，再启用")
		}
	}
	r.Outside = outside
	return clashsetup.PrepareCampus(r, false)
}

func (c *Connection) CampusCoverage(root string, rulesFiles ...string) (string, error) {
	api, err := c.api(root)
	if err != nil {
		return "", err
	}
	var config map[string]any
	if err = api.Do("GET", "/configs", nil, &config); err != nil {
		return "", err
	}
	if tun, _ := config["tun"].(map[string]any); tun != nil && tun["enable"] == true {
		return "全部经 Clash TUN 接管的应用", nil
	}
	port := number(config["mixed-port"])
	if port == 0 {
		port = number(config["port"])
	}
	file := ""
	if len(rulesFiles) > 0 {
		file = rulesFiles[0]
	}
	return proxyCoverage(port, file), nil
}

func (c *Connection) ImportCampus(plan Plan) (string, CampusAttachment, error) {
	a := CampusAttachment{}
	if !plan.Campus {
		return "", a, errors.New("缺少校园接入预览，请重新检查")
	}
	root, err := profilesRoot(plan)
	if err != nil {
		return "", a, err
	}
	api, err := c.api(root)
	if err != nil {
		return "", a, err
	}
	var response proxyResponse
	if err = api.Do("GET", "/proxies", nil, &response); err != nil {
		return "", a, err
	}
	outside, err := campusOutside(response.Proxies, plan.ClashMode, UnsafeSet(plan))
	if err != nil {
		return "", a, err
	}
	global := response.Proxies["GLOBAL"].Now
	if plan.ClashMode == "global" && (global == "FDU-ROUTER" || global == "INTERNET") {
		global = outside
	}
	backup, err := c.Import(plan)
	if err != nil {
		return "", a, err
	}
	a = CampusAttachment{Root: root, ProfileUID: plan.ProfileUID, Config: plan.Config, Rules: plan.Rules, ProviderPath: plan.ProviderPath, ProviderHash: fsx.HashFile(plan.ProviderPath), ClashMode: plan.ClashMode, Global: global, Outside: outside, MixedPort: plan.MixedPort, UnsafeNodes: append([]string(nil), plan.UnsafeNodes...)}
	a.RuntimeFile, a.RuntimeHash = plan.RuntimeFile, plan.RuntimeHash
	a.SocksPort, a.HTTPPort, a.UpstreamPort = plan.SocksPort, plan.HTTPPort, plan.UpstreamPort
	c.targetMu.Lock()
	c.previousRuntime = ""
	c.targetMu.Unlock()
	return backup, a, nil
}

func readCampusProxies(api API, mode string) (Proxies, error) {
	var config map[string]any
	var response proxyResponse
	if err := api.Do("GET", "/configs", nil, &config); err != nil {
		return nil, err
	}
	if mode != "" && !strings.EqualFold(text(config["mode"]), mode) {
		return nil, ErrClashChanging
	}
	if err := api.Do("GET", "/proxies", nil, &response); err != nil {
		return nil, err
	}
	if response.Proxies["GLOBAL"] == nil || response.Proxies["INTERNET"] == nil || response.Proxies["FDU-ROUTER"] == nil {
		return nil, errors.New("Clash 校园接入配置已变化，请重新启用")
	}
	return response.Proxies, nil
}

func campusSet(api API, group, target, expected, mode string, unsafe map[string]bool) error {
	proxies, err := readCampusProxies(api, mode)
	if err != nil {
		return err
	}
	node := proxies[group]
	if node == nil || node.Type != "Selector" || node.Now != expected || !contains(node.All, target) {
		return errors.New("Clash 选择或节点列表已变化，未覆盖")
	}
	if group == "INTERNET" || target != "FDU-ROUTER" {
		if !SafeCampusTarget(proxies, target, map[string]bool{}, unsafe) {
			return errors.New("外网选择不安全，未覆盖")
		}
	} else if !SafeCampusTarget(proxies, proxies["INTERNET"].Now, map[string]bool{}, unsafe) {
		return errors.New("INTERNET 外网选择不安全，未启用")
	}
	if target == expected {
		return nil
	}
	if err = api.Do("PUT", "/proxies/"+group, map[string]string{"name": target}, nil); err != nil {
		return err
	}
	after, err := readCampusProxies(api, mode)
	if err == nil && after[group].Now != target {
		err = errors.New("Clash 未确认节点选择")
	}
	return err
}

func restoreCampusSelections(api API, before Proxies, mode string, unsafe map[string]bool) error {
	outside, err := campusOutside(before, mode, unsafe)
	if err != nil {
		return err
	}
	now, err := readCampusProxies(api, mode)
	if err != nil {
		return err
	}
	if err = campusSet(api, "INTERNET", outside, now["INTERNET"].Now, mode, unsafe); err != nil {
		return err
	}
	global := before["GLOBAL"].Now
	if mode == "global" && (global == "FDU-ROUTER" || global == "INTERNET") {
		global = outside
	}
	now, err = readCampusProxies(api, mode)
	if err != nil {
		return err
	}
	return campusSet(api, "GLOBAL", global, now["GLOBAL"].Now, mode, unsafe)
}

func campusProfile(a CampusAttachment) (bool, error) {
	if a.RuntimeFile != "" {
		raw, err := fsx.ReadBounded(a.RuntimeFile, 8*1024*1024)
		return err == nil && fsx.Hash(raw) == a.RuntimeHash, err
	}
	b, err := fsx.ReadBounded(filepath.Join(a.Root, "profiles.yaml"), 8*1024*1024)
	if err != nil {
		return false, err
	}
	var meta struct {
		Current string `yaml:"current"`
	}
	if err = yaml.Unmarshal(b, &meta); err != nil {
		return false, err
	}
	return meta.Current == a.ProfileUID, nil
}

func campusProviderBytes(a CampusAttachment) ([]byte, error) {
	b, err := fsx.ReadBounded(a.ProviderPath, 8*1024*1024)
	if err != nil {
		return nil, err
	}
	if a.ProviderHash == "" || fsx.Hash(b) != a.ProviderHash {
		return nil, errors.New("校园规则集已被其他操作修改，未覆盖，请重新检查")
	}
	return b, nil
}

func (c *Connection) CheckCampus(a CampusAttachment) error {
	if _, err := campusProviderBytes(a); err != nil {
		return err
	}
	match, err := campusProfile(a)
	if err != nil {
		return err
	}
	if !match {
		return ErrClashChanging
	}
	api, err := c.campusAPI(a)
	if err != nil {
		return err
	}
	proxies, err := readCampusProxies(api, a.ClashMode)
	if err != nil {
		return err
	}
	want := a.Global
	if a.Active && a.ClashMode == "global" {
		want = "FDU-ROUTER"
	}
	if proxies["GLOBAL"].Now != want {
		return errors.New("Clash GLOBAL 已被其他操作修改，校园接入已停止跟随")
	}
	if !SafeCampusTarget(proxies, proxies["INTERNET"].Now, map[string]bool{}, UnsafeSet(Plan{UnsafeNodes: a.UnsafeNodes})) {
		return errors.New("INTERNET 当前选择可能返回 FDU，校园接入已停止跟随")
	}
	return nil
}

// replaceCampusProvider rolls back only while its own bytes still occupy the file.
func replaceCampusProvider(api API, a CampusAttachment, data []byte, forceReload bool) (CampusAttachment, error) {
	old, err := campusProviderBytes(a)
	if err != nil {
		return a, err
	}
	if fsx.Hash(data) == a.ProviderHash {
		if forceReload && api != nil {
			return a, api.Do("PUT", campusProviderAPI, nil, nil)
		}
		return a, nil
	}
	if fsx.HashFile(a.ProviderPath) != a.ProviderHash {
		return a, errors.New("校园规则集正在被其他操作修改，未覆盖")
	}
	if err = fsx.AtomicWrite(a.ProviderPath, data); err != nil {
		return a, err
	}
	next := a
	next.ProviderHash = fsx.Hash(data)
	if api == nil {
		return next, nil
	}
	if err = api.Do("PUT", campusProviderAPI, nil, nil); err == nil {
		return next, nil
	}
	if fsx.HashFile(a.ProviderPath) != next.ProviderHash {
		return a, fmt.Errorf("%w；规则文件已被其他操作修改，未恢复", err)
	}
	if rollback := fsx.AtomicWrite(a.ProviderPath, old); rollback != nil {
		return next, fmt.Errorf("%w；规则文件恢复失败：%v", err, rollback)
	}
	if rollback := api.Do("PUT", campusProviderAPI, nil, nil); rollback != nil {
		return a, fmt.Errorf("%w；Clash 规则集恢复未确认：%v", err, rollback)
	}
	return a, err
}

func (c *Connection) acknowledgedCampusRules(a CampusAttachment) ([]byte, string, error) {
	raw, err := fsx.ReadBounded(a.Rules, 256*1024)
	if err != nil {
		return nil, "", err
	}
	data, err := clashsetup.CampusRulesDocument(raw, true)
	if err != nil {
		return nil, "", err
	}
	hash := fsx.Hash(raw)
	store, err := rules.New(a.Rules)
	if err != nil {
		return nil, "", err
	}
	_, loaded, failure, ok := store.Acknowledgement()
	if !ok {
		return nil, "", ErrCampusPending
	}
	if failure != "" {
		return nil, "", fmt.Errorf("FDU 未接受校园规则：%s", failure)
	}
	if loaded != hash {
		return nil, "", ErrCampusPending
	}
	if err = fduReady(Plan{Config: a.Config, Rules: a.Rules}); err != nil {
		if strings.Contains(err.Error(), "尚未收到 FDU") {
			return nil, "", fmt.Errorf("%w：%v", ErrCampusPending, err)
		}
		return nil, "", err
	}
	if fsx.HashFile(a.Rules) != hash {
		return nil, "", ErrCampusPending
	}
	return data, hash, nil
}

func (c *Connection) ActivateCampus(a CampusAttachment) (CampusAttachment, error) {
	if _, err := campusProviderBytes(a); err != nil {
		return a, err
	}
	if match, err := campusProfile(a); err != nil || !match {
		if err == nil {
			err = errors.New("Clash 当前订阅已变化，请重新启用校园接入")
		}
		return a, err
	}
	data, hash, err := c.acknowledgedCampusRules(a)
	if err != nil {
		return a, err
	}
	api, err := c.campusAPI(a)
	if err != nil {
		return a, err
	}
	return c.activateCampus(api, a, data, hash)
}

func (c *Connection) activateCampus(api API, a CampusAttachment, data []byte, rulesHash string) (CampusAttachment, error) {
	if err := c.CheckCampus(a); err != nil {
		return a, err
	}
	next, err := replaceCampusProvider(api, a, data, true)
	if err != nil {
		return next, err
	}
	proxies, err := readCampusProxies(api, a.ClashMode)
	unsafe := UnsafeSet(Plan{UnsafeNodes: a.UnsafeNodes})
	if err == nil {
		err = campusSet(api, "INTERNET", a.Outside, proxies["INTERNET"].Now, a.ClashMode, unsafe)
	}
	if err == nil && a.ClashMode == "global" {
		proxies, err = readCampusProxies(api, a.ClashMode)
		if err == nil {
			err = campusSet(api, "GLOBAL", "FDU-ROUTER", proxies["GLOBAL"].Now, a.ClashMode, unsafe)
		}
	}
	if err == nil {
		next.Active, next.RulesHash = true, rulesHash
		if err = c.CheckCampus(next); err == nil {
			return next, nil
		}
	}
	next.Active = true
	restored, rollback := c.DeactivateCampus(next)
	if rollback != nil {
		return restored, fmt.Errorf("%v；校园接入恢复未确认：%v", err, rollback)
	}
	return restored, err
}

func (c *Connection) DeactivateCampus(a CampusAttachment) (CampusAttachment, error) {
	if a.RuntimeFile != "" {
		return c.deactivateRuntime(a)
	}
	_, providerErr := campusProviderBytes(a)
	match, err := campusProfile(a)
	if err != nil {
		return a, err
	}
	var api API
	if match {
		api, err = c.campusAPI(a)
		if err != nil {
			return a, err
		}
		var response proxyResponse
		if err = api.Do("GET", "/proxies", nil, &response); err != nil {
			return a, err
		}
		proxies := response.Proxies
		if err = restoreCampusExit(api, &a, proxies); err != nil {
			return a, err
		}
		if proxies["INTERNET"] == nil || proxies["FDU-ROUTER"] == nil {
			api = nil
		}
	} else {
		api, err = c.api(a.Root)
		if err != nil {
			return a, err
		}
		var response proxyResponse
		if err = api.Do("GET", "/proxies", nil, &response); err != nil {
			return a, err
		}
		if response.Proxies["FDU-ROUTER"] != nil {
			return a, errors.New("订阅已切换且仍引用 FDU，请切回原订阅后停用")
		}
		api = nil
	}
	if providerErr != nil {
		return a, providerErr
	}
	empty := []byte("payload: []\n")
	a, err = replaceCampusProvider(api, a, empty, true)
	if err == nil {
		a.Active = false
		a.RulesHash = ""
	}
	return a, err
}

func restoreCampusExit(api API, a *CampusAttachment, proxies Proxies) error {
	global, internet := proxies["GLOBAL"], proxies["INTERNET"]
	if global == nil || global.Now != "FDU-ROUTER" {
		return nil
	}
	unsafe := UnsafeSet(Plan{UnsafeNodes: a.UnsafeNodes})
	target := a.Global
	if !SafeCampusTarget(proxies, target, map[string]bool{}, unsafe) {
		target = a.Outside
	}
	if internet != nil && SafeCampusTarget(proxies, internet.Now, map[string]bool{}, unsafe) {
		target = internet.Now
	}
	if err := restoreCampusGlobal(api, target, unsafe); err != nil {
		return err
	}
	a.Global, a.Outside = target, target
	return nil
}

func restoreCampusGlobal(api API, target string, unsafe map[string]bool) error {
	var response proxyResponse
	if err := api.Do("GET", "/proxies", nil, &response); err != nil {
		return err
	}
	global := response.Proxies["GLOBAL"]
	if global == nil || global.Now != "FDU-ROUTER" {
		return nil
	}
	if !contains(global.All, target) || !SafeCampusTarget(response.Proxies, target, map[string]bool{}, unsafe) {
		return errors.New("原外网选择已失效，未覆盖 GLOBAL")
	}
	if err := api.Do("PUT", "/proxies/GLOBAL", map[string]string{"name": target}, nil); err != nil {
		return err
	}
	if err := api.Do("GET", "/proxies", nil, &response); err != nil {
		return err
	}
	if response.Proxies["GLOBAL"] == nil || response.Proxies["GLOBAL"].Now != target {
		return errors.New("Clash 未确认恢复外网选择")
	}
	return nil
}

func (c *Connection) SyncCampus(a CampusAttachment) (CampusAttachment, error) {
	if !a.Active {
		return a, nil
	}
	data, hash, err := c.acknowledgedCampusRules(a)
	if err != nil {
		return a, err
	}
	if err = c.CheckCampus(a); err != nil {
		return a, err
	}
	api, err := c.campusAPI(a)
	if err != nil {
		return a, err
	}
	next, err := replaceCampusProvider(api, a, data, false)
	if err == nil {
		next.RulesHash = hash
	}
	return next, err
}
