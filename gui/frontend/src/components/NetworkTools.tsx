import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Activity, ArrowDown, ArrowUp, ChevronRight, Info, RefreshCw } from "lucide-react";
import { NetworkService, errorText, type NetworkTarget, type NetworkView, type ProbeReport, type TrafficBucket } from "../lib/api";
import { useStore } from "../store";
import { CardTitle, Spinner } from "./ui";

const campusTargets = [
  { id: "library", name: "图书馆", host: "library.fudan.edu.cn" },
  { id: "icourse", name: "复旦课程", host: "icourse.fudan.edu.cn" },
];
const compatibilityNote = "icourse 在开启系统代理时可能无法访问。如遇此情况，请将 icourse.fudan.edu.cn 加入系统代理绕过列表；校外访问仍需校园网或有效的 TUN/FDU 路径。";

interface NetworkToolsState {
  view: NetworkView | null;
  observeError: string;
  targets: NetworkTarget[];
  targetsError: string;
  reports: Record<string, ProbeReport>;
  errors: Record<string, string>;
  busy: boolean;
  probing: string;
  probe: (ids: string[]) => void;
}

const Context = createContext<NetworkToolsState | null>(null);

export function useNetworkTools() {
  const state = useContext(Context);
  if (!state) throw new Error("network tools missing");
  return state;
}

export function NetworkToolsProvider({ children }: { children: ReactNode }) {
  const { page, status } = useStore();
  const [view, setView] = useState<NetworkView | null>(null);
  const [observeError, setObserveError] = useState("");
  const [targets, setTargets] = useState<NetworkTarget[]>([]);
  const [targetsError, setTargetsError] = useState("");
  const [reports, setReports] = useState<Record<string, ProbeReport>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [probing, setProbing] = useState("");
  const observing = useRef(false);
  const queue = useRef(Promise.resolve());
  const pending = useRef(0);
  const wasConnected = useRef(false);
  const active = page === "home";

  useEffect(() => {
    let live = true;
    NetworkService.Targets().then((items) => {
      if (live) { setTargets(items); setTargetsError(""); }
    }).catch((e) => { if (live) setTargetsError(errorText(e)); });
    return () => { live = false; };
  }, []);

  useEffect(() => {
    if (!active) return;
    let live = true;
    const observe = async () => {
      if (document.hidden || observing.current) return;
      observing.current = true;
      try {
        const next = await NetworkService.Observe();
        if (live) { setView(next); setObserveError(""); }
      } catch (e) {
        if (live) setObserveError(errorText(e));
      } finally {
        observing.current = false;
      }
    };
    const visible = () => { if (!document.hidden) void observe(); };
    void observe();
    const timer = setInterval(observe, 1000);
    document.addEventListener("visibilitychange", visible);
    return () => {
      live = false;
      clearInterval(timer);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [active]);

  const runProbes = useCallback((ids: string[]) => {
    if (!ids.length || pending.current > 0) return;
    pending.current++;
    setBusy(true);
    queue.current = queue.current.then(async () => {
      for (const id of ids) {
        setProbing(id);
        setErrors((current) => ({ ...current, [id]: "" }));
        try {
          const report = await NetworkService.Probe(id);
          setReports((current) => ({ ...current, [id]: report }));
        } catch (e) {
          setErrors((current) => ({ ...current, [id]: errorText(e) }));
        }
      }
    }).finally(() => {
      pending.current--;
      if (pending.current === 0) { setBusy(false); setProbing(""); }
    });
  }, []);

  useEffect(() => {
    if (status.connected && !wasConnected.current) runProbes(campusTargets.map((target) => target.id));
    wasConnected.current = status.connected;
  }, [status.connected, runProbes]);

  return <Context.Provider value={{ view, observeError, targets, targetsError, reports, errors, busy, probing, probe: runProbes }}>{children}</Context.Provider>;
}

export function formatBytes(value: number): string {
  const bytes = Math.max(0, Number.isFinite(value) ? value : 0);
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length);
  return `${(bytes / 1024 ** power).toFixed(power === 1 ? 0 : 1)} ${units[power - 1]}`;
}

function TrafficCard({ label, bucket }: { label: string; bucket?: TrafficBucket }) {
  return (
    <section className="card traffic-card">
      <CardTitle icon={Activity} extra={<span className="traffic-count">{bucket ? bucket.connections : "—"} 连接</span>}>{label}</CardTitle>
      <div className="traffic-rates">
        <div><ArrowUp size={15} /><strong>{bucket ? formatBytes(bucket.uploadRate) : "—"}</strong><span>/s</span></div>
        <div><ArrowDown size={15} /><strong>{bucket ? formatBytes(bucket.downloadRate) : "—"}</strong><span>/s</span></div>
      </div>
      <div className="traffic-total">{label === "非 FDU" ? "累计估算" : "累计"} <span>↑ {bucket ? formatBytes(bucket.upload) : "—"}</span><span>↓ {bucket ? formatBytes(bucket.download) : "—"}</span></div>
    </section>
  );
}

export function TrafficCards() {
  const { view, observeError } = useNetworkTools();
  const sources = [view?.coreAvailable && "FDU", view?.clashAvailable && "Clash"].filter(Boolean).join(" / ");
  return (
    <div className="traffic-section">
      <div className="grid-2">
        <TrafficCard label="FDU" bucket={view?.fdu} />
        <TrafficCard label="非 FDU" bucket={view?.nonFdu} />
      </div>
      <div className={`network-caption${observeError ? " warning-text" : ""}`} title={observeError || view?.scope}>
        {observeError ? view ? "采样失败 · 显示上次数据" : "暂时无法读取流量" : !view ? "正在读取流量…" : sources ? `本次运行 · ${sources} 可观测流量` : "暂无可用流量来源"}
        {view?.truncated && <span> · 连接列表已截断</span>}
      </div>
    </div>
  );
}

const checkNames: Record<string, string> = { dns: "DNS", tcp: "TCP", tls: "TLS", http: "HTTP", websocket: "WS" };
const checkLabels: Record<string, string> = { ok: "通过", error: "失败", auth: "需认证", unsupported: "不支持" };
const checkTones: Record<string, string> = { ok: "success", error: "danger", auth: "warning", unsupported: "" };

function ProbeStatus({ report, error, loading }: { report?: ProbeReport; error?: string; loading: boolean }) {
  if (loading) return <span className="probe-status muted"><Spinner />检测中</span>;
  if (error) return <span className="pill danger">检测失败</span>;
  if (!report) return <span className="muted">未检测</span>;
  const checks = report.result.checks ?? [];
  const http = checks.find((check) => check.kind === "http");
  const dnsError = checks.some((check) => check.kind === "dns" && check.status === "error");
  let tone = "", label = "已检测";
  if (http?.status === "ok") {
    tone = dnsError ? "warning" : "success";
    label = dnsError ? "已响应 · DNS 异常" : "已响应";
  } else if (http?.status === "auth") {
    tone = "warning";
    label = "已响应 · 需认证/受限";
  } else if (report.result.httpStatus > 0) {
    tone = "warning";
    label = `HTTP ${report.result.httpStatus}`;
  } else if (checks.some((check) => check.status === "error")) {
    tone = "danger";
    label = "连接异常";
  }
  return <span className="probe-status"><span className={`pill ${tone}`}>{label}</span>{http && http.millis > 0 && <span className="muted mono">{Math.round(http.millis)} ms</span>}</span>;
}

function ProbeDetails({ report, error }: { report?: ProbeReport; error?: string }) {
  return (
    <div className="probe-detail-body">
      {error && <div className="field-error selectable">{error}{report ? "（下方为上次结果）" : ""}</div>}
      {report && <>
        <div className="probe-meta">入口：{report.via || "未确认"}{report.result.httpStatus > 0 && <span>HTTP {report.result.httpStatus}</span>}{report.result.checkedAt > 0 && <span>{new Date(report.result.checkedAt).toLocaleTimeString("zh-CN", { hour12: false })}</span>}</div>
        <div className="probe-checks">
          {(report.result.checks ?? []).map((check, index) => (
            <div className="probe-check" key={`${check.kind}-${index}`}>
              <span className="mono">{checkNames[check.kind] ?? check.kind}</span>
              <span className={`pill ${checkTones[check.status] ?? ""}`}>{checkLabels[check.status] ?? check.status}</span>
              <span className="mono muted">{check.millis > 0 ? `${Math.round(check.millis)} ms` : "—"}</span>
              <span className="probe-check-detail selectable">{check.detail}</span>
            </div>
          ))}
        </div>
        {!!report.result.answers?.length && <div className="probe-answers mono selectable">DNS：{report.result.answers.join(" · ")}</div>}
      </>}
      {!report && !error && <div className="muted">尚无检测结果</div>}
    </div>
  );
}

export function ProbeRow({ target, campus = false }: { target: Pick<NetworkTarget, "id" | "name" | "host">; campus?: boolean }) {
  const { reports, errors, busy, probing, probe } = useNetworkTools();
  const report = reports[target.id];
  const error = errors[target.id];
  const loading = probing === target.id;
  return (
    <div className="probe-row">
      <details className="probe-details">
        <summary>
          <ChevronRight className="probe-chevron" size={14} />
          <div className="probe-target">
            <span className="probe-name">{target.name}{target.id === "icourse" && <span className="compat-note" tabIndex={0} aria-label={`代理兼容：${compatibilityNote}`}>
              代理兼容 <Info size={12} /><span className="compat-tooltip" role="tooltip">{compatibilityNote}</span>
            </span>}</span>
            <span className="mono muted">{target.host}</span>
          </div>
          <ProbeStatus report={report} error={error} loading={loading} />
        </summary>
        <ProbeDetails report={report} error={error} />
      </details>
      <button className="btn icon-btn sm ghost probe-refresh" onClick={() => probe([target.id])} disabled={busy} aria-label={`${campus ? "刷新" : "检测"}${target.name}`} title={campus ? "刷新" : "检测"}>
        {loading ? <Spinner /> : <RefreshCw size={14} />}
      </button>
    </div>
  );
}

export function CampusChecks() {
  const { busy, probe } = useNetworkTools();
  return (
    <section className="card campus-checks">
      <CardTitle icon={Activity} extra={<button className="link" disabled={busy} onClick={() => probe(campusTargets.map((target) => target.id))}>刷新</button>}>校园可用性</CardTitle>
      {campusTargets.map((target) => <ProbeRow key={target.id} target={target} campus />)}
    </section>
  );
}
