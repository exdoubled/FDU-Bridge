package routing

import "testing"

func TestExplainMatchUsesEnabledPolicyBeforeDisabledEntries(t *testing.T) {
	rules := RuleSet{Version: 2, Domains: []string{"library.fudan.edu.cn"}, DomainSuffixes: []string{"fudan.edu.cn"}, IPCIDRs: []string{"10.20.0.0/16"}, Disabled: &DisabledRules{Domains: []string{"library.fudan.edu.cn"}, DomainSuffixes: []string{}, IPCIDRs: []string{}}}
	for _, test := range []struct {
		host, kind, value string
		matched, enabled  bool
	}{
		{"LIBRARY.FUDAN.EDU.CN.", "suffix", "fudan.edu.cn", true, true},
		{"10.20.1.2", "cidr", "10.20.0.0/16", true, true},
		{"other.edu.cn", "", "", false, false},
		{"notfudan.edu.cn", "", "", false, false},
	} {
		got, err := rules.ExplainMatch(test.host)
		if err != nil || got.Kind != test.kind || got.Value != test.value || got.Matched != test.matched || got.Enabled != test.enabled {
			t.Fatalf("%s: %+v / %v", test.host, got, err)
		}
	}
	rules.Disabled.DomainSuffixes = []string{"fudan.edu.cn"}
	got, err := rules.ExplainMatch("library.fudan.edu.cn")
	if err != nil || !got.Matched || got.Enabled || got.Kind != "domain" {
		t.Fatalf("disabled: %+v / %v", got, err)
	}
}
