package services

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mythologyli/zju-connect/gui/internal/appconf"
	"github.com/mythologyli/zju-connect/gui/internal/netcheck"
	"github.com/mythologyli/zju-connect/gui/internal/updates"
)

func TestDiagnosisReportProjectsOnlySafeFields(t *testing.T) {
	private := "account123 token=top-secret Cookie=session 10.18.7.3 C:\\Users\\Alice\\private"
	d := CampusDiagnosis{Target: "https://alice:password@library.fudan.edu.cn/private?token=secret#fragment", Host: "library.fudan.edu.cn", Integration: "manual", State: private,
		Connected: true, Coverage: private, ExpectedPath: private, Problem: private, Suggestions: []string{private},
		Rule: CampusRuleDiagnosis{Known: true, Matched: true, Kind: "suffix", Value: "fudan.edu.cn", Enabled: true, Applied: true, Message: private},
		Probe: &ProbeReport{Via: private, Result: netcheck.Result{Host: private, Name: private, TargetID: private, Answers: []string{"10.18.7.3"}, HTTPStatus: 401,
			Checks: []netcheck.Check{{Kind: "dns", Status: "ok", Millis: 12, Detail: private}, {Kind: "tls", Status: "error", Millis: 34, Detail: private}, {Kind: private, Status: private, Detail: private}}}}}
	s := NewNetworkService(&Backend{logs: []string{private}, saved: map[string]any{"username": private}})
	s.saveDiagnosis(d)
	report, err := s.PreviewDiagnosisReport()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{updates.Version, runtime.GOOS + "/" + runtime.GOARCH, "library.fudan.edu.cn", "fudan.edu.cn", "手动接入", "DNS: 通过 (12 ms)", "TLS: 失败 (34 ms)", "HTTP 状态: 401", "已命中", "证书"} {
		if !strings.Contains(report, want) {
			t.Errorf("missing useful report field %q in %s", want, report)
		}
	}
	for _, secret := range []string{private, "account123", "top-secret", "session", "10.18.7.3", "Alice", "password", "/private", "?token", "#fragment"} {
		if strings.Contains(report, secret) {
			t.Errorf("report leaked %q: %s", secret, report)
		}
	}
}

func TestDiagnosisReportLabelsKnownConnectionStatesAndHidesUnknownState(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"idle", "未连接"},
		{"preparing", "准备中"},
		{"connecting", "连接中"},
		{"enabling", "启用中"},
		{"adapting", "适配 Clash 中"},
		{"authentication", "认证中"},
		{"reconnecting", "恢复连接中"},
		{"auth-required", "需要重新登录"},
		{"connected", "已连接"},
		{"paused", "已暂停"},
		{"error", "启用失败"},
		{"campus-unavailable", "校园连接不可用"},
		{"stopped", "已停止"},
		{"tool-running", "正在执行工具"},
		{"tool-finished", "工具已结束"},
		{"unrecognized-state", "未确认"},
		{"error\nCookie=state-secret 10.18.7.3 C:\\Users\\Alice", "未确认"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			s := NewNetworkService(&Backend{})
			s.saveDiagnosis(CampusDiagnosis{State: tc.state})
			report, err := s.PreviewDiagnosisReport()
			if err != nil || !strings.Contains(report, "\n连接状态: "+tc.want+"\n") {
				t.Fatalf("state %q did not produce its safe label %q: %q %v", tc.state, tc.want, report, err)
			}
			if tc.want == "未确认" && strings.Contains(report, tc.state) {
				t.Fatalf("report leaked an unknown raw state: %q", report)
			}
		})
	}
}

func TestDiagnosisReportHidesCustomHostsAndURLSecrets(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"https://alice:secret@LIBRARY.FUDAN.EDU.CN/path?token=secret#secret", "library.fudan.edu.cn"},
		{"fudan.edu.cn.", "fudan.edu.cn"},
		{"www.fudan.edu.cn", "www.fudan.edu.cn"},
		{"personal-lab.fudan.edu.cn", "[已隐藏]"},
		{"10.9.8.7", "[已隐藏]"}, {"2001:db8::1", "[已隐藏]"}, {"private.example", "[已隐藏]"},
		{"fudan.edu.cn.evil.example", "[已隐藏]"}, {"evilfudan.edu.cn", "[已隐藏]"}, {"secret\nfudan.edu.cn", "[已隐藏]"},
	} {
		t.Run(tc.host, func(t *testing.T) {
			s := NewNetworkService(&Backend{})
			s.saveDiagnosis(CampusDiagnosis{Host: tc.host, Rule: CampusRuleDiagnosis{Known: true, Matched: true, Kind: "cidr", Value: "10.9.8.0/24"}})
			report, err := s.PreviewDiagnosisReport()
			if err != nil || !strings.Contains(report, "目标主机: "+tc.want) {
				t.Fatalf("safe host: %q %v", report, err)
			}
			for _, secret := range []string{"alice", "secret", "/path", "10.9.8.0", "private.example", "evil.example"} {
				if strings.Contains(report, secret) {
					t.Errorf("host projection leaked %q", secret)
				}
			}
		})
	}
}

func TestDiagnosisReportExportUsesPreviewEvenWhenDiagnosisChanges(t *testing.T) {
	s := NewNetworkService(&Backend{Dir: t.TempDir()})
	if _, err := s.PreviewDiagnosisReport(); err == nil {
		t.Fatal("preview accepted missing diagnosis")
	}
	called := false
	if _, err := s.exportDiagnosisReport(func() (string, error) { called = true; return "", nil }); err == nil || called {
		t.Fatal("export opened dialog without a preview")
	}
	s.saveDiagnosis(CampusDiagnosis{Host: "library.fudan.edu.cn"})
	preview, err := s.PreviewDiagnosisReport()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "diagnosis.txt")
	path, err := s.exportDiagnosisReport(func() (string, error) {
		s.saveDiagnosis(CampusDiagnosis{Host: "icourse.fudan.edu.cn"})
		if _, err := s.PreviewDiagnosisReport(); err != nil {
			t.Fatal(err)
		}
		return output, nil
	})
	data, readErr := os.ReadFile(output)
	if err != nil || readErr != nil || path != output || string(data) != preview || strings.Contains(string(data), "icourse") {
		t.Fatalf("export changed preview: %q %v %v", data, err, readErr)
	}
	refreshed, err := s.PreviewDiagnosisReport()
	if err != nil || !strings.Contains(refreshed, "icourse.fudan.edu.cn") {
		t.Fatalf("new preview did not refresh: %q %v", refreshed, err)
	}
}

func TestDiagnosisReportUnknownIntegrationDoesNotClaimClashPath(t *testing.T) {
	s := NewNetworkService(&Backend{})
	s.saveDiagnosis(CampusDiagnosis{Integration: "unrecognized mode", Rule: CampusRuleDiagnosis{Known: true, Matched: true, Enabled: true}})
	report, err := s.PreviewDiagnosisReport()
	if err != nil || strings.Contains(report, "预期路径: Clash") || !strings.Contains(report, "预期路径: 未确认") {
		t.Fatalf("unknown integration claimed a route: %q %v", report, err)
	}
}

func TestDiagnosisReportDistinguishesUnknownRulesFromConfirmedMiss(t *testing.T) {
	for _, tc := range []struct {
		name            string
		rule            CampusRuleDiagnosis
		want, forbidden string
	}{
		{"unknown", CampusRuleDiagnosis{}, "匹配: 无法确认", "匹配: 未命中"},
		{"unknown with stale match fields", CampusRuleDiagnosis{Matched: true, Enabled: true, Applied: true, Kind: "domain", Value: "library.fudan.edu.cn"}, "匹配: 无法确认", "匹配: 已命中"},
		{"confirmed miss", CampusRuleDiagnosis{Known: true}, "匹配: 未命中", "匹配: 无法确认"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewNetworkService(&Backend{})
			s.saveDiagnosis(CampusDiagnosis{Integration: "auto", Rule: tc.rule})
			report, err := s.PreviewDiagnosisReport()
			if err != nil || !strings.Contains(report, tc.want) || strings.Contains(report, tc.forbidden) {
				t.Fatalf("incorrect rule knowledge: %q %v", report, err)
			}
			if !tc.rule.Known {
				for _, forbidden := range []string{"添加或启用目标规则", "预期路径: Clash", "已启用: true", "已应用: true", "规则值:"} {
					if strings.Contains(report, forbidden) {
						t.Errorf("unknown rules made an unsupported claim: %q", forbidden)
					}
				}
				if !strings.Contains(report, "检查校内资源能否正常读取") {
					t.Error("missing safe rule verification suggestion")
				}
			} else if !strings.Contains(report, "添加或启用目标规则") {
				t.Error("confirmed miss lost its actionable suggestion")
			}
		})
	}
}

func TestDiagnosisReportExportCancellationAndProtection(t *testing.T) {
	dir, external := t.TempDir(), t.TempDir()
	managed := filepath.Join(external, "runtime.yaml")
	if err := os.WriteFile(managed, []byte("private config"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewNetworkService(&Backend{Dir: dir, config: appconf.Config{Integration: appconf.Integration{RuntimeFile: managed}}})
	s.saveDiagnosis(CampusDiagnosis{})
	if _, err := s.PreviewDiagnosisReport(); err != nil {
		t.Fatal(err)
	}
	if path, err := s.exportDiagnosisReport(func() (string, error) { return "", nil }); err != nil || path != "" {
		t.Fatalf("cancelled export: %q %v", path, err)
	}
	dialogErr := errors.New("dialog unavailable")
	if _, err := s.exportDiagnosisReport(func() (string, error) { return "", dialogErr }); !errors.Is(err, dialogErr) {
		t.Fatalf("lost dialog error: %v", err)
	}
	for _, blocked := range []string{managed, filepath.Join(dir, "client.toml"), filepath.Join(dir, "connection-backups", "nested", "report.txt")} {
		if _, err := s.exportDiagnosisReport(func() (string, error) { return blocked, nil }); err == nil {
			t.Errorf("accepted managed destination %s", blocked)
		}
	}
	if data, _ := os.ReadFile(managed); string(data) != "private config" {
		t.Fatal("overwrote runtime config")
	}
}
