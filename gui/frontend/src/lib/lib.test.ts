import { describe, expect, it } from "vitest";
import { addRule, counts, emptyDraft, filterRows, mergeDraft, removeRules, rows, sameDraft, setEnabled } from "./rulesDraft";
import { availability, getValue, parseList, prepareForSave, setValue, tableRows } from "./settingsForm";
import { formatUptime, stateLabel, stateTone } from "./status";

describe("rules draft", () => {
  it("adds, toggles and removes without mutating the original", () => {
    const base = emptyDraft();
    const added = addRule(base, "cidr", "10.0.0.0/8")!;
    expect(base.ip_cidrs).toEqual([]);
    expect(addRule(added, "cidr", "10.0.0.0/8")).toBeNull();
    const off = setEnabled(added, "cidr", "10.0.0.0/8", false);
    expect(rows(off)).toEqual([{ type: "cidr", value: "10.0.0.0/8", enabled: false }]);
    expect(added.disabled.ip_cidrs).toEqual([]);
    const on = setEnabled(off, "cidr", "10.0.0.0/8", true);
    expect(on.disabled.ip_cidrs).toEqual([]);
    const removed = removeRules(off, [{ type: "cidr", value: "10.0.0.0/8" }]);
    expect(removed.ip_cidrs).toEqual([]);
    expect(removed.disabled.ip_cidrs).toEqual([]);
  });

  it("keeps the same value separate across rule types", () => {
    let d = addRule(emptyDraft(), "domain", "lab.fudan.edu.cn")!;
    d = addRule(d, "suffix", "lab.fudan.edu.cn")!;
    d = setEnabled(d, "domain", "lab.fudan.edu.cn", false);
    expect(rows(d).map((r) => r.enabled)).toEqual([false, true]);
    expect(counts(d)).toEqual({ total: 2, enabled: 1, disabled: 1 });
    expect(filterRows(rows(d), "suffix", "")).toHaveLength(1);
    expect(filterRows(rows(d), "all", "LAB")).toHaveLength(2);
  });

  it("compares drafts ignoring disabled order", () => {
    let a = addRule(addRule(emptyDraft(), "domain", "a.cn")!, "domain", "b.cn")!;
    a = setEnabled(setEnabled(a, "domain", "a.cn", false), "domain", "b.cn", false);
    let b = addRule(addRule(emptyDraft(), "domain", "a.cn")!, "domain", "b.cn")!;
    b = setEnabled(setEnabled(b, "domain", "b.cn", false), "domain", "a.cn", false);
    expect(sameDraft(a, b)).toBe(true);
    expect(sameDraft(a, emptyDraft())).toBe(false);
  });

  it("merges typed entries while keeping the current draft switches and edits", () => {
    const current = { ...emptyDraft(), domains: ["lab.fudan.edu.cn", "draft.cn"], domain_suffixes: ["lab.fudan.edu.cn"],
      disabled: { domains: ["lab.fudan.edu.cn"], domain_suffixes: [], ip_cidrs: [] } };
    const imported = { ...emptyDraft(), domains: ["lab.fudan.edu.cn", "new.cn"], domain_suffixes: ["lab.fudan.edu.cn", "new.cn"], ip_cidrs: ["10.0.0.0/8"],
      disabled: { domains: ["new.cn"], domain_suffixes: ["lab.fudan.edu.cn"], ip_cidrs: ["10.0.0.0/8"] } };
    const merged = mergeDraft(current, imported);
    expect(merged.domains).toEqual(["lab.fudan.edu.cn", "draft.cn", "new.cn"]);
    expect(merged.disabled).toEqual({ domains: ["lab.fudan.edu.cn", "new.cn"], domain_suffixes: [], ip_cidrs: ["10.0.0.0/8"] });
    expect(merged.domain_suffixes).toEqual(["lab.fudan.edu.cn", "new.cn"]);
    expect(current.domains).toEqual(["lab.fudan.edu.cn", "draft.cn"]);
    expect(imported.disabled.domain_suffixes).toEqual(["lab.fudan.edu.cn"]);
  });
});

describe("settings form", () => {
  it("reads and writes dotted keys immutably", () => {
    const values = { routing: { enabled: false, rules_file: "x" }, protocol: "atrust" };
    const next = setValue(values, "routing.enabled", true);
    expect(getValue(next, "routing.enabled")).toBe(true);
    expect(getValue(next, "routing.rules_file")).toBe("x");
    expect(values.routing.enabled).toBe(false);
  });

  it("disables protocol-specific and routing-incompatible fields", () => {
    const atrust = { protocol: "atrust", routing: { enabled: true } };
    expect(availability({ key: "tun_mode", label: "", group: "网络", kind: "bool" }, atrust).enabled).toBe(false);
    expect(availability({ key: "bind_interface", label: "", group: "网络", kind: "text" }, atrust).enabled).toBe(true);
    expect(availability({ key: "cert_file", label: "", group: "EasyConnect", kind: "file" }, atrust).enabled).toBe(false);
    expect(availability({ key: "sid", label: "", group: "aTrust", kind: "secret" }, { protocol: "easyconnect" }).enabled).toBe(false);
  });

  it("validates and converts numbers before saving", () => {
    const fields = [{ key: "server_port", label: "端口", group: "登录", kind: "number" }];
    expect(prepareForSave(fields, { server_port: "443" })).toEqual({ values: { server_port: 443 }, error: null });
    expect(prepareForSave(fields, { server_port: "-1" }).error).toContain("非负整数");
    expect(prepareForSave(fields, { server_port: "1.5" }).error).toContain("非负整数");
  });

  it("parses list and table values", () => {
    expect(parseList(" a.cn \r\n\n b.cn ")).toEqual(["a.cn", "b.cn"]);
    expect(tableRows([{ host_name: "a", ip: "1.1.1.1" }], [{ key: "host_name" }, { key: "ip" }])).toEqual([{ host_name: "a", ip: "1.1.1.1" }]);
    expect(tableRows(null, [{ key: "ip" }])).toEqual([]);
  });
});

describe("status", () => {
  const base = { state: "idle", running: false, starting: false, connected: false, external: false, stopping: false };
  it("labels and colours states", () => {
    expect(stateLabel({ ...base, state: "connected", running: true, connected: true })).toBe("校内访问已启用");
    expect(stateTone({ ...base, state: "connected", running: true, connected: true })).toBe("on");
    expect(stateLabel({ ...base, external: true })).toBe("其他 FDU 正在运行");
    expect(stateTone({ ...base, state: "connecting", running: true })).toBe("busy");
    expect(stateTone({ ...base, state: "campus-unavailable", running: true })).toBe("bad");
    expect(stateLabel({ ...base, state: "connected" })).toBe("未启用校内访问");
  });
  it("keeps preparation busy and paused routing visibly unavailable", () => {
    expect(stateLabel({ ...base, state: "preparing", starting: true })).toBe("正在准备校内访问…");
    expect(stateTone({ ...base, state: "preparing", starting: true })).toBe("busy");
    expect(stateLabel({ ...base, state: "paused", running: true })).toBe("校内访问已暂停");
    expect(stateTone({ ...base, state: "paused", running: true })).toBe("bad");
    expect(stateTone({ ...base, state: "error" })).toBe("bad");
    expect(stateTone({ ...base, state: "connected", running: true, connected: true, stopping: true })).toBe("busy");
  });
  it("formats uptime", () => {
    expect(formatUptime(0, 3_723_000)).toBe("01:02:03");
  });
});
