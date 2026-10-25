import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CampusDiagnosticResult } from "./CampusDiagnostics";
import type { CampusDiagnosis } from "../lib/api";

vi.mock("../lib/api", () => ({ NetworkService: {} }));
vi.mock("@wailsio/runtime", () => ({ Events: {} }));

const diagnosis: CampusDiagnosis = {
  checkedAt: 1000, target: "https://library.fudan.edu.cn/catalog", host: "library.fudan.edu.cn",
  integration: "manual", state: "disconnected", connected: false, authRequired: true, coverage: "未开 TUN",
  rule: { known: true, matched: true, kind: "domain", value: "library.fudan.edu.cn", enabled: false, applied: false, message: "尚未同步" },
  expectedPath: "命中的校园规则已禁用", probe: null, problem: "当前入口不可用", suggestions: ["请重新登录。"],
};

describe("campus diagnosis result", () => {
  it("separates expected routing from missing evidence and shows disabled saved rules", () => {
    const html = renderToStaticMarkup(createElement(CampusDiagnosticResult, { diagnosis }));
    expect(html).toContain("预期路径");
    expect(html).toContain("实测证据");
    expect(html).toContain("已禁用");
    expect(html).toContain("尚未同步");
    expect(html).toContain("需重新登录");
    expect(html).toContain("当前入口不可用");
    expect(html).toContain("未执行网络探测");
    expect(html).toContain("请重新登录。");
  });
  it("shows HTTP evidence independently of tunnel readiness", () => {
    const html = renderToStaticMarkup(createElement(CampusDiagnosticResult, { diagnosis: {
      ...diagnosis, probe: { via: "Clash 当前配置", result: { targetId: "campus-url", name: "校园网址", host: diagnosis.host, provider: "campus", checkedAt: 1000, answers: [], httpStatus: 403, checks: [{ kind: "http", status: "auth", millis: 10, detail: "HTTP 403；需要认证" }] } },
    } }));
    expect(html).toContain("HTTP 403");
    expect(html).toContain("Clash 当前配置");
    expect(html).toContain("隧道未就绪");
    expect(html).not.toContain("未执行网络探测");
  });
});
