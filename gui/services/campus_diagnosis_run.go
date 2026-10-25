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
	diagnosis.ExpectedPath = "未命中启用的校园规则；不能预期经校园 VPN 访问"
	if !diagnosis.Rule.Known {
		diagnosis.ExpectedPath = "无法确认已保存校园规则，尚不能推断预期路径"
	}
	if diagnosis.Rule.Matched && !diagnosis.Rule.Enabled {
		diagnosis.ExpectedPath = "命中的校园规则已禁用；不能预期经校园 VPN 访问"
	}
	if diagnosis.Rule.Enabled {
		diagnosis.ExpectedPath = "配置预期：Clash 校园规则 → FDU → 校园 VPN；此处为规则推断"
		if !diagnosis.Rule.Applied {
			diagnosis.ExpectedPath += "，规则尚未确认同步"
		}
	}
	if mode == "proxy" {
		diagnosis.ExpectedPath = "本次检测使用 FDU 本地 HTTP 入口；校园分流由外部客户端管理，保存的规则不代表外部客户端已应用"
	}
	if !diagnosis.Connected {
		diagnosis.ExpectedPath += "；当前登录／隧道未就绪"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts, via, err := s.campusProbeOptions(mode, status.Running)
	if err != nil {
		diagnosis.Problem = err.Error()
	} else {
		result, probeErr := netcheck.ProbeCampus(ctx, target.URL, opts)
		if probeErr != nil {
			diagnosis.Problem = "无法开始网址检测，请检查输入和本机代理入口"
		} else {
			if opts.Lookup != nil {
				for i := range result.Checks {
					if result.Checks[i].Kind == "dns" {
						result.Checks[i].Detail = "Clash DNS 候选结果；" + result.Checks[i].Detail
					}
				}
			}
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
		suggestions = append(suggestions, "请先连接 FDU，并等待登录和隧道就绪后重新检测。")
	}
	if d.Integration != "proxy" {
		if !d.Rule.Known {
			suggestions = append(suggestions, "请在校内资源页重新读取并检查已保存规则，确认规则有效后再判断分流。")
		} else if !d.Rule.Matched {
			suggestions = append(suggestions, "请在校内资源页确认已保存规则；确认该站点属于校园资源后再添加域名规则。")
		} else if !d.Rule.Enabled {
			suggestions = append(suggestions, "请在校内资源页启用命中的规则并保存。")
		} else if !d.Rule.Applied {
			suggestions = append(suggestions, "规则尚未确认同步，请等待 FDU 和 Clash 完成同步后重试。")
		}
		if d.Coverage != "" {
			suggestions = append(suggestions, "请按代理覆盖提示检查 Clash TUN／系统代理，并确认浏览器使用当前 Clash 入口。")
		}
	}
	if d.Probe == nil {
		suggestions = append(suggestions, "请检查当前接入方式和本机代理入口；检测未改走系统直连。")
		return suggestions
	}
	for _, check := range d.Probe.Result.Checks {
		switch {
		case check.Kind == "dns" && check.Status == "error":
			suggestions = append(suggestions, "请检查当前 DNS 配置；DNS 候选查询失败不等于代理访问失败，请结合 HTTP 结果判断。")
		case check.Kind == "tls" && check.Status == "error":
			suggestions = append(suggestions, "请检查系统时间和站点证书，不要关闭证书验证。")
		case check.Kind == "http" && check.Status == "auth":
			suggestions = append(suggestions, "站点已响应但要求认证或限制访问，请在浏览器中完成站点登录并检查访问权限。")
		case check.Kind == "http" && check.Status == "error":
			suggestions = append(suggestions, "请结合 DNS／TCP／TLS 阶段定位失败；入口正常时仍失败，请检查目标服务状态。")
		case check.Kind == "http" && check.Status == "ok":
			suggestions = append(suggestions, "HTTP 已响应，仅证明本次指定入口可达；请在浏览器中验证实际访问路径和站点登录。")
		}
	}
	return suggestions
}
