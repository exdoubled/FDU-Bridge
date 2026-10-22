package clash

import (
	"errors"
	"fmt"
	"strings"
)

type ProxyData struct {
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

type Proxies map[string]*ProxyData

type proxyResponse struct {
	Proxies Proxies `json:"proxies"`
}

// NodeState is what the node page shows; Problem blocks switching without hiding the list.
type NodeState struct {
	Mode     string            `json:"mode"`
	Global   string            `json:"global"`
	Selector string            `json:"selector"`
	Current  string            `json:"current"`
	Choices  []string          `json:"choices"`
	Types    map[string]string `json:"types"` // Clash proxy type per choice, e.g. Shadowsocks or URLTest
	Problem  string            `json:"problem"`
	Imported bool              `json:"imported"`
}

func text(v any) string { s, _ := v.(string); return s }

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func reserved(name string) bool {
	return name == "" || name == "GLOBAL" || name == "INTERNET" || name == "FDU" || name == "FDU-ROUTER"
}

// SafeTarget rejects anything that can lead back into FDU, including nested groups.
func SafeTarget(proxies Proxies, name string, visiting map[string]bool, unsafe map[string]bool) bool {
	if reserved(name) || unsafe[name] {
		return false
	}
	node := proxies[name]
	if node == nil || node.Type == "" {
		return false
	}
	switch node.Type {
	case "Direct", "Reject", "RejectDrop", "Pass", "Compatible":
		return false
	}
	if visiting[name] {
		return false
	}
	visiting[name] = true
	defer delete(visiting, name)
	if node.All == nil {
		return true
	}
	if len(node.All) == 0 {
		return false
	}
	for _, child := range node.All {
		if !SafeTarget(proxies, child, visiting, unsafe) {
			return false
		}
	}
	return true
}

type Picker struct {
	API      API    // nil discovers the live controller on each call
	ClashDir string // where Discover reads the running controller's address
	Front    bool
	Campus   bool
	Unsafe   map[string]bool
}

func (p *Picker) api() (API, error) {
	if p.API != nil {
		return p.API, nil
	}
	return Discover(p.ClashDir)
}

func (p *Picker) Load() (NodeState, error) {
	api, err := p.api()
	if err != nil {
		return NodeState{}, err
	}
	return p.load(api)
}

func (p *Picker) load(api API) (NodeState, error) {
	var config map[string]any
	var response proxyResponse
	if err := api.Do("GET", "/configs", nil, &config); err != nil {
		return NodeState{}, err
	}
	if err := api.Do("GET", "/proxies", nil, &response); err != nil {
		return NodeState{}, err
	}
	if config == nil || response.Proxies == nil {
		return NodeState{}, errors.New("未能读取 Clash 状态，请刷新")
	}
	global, internet := response.Proxies["GLOBAL"], response.Proxies["INTERNET"]
	selected := internet
	state := NodeState{Mode: "未知", Global: "未找到", Selector: "INTERNET", Current: "未找到", Choices: []string{}, Types: map[string]string{},
		Imported: internet != nil && response.Proxies["FDU-ROUTER"] != nil}
	if p.Front && !p.Campus {
		selected, state.Selector = global, "GLOBAL"
	}
	if mode := text(config["mode"]); mode != "" {
		state.Mode = mode
	}
	if global != nil {
		state.Global = global.Now
	}
	if p.Campus && strings.EqualFold(state.Mode, "rule") {
		state.Current = "沿用 Clash 设置"
		return state, nil
	}
	if selected != nil {
		state.Current = selected.Now
		seen := map[string]bool{}
		for _, name := range selected.All {
			safe := SafeTarget(response.Proxies, name, map[string]bool{}, p.Unsafe)
			if p.Campus {
				safe = SafeCampusTarget(response.Proxies, name, map[string]bool{}, p.Unsafe)
			}
			if !seen[name] && safe {
				seen[name] = true
				state.Choices = append(state.Choices, name)
				state.Types[name] = response.Proxies[name].Type
			}
		}
	}
	switch {
	case !strings.EqualFold(state.Mode, "global"):
		state.Problem = "Clash 当前不是全局模式，请在 Clash 中切换到全局模式后刷新"
	case p.Front && (global == nil || !SafeTarget(response.Proxies, global.Now, map[string]bool{}, p.Unsafe)):
		state.Problem = "FDU 前置模式的 GLOBAL 必须选择外网节点，请在首页重新应用连接模式"
	case !p.Front && state.Global != "FDU-ROUTER":
		state.Problem = "GLOBAL 未选中 FDU-ROUTER，请在设置中“导入并启用”"
	case internet == nil || internet.Type != "Selector":
		state.Problem = "未找到可手动选择的 INTERNET 组，请先在设置中导入 Clash 配置"
	case len(state.Choices) == 0:
		state.Problem = "INTERNET 中没有可用的外网节点"
	}
	if p.Campus && state.Problem != "" {
		if len(state.Choices) == 0 && internet != nil {
			state.Problem = "未找到可用外网节点，请检查当前订阅"
		} else {
			state.Problem = "请先在首页启用校内访问"
		}
	}
	return state, nil
}

// Select re-reads right before writing and never retries an unconfirmed write.
func (p *Picker) Select(name string) (NodeState, error) {
	api, err := p.api()
	if err != nil {
		return NodeState{}, err
	}
	before, err := p.load(api)
	if err != nil {
		return NodeState{}, err
	}
	if before.Problem != "" {
		return NodeState{}, errors.New(before.Problem)
	}
	if !contains(before.Choices, name) {
		return NodeState{}, errors.New("所选节点已不存在或可能形成回环，请刷新列表后重新选择")
	}
	if before.Current == name {
		return before, nil
	}
	after, err := func() (NodeState, error) {
		if err := api.Do("PUT", "/proxies/"+before.Selector, map[string]string{"name": name}, nil); err != nil {
			return NodeState{}, err
		}
		after, err := p.load(api)
		if err == nil && (after.Problem != "" || after.Current != name) {
			err = errors.New("读取的选择与请求不一致")
		}
		return after, err
	}()
	if err != nil {
		return NodeState{}, fmt.Errorf("切换请求已发送，但未能确认结果；请刷新查看当前节点，不会自动重试。%w", err)
	}
	return after, nil
}
