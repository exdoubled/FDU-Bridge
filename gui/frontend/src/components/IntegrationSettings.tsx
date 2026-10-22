import { useEffect, useRef, useState } from "react";
import { ChevronRight, Copy, Download, FolderOpen, Plug, RefreshCw } from "lucide-react";
import { AppService, ClashService } from "../lib/api";
import { useStore } from "../store";
import { Banner, Segmented, Spinner } from "./ui";

interface IntegrationConfig {
  mode: string;
  runtimeFile: string;
  controller: string;
  secret: string;
}

interface Client {
  kind: string;
  name: string;
  root: string;
  runtimeFile: string;
}

interface IntegrationExport {
  script: string;
  yaml: string;
  socks: string;
  http: string;
  message: string;
}

const clientPath = (client: Client) => client.kind === "verge" ? client.root : client.runtimeFile || client.root;

export function IntegrationSettings({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const { status, overview, setOverview, setConfig, toast, fail } = useStore();
  const [draft, setDraft] = useState<IntegrationConfig | null>(null);
  const [baseline, setBaseline] = useState<IntegrationConfig | null>(null);
  const [clients, setClients] = useState<Client[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [checking, setChecking] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [format, setFormat] = useState<"script" | "yaml">("script");
  const [preview, setPreview] = useState<IntegrationExport | null>(null);
  const [exporting, setExporting] = useState(false);
  const previewField = useRef<HTMLTextAreaElement>(null);
  const locked = status.running || status.starting || status.stopping;
  const dirty = !!draft && JSON.stringify(draft) !== JSON.stringify(baseline);
  const disabled = locked || saving;
  const mode = draft?.mode;

  useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

  useEffect(() => {
    let active = true;
    ClashService.Integration().then((next) => {
      if (active) {
        setDraft(next);
        setBaseline(next);
      }
    }).catch(fail).finally(() => { if (active) setLoading(false); });
    ClashService.Clients().then((next) => { if (active) setClients(next); }).catch(fail);
    return () => { active = false; };
  }, [fail]);

  useEffect(() => {
    if (!mode || dirty || (mode !== "proxy" && !expanded)) return;
    let active = true;
    setExporting(true);
    ClashService.ExportIntegration().then((next) => { if (active) setPreview(next); }).catch(fail)
      .finally(() => { if (active) setExporting(false); });
    return () => { active = false; };
  }, [mode, dirty, expanded, fail]);

  const change = (patch: Partial<IntegrationConfig>) => setDraft((current) => current && { ...current, ...patch });

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    try {
      const manual = draft.mode === "manual";
      const next = await ClashService.SaveIntegration(draft.mode, draft.runtimeFile.trim(), manual ? draft.controller.trim() : "", manual ? draft.secret : "");
      setDraft(next);
      setBaseline(next);
      setPreview(null);
      const [config, inspected] = await Promise.all([AppService.Config(), ClashService.Inspect()]);
      setConfig(config);
      setOverview(inspected);
      toast("success", "接入设置已保存");
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  const inspect = async () => {
    setChecking(true);
    try {
      const [inspected, detected] = await Promise.all([ClashService.Inspect(), ClashService.Clients()]);
      setOverview(inspected);
      setClients(detected);
    } catch (e) {
      fail(e);
    } finally {
      setChecking(false);
    }
  };

  const browse = async () => {
    try {
      const path = await AppService.PickRuntimeConfig();
      if (path) change({ runtimeFile: path });
    } catch (e) {
      fail(e);
    }
  };

  const text = preview?.[format] ?? "";
  const selectPreview = () => {
    previewField.current?.focus();
    previewField.current?.select();
    toast("info", "已选中，可按 Ctrl+C 复制");
  };
  const copy = async () => {
    if (!navigator.clipboard?.writeText) {
      selectPreview();
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      toast("success", "已复制");
    } catch {
      selectPreview();
    }
  };
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = format === "script" ? "fdu-bridge.js" : "fdu-bridge.yaml";
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <section className="card section" id="clash" style={{ padding: 0 }}>
      <div className="section-head"><Plug size={16} />代理接入</div>
      {loading ? <div className="row muted" style={{ padding: "12px 20px 20px" }}><Spinner />正在读取…</div> : !draft ? (
        <div style={{ padding: "10px 20px 16px" }}><Banner tone="warning">无法读取接入设置</Banner></div>
      ) : <>
        <div className="setting">
          <div className="setting-label">接入方式</div>
          <div className="setting-control">
            <div style={{ width: 360, maxWidth: "100%" }}>
              <Segmented value={draft.mode} disabled={disabled} onChange={(mode) => change({ mode })}
                options={[{ value: "auto", label: "自动接入" }, { value: "manual", label: "手动接入" }, { value: "proxy", label: "仅本地代理" }]} />
            </div>
          </div>
        </div>
        {draft.mode === "auto" && <div className="setting">
          <label className="setting-label" htmlFor="integration-client">代理客户端</label>
          <div className="setting-control">
            <select id="integration-client" className="select" value={draft.runtimeFile} disabled={disabled}
              title={draft.runtimeFile} onChange={(e) => change({ runtimeFile: e.target.value })}>
              <option value="">自动检测{clients.length ? "" : "（未发现客户端）"}</option>
              {draft.runtimeFile && !clients.some((client) => clientPath(client) === draft.runtimeFile) && (
                <option value={draft.runtimeFile}>当前路径</option>
              )}
              {clients.map((client) => <option key={`${client.kind}:${client.root}`} value={clientPath(client)} title={clientPath(client)}>{client.name}</option>)}
            </select>
          </div>
        </div>}
        {draft.mode === "manual" && <>
          <div className="setting">
            <label className="setting-label" htmlFor="integration-runtime">运行时 YAML</label>
            <div className="setting-control">
              <input id="integration-runtime" className="input mono" style={{ width: "100%", minWidth: 0, flex: 1 }} value={draft.runtimeFile}
                disabled={disabled} title={draft.runtimeFile} placeholder="选择运行时配置文件" onChange={(e) => change({ runtimeFile: e.target.value })} />
              <button className="btn" onClick={browse} disabled={disabled}><FolderOpen size={15} />浏览</button>
            </div>
          </div>
          <div className="setting">
            <label className="setting-label" htmlFor="integration-controller">控制器地址</label>
            <div className="setting-control">
              <input id="integration-controller" className="input mono" value={draft.controller} disabled={disabled} placeholder="自动读取（可选）"
                title="例如 http://127.0.0.1:9090" onChange={(e) => change({ controller: e.target.value })} />
            </div>
          </div>
          <div className="setting">
            <label className="setting-label" htmlFor="integration-secret">控制器密码</label>
            <div className="setting-control">
              <input id="integration-secret" className="input" type="password" autoComplete="off" value={draft.secret} disabled={disabled}
                placeholder="自动读取（可选）" onChange={(e) => change({ secret: e.target.value })} />
            </div>
          </div>
        </>}
        <div style={{ padding: "6px 20px 16px" }}>
          {draft.mode === "proxy" ? <>
            <div className="muted">分流由外部应用设置</div>
            <dl className="kv" style={{ marginBottom: 0 }}>
              <dt>SOCKS</dt><dd className="mono">{preview?.socks || status.socks || "—"}</dd>
              <dt>HTTP</dt><dd className="mono">{preview?.http || status.http || "—"}</dd>
            </dl>
          </> : overview?.ready ? (
            <dl className="kv" style={{ margin: "4px 0 0" }}><dt>当前订阅</dt><dd>{overview.profile || "已就绪"}</dd></dl>
          ) : overview?.error ? <Banner tone="warning">{overview.error}</Banner> : <span className="muted">尚未检查连接</span>}
          <div className="row" style={{ marginTop: 14, justifyContent: "flex-end", flexWrap: "wrap" }}>
            {dirty && <>
              <span style={{ color: "var(--warning)", marginRight: "auto" }}>有未保存的修改</span>
              <button className="btn" onClick={() => setDraft(baseline)} disabled={disabled}>放弃</button>
            </>}
            <button className="btn" onClick={inspect} disabled={checking || saving}>{checking ? <Spinner /> : <RefreshCw size={15} />}检查连接</button>
            <button className="btn primary" onClick={save} disabled={disabled || !dirty}>{saving && <Spinner />}保存</button>
          </div>
        </div>
        <div className="collapse">
          <button className={`collapse-head${expanded ? " open" : ""}`} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            导出扩展（可选）<ChevronRight size={16} className="chev" />
          </button>
          {expanded && <div style={{ padding: "0 20px 16px" }}>
            {dirty ? <span className="muted">先保存接入设置</span> : exporting ? <div className="row muted"><Spinner />正在生成…</div> : <>
              {draft.mode === "manual" && <div className="muted" style={{ marginBottom: 10 }}>手动接入使用运行时 YAML 和控制器，无需安装此脚本</div>}
              <div className="row" style={{ marginBottom: 10 }}>
                <div style={{ width: 240 }}><Segmented value={format} onChange={setFormat} options={[{ value: "script", label: "覆写脚本" }, { value: "yaml", label: "YAML" }]} /></div>
                <span className="spacer" />
                <button className="btn sm" onClick={copy} disabled={!text}><Copy size={14} />复制</button>
                <button className="btn sm" onClick={download} disabled={!text}><Download size={14} />导出</button>
              </div>
              <textarea ref={previewField} className="textarea" readOnly aria-label={format === "script" ? "覆写脚本" : "YAML 配置"}
                value={text} rows={5} style={{ display: "block", maxHeight: 200 }} spellCheck={false} placeholder={preview?.message || "暂无可用配置"} />
              {preview?.message && <div className="muted" style={{ marginTop: 8 }}>{preview.message}</div>}
            </>}
          </div>}
        </div>
      </>}
    </section>
  );
}
