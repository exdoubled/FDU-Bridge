package clash

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"gopkg.in/yaml.v3"
)

// ExportPatch exports the same node, provider and listener used by the managed integration.
// It persists only GUI-owned feed data; installing the extension is an explicit user action.
func (c *Connection) ExportPatch(plan Plan) (string, string, error) {
	script, fragment, err := renderExportPatch(plan)
	if err != nil {
		return "", "", err
	}
	feed, err := c.campusFeed()
	if err != nil {
		return "", "", err
	}
	feed.url(plan.ProviderPath)
	if err = feed.save(); err != nil {
		return "", "", err
	}
	if _, err = os.Stat(plan.ProviderPath); os.IsNotExist(err) {
		err = fsx.AtomicWrite(plan.ProviderPath, []byte("payload: []\n"))
	}
	if err != nil {
		return "", "", err
	}
	return script, fragment, nil
}

func renderExportPatch(plan Plan) (string, string, error) {
	var runtime map[string]any
	if err := yaml.Unmarshal([]byte(plan.Payload), &runtime); err != nil {
		return "", "", err
	}
	patch := map[string]any{"rules": []string{"RULE-SET,FDU-CAMPUS,FDU-ROUTER"}}
	for key, name := range map[string]string{"proxies": "FDU-ROUTER", "proxy-groups": "INTERNET", "listeners": "FDU-FALLBACK"} {
		items, _ := runtime[key].([]any)
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok && entry["name"] == name {
				if key == "proxy-groups" {
					// Subscription names change. Expand current nodes/providers in
					// Mihomo instead of embedding references to today's subscription.
					entry["proxies"] = []any{"DIRECT"}
					entry["include-all-proxies"], entry["include-all-providers"] = true, true
					delete(entry, "include-all")
					delete(entry, "use")
				}
				patch[key] = []any{entry}
			}
		}
	}
	providers, _ := runtime["rule-providers"].(map[string]any)
	if providers == nil || providers["FDU-CAMPUS"] == nil {
		return "", "", errors.New("缺少校园规则接口")
	}
	patch["rule-providers"] = map[string]any{"FDU-CAMPUS": providers["FDU-CAMPUS"]}
	data, err := json.Marshal(patch)
	if err != nil {
		return "", "", err
	}
	fragment, err := yaml.Marshal(patch)
	if err != nil {
		return "", "", err
	}
	script := "// FDU Bridge: keep the application running while campus access is enabled.\nconst fdu = " + string(data) + `;
function main(config) {
  for (const key of ["proxies", "proxy-groups", "listeners"]) {
    const existing = config[key] || [];
    for (const entry of fdu[key] || []) {
      const old = existing.find(item => item.name === entry.name);
      if (old && JSON.stringify(old) !== JSON.stringify(entry)) throw new Error("FDU name conflict: " + entry.name);
      if (!old) existing.push(entry);
    }
    config[key] = existing;
  }
  const providers = config["rule-providers"] || {};
  for (const name of Object.keys(fdu["rule-providers"])) {
    if (providers[name] && JSON.stringify(providers[name]) !== JSON.stringify(fdu["rule-providers"][name])) throw new Error("FDU provider conflict: " + name);
    providers[name] = fdu["rule-providers"][name];
  }
  config["rule-providers"] = providers;
  config.rules = fdu.rules.concat((config.rules || []).filter(rule => !fdu.rules.includes(rule)));
  return config;
}
`
	return script, string(fragment), nil
}
