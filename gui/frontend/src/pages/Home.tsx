import { useCallback, useEffect, useState } from "react";
import { ArrowRight, Globe, LoaderCircle, Network, Route, Shield, ShieldAlert, ShieldCheck } from "lucide-react";
import { ClashService, CoreService, RulesService, errorText, type ApplyState, type NodeState } from "../lib/api";
import { counts, type Draft } from "../lib/rulesDraft";
import { formatUptime, stateLabel, stateTone } from "../lib/status";
import { useStore } from "../store";
import { Banner, CardTitle, Spinner, Switch } from "../components/ui";
import { CampusChecks } from "../components/NetworkTools";

function useNow(active: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);
  return now;
}

const heroIcons = { off: Shield, on: ShieldCheck, busy: LoaderCircle, bad: ShieldAlert };

export function Home() {
  const { status, navigate, confirm, fail, versions } = useStore();
  const [nodes, setNodes] = useState<NodeState | null>(null);
  const [nodesError, setNodesError] = useState("");
  const [rules, setRules] = useState<{ enabled: number; disabled: number; status: ApplyState; hash: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const now = useNow(status.connected);
  const tone = stateTone(status);
  const HeroIcon = heroIcons[tone];
  const active = status.running || status.starting;

  const loadNodes = useCallback(() => {
    ClashService.Nodes().then((n) => { setNodes(n); setNodesError(""); }).catch((e) => { setNodes(null); setNodesError(errorText(e)); });
  }, []);
  useEffect(loadNodes, [loadNodes, versions.node, versions.mode, versions.clash, status.connected]);

  const loadRules = useCallback(() => {
    RulesService.Load()
      .then((v) => setRules({ ...counts(v.snapshot.document as Draft), status: v.status, hash: v.snapshot.hash }))
      .catch(() => setRules(null));
  }, []);
  useEffect(loadRules, [loadRules, versions.rules, versions.settings, versions.clash]);
  useEffect(() => {
    if (!rules?.hash) return;
    const timer = setInterval(() => RulesService.Status(rules.hash).then((s) => setRules((r) => (r ? { ...r, status: s } : r))).catch(() => undefined), 3000);
    return () => clearInterval(timer);
  }, [rules?.hash]);

  const togglePower = async () => {
    setBusy(true);
    try {
      if (active) {
        const stopped = await CoreService.Disconnect();
        if (!stopped && (await confirm({ title: "关闭超时", message: "校园 VPN 尚未退出，强制结束本程序启动的进程？", confirm: "强制结束", danger: true }))) {
          await CoreService.ForceStop();
        }
      } else {
        const preview = await CoreService.PreviewConnect();
        if (preview.needsConsent && !(await confirm({ title: "启用校内访问", message: preview.message, confirm: "启用" }))) return;
        await CoreService.Connect(preview.needsConsent);
      }
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  const relogin = async () => {
    setBusy(true);
    try {
      if (!(await confirm({ title: "重新登录", message: status.state === "authentication" ? "结束当前登录并重新打开登录窗口，保留配置和校园规则。" : "重新认证，保留配置和校园规则。校内连接可能暂时中断。", confirm: "重新登录" }))) return;
      await CoreService.Relogin();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  const metaPills = [
    status.server && status.server,
    status.protocol && (status.protocol === "atrust" ? "aTrust" : "EasyConnect"),
  ].filter(Boolean) as string[];

  return (
    <>
      <header className="page-head">
        <h1 className="page-title">首页</h1>
      </header>
      <div className="page-body">
        <div className="stack">
          {!active && status.external && <Banner tone="warning">其他 FDU 正在运行，请先关闭它再启用校内访问</Banner>}
          <section className="card hero">
            <div className={`hero-icon ${tone === "off" ? "" : tone}`}>
              <HeroIcon size={28} className={tone === "busy" ? "spin" : undefined} />
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div className="hero-title">{stateLabel(status)}</div>
              <div className="hero-meta">
                {status.connected && status.connectedAt > 0 && <span className="pill success">{formatUptime(status.connectedAt, now)}</span>}
                {metaPills.map((p) => <span key={p} className="pill">{p}</span>)}
              </div>
              {status.problem && <div className="note">{status.problem}</div>}
              {status.protocol === "atrust" && status.saved && !status.external && !status.tool && (
                <button className={status.authRequired ? "btn primary" : "link"} style={{ marginTop: 10 }} onClick={relogin}
                  disabled={busy || status.stopping || (status.starting && !status.authRequired && !["campus-unavailable", "authentication"].includes(status.state))}>
                  重新登录
                </button>
              )}
            </div>
            {busy && <Spinner size={18} />}
            <Switch large checked={active} disabled={busy || status.stopping || (!active && status.external)} onChange={togglePower} label="启用校内访问" />
          </section>

          <div className="grid-2">
            <section className="card">
              <CardTitle icon={Network}>覆盖范围</CardTitle>
              <div>{status.coverage || (active ? "正在检查覆盖范围…" : "启用后显示实际覆盖范围")}</div>
            </section>
            <section className="card">
              <CardTitle icon={Globe}>外网节点</CardTitle>
              <div className="big-value ellipsis" title={nodes?.current}>{nodes?.current || "—"}</div>
              {(nodesError || nodes?.problem) && <div className="note ellipsis" title={nodesError || nodes?.problem}>{nodesError || nodes?.problem}</div>}
              {!!nodes?.choices.length && <button className="link" style={{ marginTop: 10 }} onClick={() => navigate("nodes")}>切换节点 <ArrowRight size={14} /></button>}
            </section>
          </div>

          <CampusChecks />

          <section className="card row" style={{ alignItems: "center" }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <CardTitle icon={Route}>校内资源</CardTitle>
              {rules ? (
                <div className="row" style={{ flexWrap: "wrap" }}>
                  <span><span className="stat">{rules.enabled}</span> <span className="muted">项启用</span></span>
                  {rules.disabled > 0 && <span className="muted">{rules.disabled} 项停用</span>}
                  <span className={`pill ${rules.status.applied ? "success" : ""}`} title={rules.status.message}>
                    {rules.status.applied ? "已生效" : active ? "待生效" : "启用后生效"}
                  </span>
                </div>
              ) : <div className="muted">尚未读取校内资源</div>}
            </div>
            <button className="btn" onClick={() => navigate("rules")}>管理校内资源</button>
          </section>
        </div>
      </div>
    </>
  );
}
