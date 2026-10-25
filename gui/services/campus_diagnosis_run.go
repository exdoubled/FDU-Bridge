package services

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"github.com/mythologyli/zju-connect/gui/internal/rules"
	"github.com/mythologyli/zju-connect/routing"
)

// DiagnoseCampus is read-only. Invalid input leaves the last completed snapshot
// intact; routing and network failures are returned as diagnostic evidence.
func (s *NetworkService) DiagnoseCampus(input string) (CampusDiagnosis, error) {
	target, err := netcheck.NormalizeCampusURL(input)
	if err != nil {
		return CampusDiagnosis{}, err
	}
	if !s.probeBusy.TryLock() {
		return CampusDiagnosis{}, errBusy
	}
	defer s.probeBusy.Unlock()
	status := s.b.Status()
	s.b.mu.Lock()
	mode := s.b.config.Integration.Mode
	s.b.mu.Unlock()
	if mode == "" {
		mode = "auto"
	}
	diagnosis := CampusDiagnosis{CheckedAt: time.Now().UnixMilli(), Target: target.URL, Host: target.Host,
		Integration: mode, State: status.State, Connected: status.Connected, AuthRequired: status.AuthRequired,
		Coverage: status.Coverage, Problem: status.Problem, Suggestions: []string{}}
	diagnosis.Rule = s.diagnoseRule(target.Host)
	diagnosis.ExpectedPath = "没有命中启用的规则，不经过校园 VPN"
	if !diagnosis.Rule.Known {
		diagnosis.ExpectedPath = "读取不到校内资源规则，无法判断路径"
	}
	if diagnosis.Rule.Matched && !diagnosis.Rule.Enabled {
		diagnosis.ExpectedPath = "命中的规则已停用，不经过校园 VPN"
	}
	if diagnosis.Rule.Enabled {
		diagnosis.ExpectedPath = "Clash 校园规则 → FDU → 校园 VPN"
		if !diagnosis.Rule.Applied {
			diagnosis.ExpectedPath += "（规则同步中）"
		}
	}
	if mode == "proxy" {
		diagnosis.ExpectedPath = "经 FDU 本地 HTTP 代理检测；分流由外部客户端决定"
	}
	if !diagnosis.Connected {
		diagnosis.ExpectedPath += "；隧道未就绪"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts, via, err := s.campusProbeOptions(mode, status.Running)
	if err != nil {
		if diagnosis.Problem != "" {
			diagnosis.Problem += "；"
		}
		diagnosis.Problem += err.Error()
	} else {
		result, probeErr := netcheck.ProbeCampus(ctx, target.URL, opts)
		if probeErr != nil {
			if diagnosis.Problem != "" {
				diagnosis.Problem += "；"
			}
			diagnosis.Problem += "无法开始网址检测，请检查输入和本机代理入口"
		} else {
			diagnosis.Probe = &ProbeReport{Result: result, Via: via}
		}
	}
	diagnosis.Suggestions = campusSuggestions(diagnosis)
	s.saveDiagnosis(diagnosis)
	return diagnosis, nil
}

func (s *NetworkService) campusProbeOptions(mode string, running bool) (netcheck.Options, string, error) {
	if mode == "proxy" {
		if !running {
			return netcheck.Options{}, "", errors.New("FDU 未运行，未尝试系统直连；请先连接 FDU")
		}
	} else if _, err := s.controller(); err != nil {
		return netcheck.Options{}, "", errors.New("当前 Clash 控制接口不可用，未切换检测路径；请启动所选 Clash 客户端并检查控制接口")
	}
	opts, via, err := s.probeOptions(running)
	expectedVia := "Clash 当前配置"
	if mode == "proxy" {
		expectedVia = "FDU 本地入口"
	}
	if err != nil || opts.ProxyURL == "" || via != expectedVia {
		return netcheck.Options{}, "", errors.New("当前代理检测入口不可用，未切换检测路径；请检查所选客户端的 HTTP/mixed 端口")
	}
	return opts, via, nil
}

func (s *NetworkService) diagnoseRule(host string) CampusRuleDiagnosis {
	service := NewRulesService(s.b)
	s.b.mu.Lock()
	path := text(s.b.saved, "routing.rules_file")
	if path == "" {
		path = filepath.Join(s.b.Dir, "routing-rules.json")
	} else if !filepath.IsAbs(path) {
		base := s.b.Dir
		if s.b.workspace != nil {
			base = filepath.Dir(s.b.workspace.Target)
		}
		path = filepath.Join(base, path)
	}
	if s.b.enabled && s.b.campus != nil && s.b.campus.Rules != "" {
		path = s.b.campus.Rules
	}
	s.b.mu.Unlock()
	store, err := rules.New(path)
	if err != nil {
		return CampusRuleDiagnosis{Message: "无法定位已保存校园规则，请检查设置"}
	}
	snapshot, err := store.Load()
	if err == nil && snapshot.Missing {
		// Only the last saved configuration may supply defaults. The editor's
		// current View can contain a draft that the running core has never seen.
		s.b.mu.Lock()
		raw, encodeErr := json.Marshal(s.b.saved["routing"])
		s.b.mu.Unlock()
		document := rules.Empty()
		if encodeErr != nil {
			err = encodeErr
		} else if err = json.Unmarshal(raw, &document); err == nil {
			snapshot.Document, err = rules.Normalize(document)
		}
	}
	if err != nil {
		return CampusRuleDiagnosis{Message: "无法读取已保存校园规则，请在校内资源页重新读取并检查规则文件"}
	}
	doc := snapshot.Document
	match, err := (routing.RuleSet{Version: 2, Domains: doc.Domains, DomainSuffixes: doc.DomainSuffixes, IPCIDRs: doc.IPCIDRs,
		Disabled: &routing.DisabledRules{Domains: doc.Disabled.Domains, DomainSuffixes: doc.Disabled.DomainSuffixes, IPCIDRs: doc.Disabled.IPCIDRs}}).ExplainMatch(host)
	if err != nil {
		return CampusRuleDiagnosis{Message: "已保存校园规则无效，请在校内资源页检查"}
	}
	state := service.applyStatus(store, snapshot.Hash)
	return CampusRuleDiagnosis{Known: true, Matched: match.Matched, Kind: match.Kind, Value: match.Value, Enabled: match.Enabled, Applied: state.Applied, Message: state.Message}
}

func campusSuggestions(d CampusDiagnosis) []string {
	suggestions := []string{}
	if d.AuthRequired {
		suggestions = append(suggestions, "登录已失效，请重新登录 UIS 后重试。")
	} else if !d.Connected {
		suggestions = append(suggestions, "先在首页开启校内访问，连接就绪后再检测。")
	}
	if d.Integration != "proxy" {
		if !d.Rule.Known {
			suggestions = append(suggestions, "到校内资源页重新读取规则。")
		} else if !d.Rule.Matched {
			suggestions = append(suggestions, "如果这是校内网站，到校内资源页添加它的域名。")
		} else if !d.Rule.Enabled {
			suggestions = append(suggestions, "到校内资源页启用这条规则并保存。")
		} else if !d.Rule.Applied {
			suggestions = append(suggestions, "规则正在同步，稍后再试。")
		}
	}
	if d.Probe == nil {
		suggestions = append(suggestions, "检查接入方式和本机代理端口。")
		return suggestions
	}
	for _, check := range d.Probe.Result.Checks {
		switch {
		case check.Kind == "dns" && check.Status == "error":
			suggestions = append(suggestions, "DNS 解析失败；HTTP 正常时可以忽略。")
		case check.Kind == "tls" && check.Status == "error":
			suggestions = append(suggestions, "检查系统时间和网站证书。")
		case check.Kind == "http" && check.Status == "auth":
			suggestions = append(suggestions, "网站要求登录或没有访问权限，在浏览器中登录后再试。")
		case check.Kind == "http" && check.Status == "error":
			if d.Integration != "proxy" && d.Coverage != "" {
				suggestions = append(suggestions, "检查 Clash 的 TUN 或系统代理是否开启，并确认浏览器走 Clash。")
			}
			suggestions = append(suggestions, "看是哪一步失败；前面几步都通过时，多半是网站本身不可用。")
		case check.Kind == "http" && check.Status == "ok":
			suggestions = append(suggestions, "网站可以访问；浏览器仍打不开时，确认浏览器走 Clash。")
		}
	}
	return suggestions
}
