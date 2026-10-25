import { useState } from "react";
import { Stethoscope } from "lucide-react";
import { NetworkService, errorText, type CampusDiagnosis } from "../lib/api";
import { ProbeDetails } from "./NetworkTools";
import { CardTitle, Hint, Spinner } from "./ui";
import { DiagnosisReport } from "./DiagnosisReport";

const diagnosisHelp = "只访问网址本身（去掉 ? 和 # 之后的部分），不带登录信息，不改任何设置。";
const integrationNames: Record<string, string> = { auto: "自动接入 Clash", manual: "手动接入 Clash", proxy: "仅本地代理" };
const ruleNames: Record<string, string> = { domain: "精确域名", suffix: "域名及子域名", cidr: "IPv4 网段" };

// The input already shows the diagnosed URL; repeat it only once the input has been edited.
export function CampusDiagnosticResult({ diagnosis: d, input }: { diagnosis: CampusDiagnosis; input?: string }) {
  return (
    <div className="campus-diagnosis-result" aria-live="polite">
      {input !== undefined && input.trim() !== d.target && <div className="muted selectable">以下为 <span className="mono">{d.target}</span> 的结果</div>}
      <div className="diagnosis-columns">
        <section aria-label="预期路径">
          <h3>预期路径</h3>
          <p className="selectable">{d.expectedPath}</p>
          <dl className="diagnosis-facts">
            <dt>接入方式</dt><dd>{integrationNames[d.integration] ?? d.integration}</dd>
            <dt>登录／隧道</dt><dd>{d.authRequired ? "需重新登录" : d.connected ? "已登录" : "未登录"} · {d.connected ? "隧道已就绪" : "隧道未就绪"}</dd>
            <dt>保存的规则</dt><dd>{!d.rule.known ? "无法确认" : d.rule.matched ? <><span className={d.rule.enabled ? "success-text" : "warning-text"}>{d.rule.enabled ? "已启用" : "已禁用"}</span> · {ruleNames[d.rule.kind] ?? d.rule.kind} <span className="mono selectable">{d.rule.value}</span></> : "未命中"}</dd>
            <dt>规则同步</dt><dd>{d.rule.message || (d.rule.applied ? "已同步" : "同步中")}</dd>
            <dt>代理覆盖</dt><dd>{d.coverage || "—"}</dd>
          </dl>
        </section>
        <section aria-label="实测证据">
          <h3>实测证据<Hint text="从 Clash 当前入口发起访问；浏览器也走 Clash 时，结果与浏览器一致。" /></h3>
          {d.probe ? <ProbeDetails report={d.probe} /> : <div className="muted">未执行网络探测</div>}
          {d.problem && <p className="field-error selectable">{d.problem}</p>}
        </section>
      </div>
      {!!d.suggestions?.length && <div className="diagnosis-suggestions"><h3>处理建议</h3><ul>{d.suggestions.map((suggestion, i) => <li key={i}>{suggestion}</li>)}</ul></div>}
    </div>
  );
}

export function CampusDiagnostics() {
  const [input, setInput] = useState("");
  const [diagnosis, setDiagnosis] = useState<CampusDiagnosis>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const diagnose = async () => {
    if (busy || !input.trim()) return;
    setBusy(true);
    setError("");
    try {
      const result = await NetworkService.DiagnoseCampus(input);
      setDiagnosis(result);
      setInput(result.target);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="card campus-diagnostics">
      <CardTitle icon={Stethoscope}>校园网址一键诊断<Hint text={diagnosisHelp} /></CardTitle>
      <form className="diagnosis-input" onSubmit={(e) => { e.preventDefault(); void diagnose(); }}>
        <input id="campus-diagnosis-url" className="input" aria-label="校园网址或域名" value={input} onChange={(e) => setInput(e.target.value)} placeholder="https://jwfw.fudan.edu.cn 或校园域名" title={diagnosisHelp} autoComplete="off" spellCheck={false} disabled={busy} />
        <button className="btn primary" type="submit" disabled={busy || !input.trim()}>{busy ? <><Spinner />检测中…</> : "开始诊断"}</button>
      </form>
      {error && <div className="field-error" role="alert">{error}{diagnosis && "（下方保留上次结果）"}</div>}
      {diagnosis && <>
        <CampusDiagnosticResult diagnosis={diagnosis} input={input} />
        <div className="diagnosis-actions">
          <DiagnosisReport key={diagnosis.checkedAt} disabled={busy} />
          <span className="faint">检测于 {new Date(diagnosis.checkedAt).toLocaleString("zh-CN", { hour12: false })}</span>
        </div>
      </>}
    </div>
  );
}
