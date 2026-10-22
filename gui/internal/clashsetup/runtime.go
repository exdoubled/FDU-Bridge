package clashsetup

import (
	"fmt"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// runtimeDocument records the external source for validation, never for writing.
func runtimeDocument(path string, p *Plan) (map[string]any, error) {
	m, err := document(path, p)
	if err != nil {
		return nil, err
	}
	p.RuntimeFile, p.RuntimeHash = path, p.Inputs[path]
	p.Profile = filepath.Base(path)
	p.ProfileUID = "runtime-" + digest([]byte(path+"\x00"+p.RuntimeHash))
	return m, nil
}

// Static groups can be retained as choices only when all their paths are safe.
// Provider members are checked against the live controller by the caller.
func runtimeSafeTargets(runtime map[string]any, unsafe map[string]bool) map[string]bool {
	proxies, groups := map[string]map[string]any{}, map[string]map[string]any{}
	for _, entry := range list(runtime, "proxies") {
		if node, ok := entry.(map[string]any); ok {
			proxies[text(node["name"])] = node
		}
	}
	for _, entry := range list(runtime, "proxy-groups") {
		if group, ok := entry.(map[string]any); ok {
			groups[text(group["name"])] = group
		}
	}
	providers, _ := runtime["proxy-providers"].(map[string]any)
	visiting, safe := map[string]bool{}, map[string]bool{"DIRECT": true}
	var target func(string) bool
	target = func(name string) bool {
		if name == "" || unsafe[name] || visiting[name] {
			return false
		}
		if value, checked := safe[name]; checked {
			return value
		}
		visiting[name] = true
		defer delete(visiting, name)
		if node := proxies[name]; node != nil {
			switch text(node["type"]) {
			case "", "direct", "reject", "rejectdrop", "pass", "compatible":
				return false
			}
			if dialer := text(node["dialer-proxy"]); dialer != "" && !target(dialer) {
				return false
			}
			safe[name] = true
			return true
		}
		group := groups[name]
		if group == nil || group["include-all"] == true || group["include-all-proxies"] == true {
			return false
		}
		members, used := list(group, "proxies"), list(group, "use")
		if len(members) == 0 && len(used) == 0 && !(group["include-all-providers"] == true && len(providers) > 0) {
			return false
		}
		for _, member := range members {
			if !target(text(member)) {
				return false
			}
		}
		for _, provider := range used {
			if _, exists := providers[text(provider)]; !exists {
				return false
			}
		}
		safe[name] = true
		return true
	}
	for name := range proxies {
		target(name)
	}
	for name := range groups {
		target(name)
	}
	return safe
}

func runtimeFirstNode(runtime map[string]any, unsafe map[string]bool) string {
	safe := runtimeSafeTargets(runtime, unsafe)
	for _, key := range []string{"proxies", "proxy-groups"} {
		for _, entry := range list(runtime, key) {
			if node, ok := entry.(map[string]any); ok && safe[text(node["name"])] {
				return text(node["name"])
			}
		}
	}
	return "DIRECT"
}

func runtimeInternet(runtime, group map[string]any, first, outside string, unsafe map[string]bool) map[string]any {
	delete(group, "include-all")
	group["include-all-proxies"] = true
	choices := []any{first}
	if first != "DIRECT" {
		choices = append(choices, "DIRECT")
	}
	// Dynamic provider nodes belong in use, not in a static proxies list.
	if outside != "" && outside != first && outside != "DIRECT" && runtimeSafeTargets(runtime, unsafe)[outside] {
		choices = append(choices, outside)
	}
	group["proxies"] = choices
	providers, _ := runtime["proxy-providers"].(map[string]any)
	if len(providers) != 0 {
		names := make([]string, 0, len(providers))
		for name := range providers {
			names = append(names, name)
		}
		sort.Strings(names)
		group["use"] = names
	}
	return group
}

func planRuntimeCampus(r Request, runtime map[string]any, enabled bool, p *Plan) error {
	var err error
	p.ProviderPath, err = scoped(r.Directory, "fdu-campus-runtime.yaml")
	if err != nil {
		return err
	}
	provider := map[string]any{"type": "http", "behavior": "classical", "format": "yaml", "url": r.CampusURL + digest([]byte(p.ProviderPath)) + ".yaml", "proxy": "DIRECT", "interval": 0}
	if err := campusProvider(runtime, provider, p.ProviderPath); err != nil {
		return err
	}
	runtime["rules"] = prependCampusRule(list(runtime, "rules"))
	if existing, err := read(p.ProviderPath, p); err != nil {
		return err
	} else if existing != nil {
		// This deterministic GUI-private path survives source refreshes. Only
		// reuse a rule feed; never overwrite an unrelated file occupying it.
		var doc map[string]any
		if err := yaml.Unmarshal(existing, &doc); err != nil || len(doc) != 1 {
			return fmt.Errorf("已有 %s 规则文件内容不匹配；未覆盖", CampusProvider)
		}
		payload, ok := doc["payload"].([]any)
		if !ok {
			return fmt.Errorf("已有 %s 规则文件内容不匹配；未覆盖", CampusProvider)
		}
		for _, item := range payload {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("已有 %s 规则文件内容不匹配；未覆盖", CampusProvider)
			}
		}
	}
	return planCampusRules(enabled, p)
}
