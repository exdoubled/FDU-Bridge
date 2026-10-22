package clashsetup

import (
	"fmt"
	"path/filepath"

	"github.com/mythologyli/zju-connect/routing"
	"gopkg.in/yaml.v3"
)

const CampusProvider = "FDU-CAMPUS"

const campusRule = "RULE-SET," + CampusProvider + ",FDU-ROUTER"

// PrepareCampus prepares the single Clash-first campus integration without writes.
// enabled controls provider contents after the caller has confirmed FDU is ready.
func PrepareCampus(r Request, enabled bool) (Plan, error) {
	if r.CampusURL == "" {
		return Plan{}, fmt.Errorf("缺少本机校园规则接口")
	}
	r.Campus, r.Mode = true, "clash-first"
	return prepare(r, enabled)
}

// CampusRulesDocument renders only active entries from the canonical FDU rules.
func CampusRulesDocument(raw []byte, enabled bool) ([]byte, error) {
	rules, err := routing.ParseRules(raw)
	if err != nil {
		return nil, err
	}
	payload := []string{}
	if enabled {
		disabled := routing.DisabledRules{}
		if rules.Disabled != nil {
			disabled = *rules.Disabled
		}
		for _, group := range []struct {
			kind             string
			values, disabled []string
		}{
			{"DOMAIN", rules.Domains, disabled.Domains},
			{"DOMAIN-SUFFIX", rules.DomainSuffixes, disabled.DomainSuffixes},
			{"IP-CIDR", rules.IPCIDRs, disabled.IPCIDRs},
		} {
			blocked := map[string]bool{}
			for _, value := range group.disabled {
				blocked[value] = true
			}
			for _, value := range group.values {
				if !blocked[value] {
					payload = append(payload, group.kind+","+value)
				}
			}
		}
	}
	return yaml.Marshal(struct {
		Payload []string `yaml:"payload"`
	}{payload})
}

func campusProvider(m map[string]any, provider map[string]any, localPath string) error {
	providers := mapping(m, "rule-providers")
	if existing, exists := providers[CampusProvider]; exists {
		owned, ok := existing.(map[string]any)
		local := ok && owned["type"] == "file" && owned["path"] == localPath
		remote := ok && owned["type"] == "http" && owned["url"] == provider["url"] && owned["path"] == nil && owned["proxy"] == "DIRECT"
		if !ok || owned["behavior"] != "classical" || (!local && !remote) {
			return fmt.Errorf("已有同名规则集 %s，内容与 FDU 接入不匹配；未覆盖", CampusProvider)
		}
	}
	providers[CampusProvider] = provider
	return nil
}

func prependCampusRule(items []any) []any {
	result := []any{campusRule}
	for _, item := range items {
		if item != campusRule {
			result = append(result, item)
		}
	}
	return result
}

func planCampus(r Request, meta, active, runtime map[string]any, enabled bool, p *Plan) error {
	var err error
	p.ProviderPath, err = scoped(filepath.Join(r.Root, "profiles"), "fdu-campus-"+digest([]byte(p.ProfileUID))[:12]+".yaml")
	if err != nil {
		return err
	}
	provider := map[string]any{"type": "http", "behavior": "classical", "format": "yaml", "url": r.CampusURL + digest([]byte(p.ProviderPath)) + ".yaml", "proxy": "DIRECT", "interval": 0}
	runtimeProviders, _ := runtime["rule-providers"].(map[string]any)
	_, ownedRuntime := runtimeProviders[CampusProvider]
	if err := campusProvider(runtime, provider, p.ProviderPath); err != nil {
		return err
	}
	runtime["rules"] = prependCampusRule(list(runtime, "rules"))
	mergePath, merge, err := extension(meta, active, "merge", r.Root, p)
	if err != nil {
		return err
	}
	mergeProviders, _ := merge["rule-providers"].(map[string]any)
	_, ownedMerge := mergeProviders[CampusProvider]
	if err := campusProvider(merge, provider, p.ProviderPath); err != nil {
		return err
	}
	if existing, err := read(p.ProviderPath, p); err != nil {
		return err
	} else if existing != nil && !ownedRuntime && !ownedMerge {
		return fmt.Errorf("待创建的 %s 规则文件已存在且不属于当前接入；未覆盖", CampusProvider)
	}
	if err := plannedYAML(mergePath, merge, p); err != nil {
		return err
	}
	rulesPath, rules, err := extension(meta, active, "rules", r.Root, p)
	if err != nil {
		return err
	}
	rules["prepend"] = prependCampusRule(list(rules, "prepend"))
	if err := plannedYAML(rulesPath, rules, p); err != nil {
		return err
	}
	raw, err := read(p.Rules, p)
	if err != nil {
		return err
	}
	for _, change := range p.Files {
		if change.Path == p.Rules {
			raw = []byte(change.Content)
			break
		}
	}
	data, err := CampusRulesDocument(raw, enabled)
	if err != nil {
		return fmt.Errorf("校园规则无效：%w", err)
	}
	return planned(p.ProviderPath, data, p)
}
