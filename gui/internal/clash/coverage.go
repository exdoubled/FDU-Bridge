package clash

import (
	"net/netip"
	"path"
	"strconv"
	"strings"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/routing"
)

func bypassPrefix(pattern string) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(pattern); err == nil {
		return prefix.Masked(), true
	}
	if ip, err := netip.ParseAddr(pattern); err == nil {
		return netip.PrefixFrom(ip, ip.BitLen()), true
	}
	parts := strings.Split(pattern, ".")
	var octets []string
	for _, part := range parts {
		if part == "*" {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 255 {
			return netip.Prefix{}, false
		}
		octets = append(octets, part)
	}
	if len(octets) >= 4 || !strings.Contains(pattern, "*") {
		return netip.Prefix{}, false
	}
	bits := len(octets) * 8
	for len(octets) < 4 {
		octets = append(octets, "0")
	}
	prefix, err := netip.ParsePrefix(strings.Join(octets, ".") + "/" + strconv.Itoa(bits))
	return prefix, err == nil
}

func bypassMatches(pattern, rule string, suffix bool) bool {
	pattern, rule = strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(rule)
	if prefix, err := netip.ParsePrefix(rule); err == nil {
		if bypass, ok := bypassPrefix(pattern); ok {
			return prefix.Contains(bypass.Addr()) || bypass.Contains(prefix.Addr())
		}
		return false
	}
	if pattern == "<local>" {
		return !strings.Contains(rule, ".")
	}
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, ".") {
		pattern = "*" + pattern
	}
	if match, _ := path.Match(pattern, rule); match {
		return true
	}
	base := strings.TrimPrefix(pattern, "*.")
	if strings.HasPrefix(pattern, "*.") && rule == base {
		return true
	}
	return suffix && (base == rule || strings.HasSuffix(base, "."+rule) ||
		(strings.HasPrefix(pattern, "*.") && strings.HasSuffix(rule, "."+base)))
}

func bypassConflicts(patterns []string, file string) ([]string, error) {
	data, err := fsx.ReadBounded(file, 256*1024)
	if err != nil {
		return nil, err
	}
	rules, err := routing.ParseRules(data)
	if err != nil {
		return nil, err
	}
	disabled := routing.DisabledRules{}
	if rules.Disabled != nil {
		disabled = *rules.Disabled
	}
	var conflicts []string
	for _, pattern := range patterns {
		// Keep the narrow icourse compatibility bypass. Broad campus/IP
		// exclusions still need the normal coverage warning.
		compat := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
		if compat == "icourse.fudan.edu.cn" || compat == "*.icourse.fudan.edu.cn" || compat == ".icourse.fudan.edu.cn" {
			continue
		}
		matched := false
		for index, list := range [][]string{rules.Domains, rules.DomainSuffixes, rules.IPCIDRs} {
			blocked := [][]string{disabled.Domains, disabled.DomainSuffixes, disabled.IPCIDRs}[index]
			for _, rule := range list {
				if !contains(blocked, rule) && bypassMatches(pattern, rule, index == 1) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if matched && !contains(conflicts, pattern) {
			conflicts = append(conflicts, pattern)
		}
	}
	return conflicts, nil
}

func proxyCoverage(port int, rulesFile string) string {
	bypass, enabled, err := systemProxyBypass(port)
	if err != nil {
		return "未开 TUN，读不到系统代理设置（" + err.Error() + "）；请确认应用走 Clash 代理"
	}
	if !enabled {
		return "未开 TUN，系统代理未指向当前 Clash；请打开 Clash 的系统代理，或为应用手动设置代理"
	}
	if rulesFile != "" {
		conflicts, err := bypassConflicts(bypass, rulesFile)
		if err != nil {
			return "未开 TUN；无法读取校内规则以检查代理绕过，请查看诊断"
		}
		if len(conflicts) > 0 {
			if len(conflicts) > 4 {
				conflicts = append(conflicts[:4], "…")
			}
			return "部分校内资源被系统代理绕过：" + strings.Join(conflicts, "、") + "。请开启 Clash TUN，或在系统代理绕过列表中移除这些冲突项"
		}
	}
	return "使用 Clash 代理的应用（未开 TUN）"
}
