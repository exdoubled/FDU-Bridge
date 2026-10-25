package services

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

type diagnosisReportSnapshot struct {
	mu   sync.Mutex
	text string
}

// PreviewDiagnosisReport projects only shareable fields; raw details, configuration
// and logs are deliberately excluded rather than passed through log redaction.
func (s *NetworkService) PreviewDiagnosisReport() (string, error) {
	diagnosis, ok := s.latestDiagnosis()
	if !ok {
		return "", errors.New("请先完成一次校园网址诊断")
	}
	report := formatDiagnosisReport(diagnosis)
	s.report.mu.Lock()
	s.report.text = report
	s.report.mu.Unlock()
	return report, nil
}

// ExportDiagnosisReport exports the preview captured before opening the dialog.
func (s *NetworkService) ExportDiagnosisReport() (string, error) {
	return s.exportDiagnosisReport(func() (string, error) {
		return s.b.App.Dialog.SaveFile().SetFilename("fdu-diagnosis.txt").AddFilter("脱敏诊断报告", "*.txt").PromptForSingleSelection()
	})
}

func (s *NetworkService) exportDiagnosisReport(choose func() (string, error)) (string, error) {
	s.report.mu.Lock()
	report := s.report.text
	s.report.mu.Unlock()
	if report == "" {
		return "", errors.New("请先预览脱敏诊断报告")
	}
	path, err := choose()
	if err != nil || path == "" {
		return "", err
	}
	files, directories := s.b.exportPaths()
	target, err := fsx.ExportPath(path, files, directories)
	if err != nil {
		return "", err
	}
	if err := fsx.AtomicWrite(target, []byte(report)); err != nil {
		return "", err
	}
	return path, nil
}

var reportVersion = regexp.MustCompile(`^(dev|v?[0-9]+(\.[0-9]+){0,3}([-+][A-Za-z0-9.-]+)?)$`)

func publicDiagnosisHost(raw string) string {
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" {
			return "[已隐藏]"
		}
		raw = u.Hostname()
	}
	host := strings.TrimSuffix(strings.ToLower(raw), ".")
	if host == "fudan.edu.cn" || host == "www.fudan.edu.cn" {
		return host
	}
	for _, target := range netcheck.Targets() {
		if target.Provider == "campus" && host == target.Host {
			return host
		}
	}
	return "[已隐藏]"
}

func reportLabel(value string, labels map[string]string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	return "未确认"
}

func formatDiagnosisReport(d CampusDiagnosis) string {
	var out strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&out, format+"\n", args...) }
	version := updates.Version
	if len(version) > 64 || !reportVersion.MatchString(version) {
		version = "unknown"
	}
	line("FDU Bridge 脱敏诊断报告")
	line("版本: %s", version)
	line("平台: %s/%s", runtime.GOOS, runtime.GOARCH)
	line("目标主机: %s", publicDiagnosisHost(d.Host))
	line("接入方式: %s", reportLabel(d.Integration, map[string]string{"auto": "自动接入", "manual": "手动接入", "proxy": "仅本地代理"}))
	line("连接状态: %s", reportLabel(d.State, map[string]string{"connected": "已连接", "connecting": "连接中", "preparing": "准备中", "authentication": "认证中", "paused": "已暂停", "idle": "未连接", "stopped": "已停止", "campus-unavailable": "校园连接不可用"}))
	line("隧道已连接: %t", d.Connected)
	line("需要重新认证: %t", d.AuthRequired)
	line("代理覆盖: 入口检测不能确认所有应用均被代理覆盖")
	line("\n规则状态")
	if d.Rule.Matched {
		line("匹配: 已命中")
		line("类型: %s", reportLabel(d.Rule.Kind, map[string]string{"domain": "精确域名", "suffix": "域名后缀", "cidr": "IP / 网段"}))
		value := "[已隐藏]"
		if d.Rule.Kind == "domain" || d.Rule.Kind == "suffix" {
			value = publicDiagnosisHost(d.Rule.Value)
		}
		line("规则值: %s", value)
	} else {
		line("匹配: 未命中")
	}
	line("已启用: %t", d.Rule.Enabled)
	line("已应用: %t", d.Rule.Applied)
	if d.Integration == "proxy" {
		line("预期路径: 应用 → FDU 本地代理 → 校园资源；分流由外部应用管理")
	} else if (d.Integration == "auto" || d.Integration == "manual") && d.Rule.Matched && d.Rule.Enabled {
		line("预期路径: Clash 校园规则 → FDU → 校园资源；此为配置预期")
	} else {
		line("预期路径: 未确认由 FDU 转发")
	}
	line("\n实测证据（检测入口不代表最终路径）")
	dnsFailed, tlsFailed, checkFailed := false, false, false
	if d.Probe == nil {
		line("尚无可用检测结果")
	} else {
		line("检测入口: %s", reportLabel(d.Probe.Via, map[string]string{"系统路由": "系统路由", "Clash 当前配置": "Clash 当前配置", "FDU 本地入口": "FDU 本地入口"}))
		for _, check := range d.Probe.Result.Checks {
			name, known := map[string]string{"dns": "DNS", "tcp": "TCP", "tls": "TLS", "http": "HTTP", "websocket": "WebSocket"}[check.Kind]
			if !known {
				continue
			}
			line("%s: %s (%d ms)", name, reportLabel(check.Status, map[string]string{"ok": "通过", "error": "失败", "auth": "需认证 / 受限", "unsupported": "不支持", "skipped": "未尝试"}), max(0, check.Millis))
			dnsFailed = dnsFailed || check.Kind == "dns" && check.Status == "error"
			tlsFailed = tlsFailed || check.Kind == "tls" && check.Status == "error"
			checkFailed = checkFailed || check.Status == "error"
		}
		if status := d.Probe.Result.HTTPStatus; status >= 100 && status <= 599 {
			line("HTTP 状态: %d", status)
		}
	}
	line("\n处理建议")
	if d.AuthRequired {
		line("- 重新登录 UIS 后再检测。")
	}
	if !d.Connected {
		line("- 启用校内访问，确认隧道连接后再检测。")
	}
	if d.Integration == "proxy" {
		line("- 检查目标应用是否已使用 FDU 本地代理。")
	} else if d.Integration == "auto" || d.Integration == "manual" {
		if !d.Rule.Matched || !d.Rule.Enabled {
			line("- 在校内资源中添加或启用目标规则，保存后再检测。")
		} else if !d.Rule.Applied {
			line("- 保存校内资源并检查 Clash 规则同步状态。")
		}
	}
	if dnsFailed {
		line("- 检查 DNS 设置与当前网络，再重试解析。")
	}
	if tlsFailed {
		line("- 检查系统时间、目标证书及代理设置，不要关闭证书验证。")
	}
	if checkFailed || d.Probe == nil {
		line("- 确认检测入口可用后重试；结合本机诊断结果定位失败阶段。")
	}
	line("- HTTP 响应仅表示网络证据，资源访问仍受学校授权限制。")
	line("\n报告不包含账号、凭据、DNS 地址、个人路径、原始配置或完整日志；仅保留内置公开校园主机，自定义校园主机及 IP 同样隐藏。")
	return out.String()
}
