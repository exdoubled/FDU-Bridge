import { useState } from "react";
import { Activity, FlaskConical } from "lucide-react";
import { aiProviders, formatBytes, ProbeRow, TrafficCards, useNetworkTools } from "../components/NetworkTools";
import { CardTitle } from "../components/ui";
import type { NetworkConnection } from "../lib/api";

const aiIds = Object.keys(aiProviders);
const routeNames: Record<string, string> = { fdu: "FDU", "non-fdu": "非 FDU", unknown: "未确认" };

function ConnectionRow({ connection }: { connection: NetworkConnection }) {
  const route = routeNames[connection.route] ?? "未确认";
  return (
    <tr>
      <td>{aiProviders[connection.provider] ?? connection.provider}</td>
      <td><div className="connection-host mono selectable" title={[connection.host, connection.started > 0 ? `连接于 ${new Date(connection.started).toLocaleString("zh-CN", { hour12: false })}` : ""].filter(Boolean).join("\n")}>{connection.host || "—"}</div><div className="connection-sub">:{connection.port || "—"}</div></td>
      <td className="mono selectable">
        {connection.ipKind === "fake" ? <><span className="connection-sub">Fake-IP</span><div>{connection.targetIP || "—"}</div></> : connection.ipKind === "actual" && connection.targetIP ? connection.targetIP : <span className="muted">未确认</span>}
      </td>
      <td className="mono">{connection.network?.toUpperCase() || "—"}</td>
      <td><span className={`pill ${connection.route === "fdu" ? "accent" : ""}`}>{route}</span></td>
      <td><div className="connection-node" title={connection.node}>{connection.node || "—"}</div><div className="connection-sub" title={connection.dnsMode ? `DNS：${connection.dnsMode}` : undefined}>{connection.source || "未确认"}</div></td>
      <td className="connection-counters mono"><div>↑ {formatBytes(connection.upload)}</div><div>↓ {formatBytes(connection.download)}</div></td>
    </tr>
  );
}

export function Network() {
  const { view, observeError, targets, targetsError, busy, probe } = useNetworkTools();
  const [provider, setProvider] = useState("all");
  const connections = (view?.connections ?? []).filter((connection) => aiIds.includes(connection.provider) && (provider === "all" || connection.provider === provider));
  const aiTargets = aiIds.map((id) => targets.find((target) => target.id === id)).filter((target) => target !== undefined);

  return (
    <>
      <header className="page-head"><h1 className="page-title">网络检测</h1></header>
      <div className="page-body">
        <div className="stack">
          <TrafficCards />
          <section className="card connections-card">
            <CardTitle icon={Activity} extra={<select className="select provider-select" aria-label="筛选 AI 服务商" value={provider} onChange={(event) => setProvider(event.target.value)}>
              <option value="all">全部服务商</option>
              {Object.entries(aiProviders).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
            </select>}>AI 当前连接 <span className="faint">{connections.length}</span></CardTitle>
            <div className="connection-table-wrap">
              <table className="connection-table">
                <thead><tr><th>服务商</th><th>域名 / 端口</th><th title="来源记录的目标 IP；远端解析可能未知，FDU 前的 DNS 答案不是实际 VPN 目标">目标 IP</th><th>传输</th><th>路径</th><th>节点 / 来源</th><th>流量</th></tr></thead>
                <tbody>
                  {connections.map((connection) => <ConnectionRow key={`${connection.source}-${connection.id}`} connection={connection} />)}
                  {connections.length === 0 && <tr><td colSpan={7} className="connection-empty">{observeError ? "暂时无法读取连接" : !view ? "正在读取连接…" : "暂无可观测的 AI 连接"}</td></tr>}
                </tbody>
              </table>
            </div>
            <div className="network-caption">仅展示 TCP / UDP；加密连接无法据此区分 HTTP / WS。{view?.truncated && " 列表最多展示 128 条连接。"}</div>
          </section>
          <section className="card ai-probes">
            <CardTitle icon={FlaskConical} extra={<button className="btn sm" disabled={busy || !aiTargets.length} onClick={() => probe(aiTargets.map((target) => target.id))}>{busy ? "检测中…" : "检测全部"}</button>}>固定目标检测</CardTitle>
            {aiTargets.map((target) => <ProbeRow key={target.id} target={target} />)}
            {targetsError && <div className="note" title={targetsError}>无法读取检测目标</div>}
            {!targetsError && !aiTargets.length && <div className="muted">正在读取检测目标…</div>}
            <div className="network-caption">经当前入口检测；HTTP 401 表示网络已响应，模型访问仍需有效认证。入口不代表最终路径。</div>
          </section>
          {view?.scope && <div className="network-caption network-scope selectable">{view.scope}</div>}
        </div>
      </div>
    </>
  );
}
