package routing

import (
	"net/netip"
	"slices"
)

// RuleMatch explains a saved rule; Matched may be true for a disabled rule.
type RuleMatch struct {
	Matched bool
	Kind    string
	Value   string
	Enabled bool
}

// ExplainMatch uses the router's policy without resolving the host. Enabled
// matches take precedence over disabled entries, which cannot veto other rules.
func (rules RuleSet) ExplainMatch(host string) (RuleMatch, error) {
	canonical, active, err := rules.canonicalPolicy()
	if err != nil {
		return RuleMatch{}, err
	}
	disabled := DisabledRules{}
	if canonical.Disabled != nil {
		disabled = *canonical.Disabled
	}
	activeMatch := active.match(host)
	for index, values := range [][]string{canonical.Domains, canonical.DomainSuffixes, canonical.IPCIDRs} {
		blocked := [][]string{disabled.Domains, disabled.DomainSuffixes, disabled.IPCIDRs}[index]
		for _, value := range values {
			enabled := !slices.Contains(blocked, value)
			if enabled != activeMatch {
				continue
			}
			candidate := policy{domains: map[string]bool{}}
			switch index {
			case 0:
				candidate.domains[value] = true
			case 1:
				candidate.suffixes = []string{value}
			case 2:
				prefix, _ := netip.ParsePrefix(value)
				candidate.prefixes = []netip.Prefix{prefix}
			}
			if candidate.match(host) {
				return RuleMatch{true, []string{"domain", "suffix", "cidr"}[index], value, enabled}, nil
			}
		}
	}
	return RuleMatch{}, nil
}
