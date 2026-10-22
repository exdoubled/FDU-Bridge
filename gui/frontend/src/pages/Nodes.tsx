import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, Globe, RefreshCw, Search } from "lucide-react";
import { ClashService, errorText, type NodeState } from "../lib/api";
import { useStore } from "../store";
import { Banner, Empty, Spinner } from "../components/ui";

const modeNames: Record<string, string> = { global: "全局", rule: "规则", direct: "直连" };

export function Nodes() {
  const { toast, fail, versions, status } = useStore();
  const [state, setState] = useState<NodeState | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [switching, setSwitching] = useState("");
  const [query, setQuery] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setState(await ClashService.Nodes());
      setError("");
    } catch (e) {
      setState(null);
      setError(errorText(e));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { load(); }, [load, versions.mode, versions.clash, status.connected]);

  const choices = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (state?.choices ?? []).filter((n) => !q || n.toLowerCase().includes(q));
  }, [state, query]);

  const select = async (name: string) => {
    if (!state || state.problem || switching || name === state.current) return;
    setSwitching(name);
    try {
      const next = await ClashService.SelectNode(name);
      setState(next);
      toast("success", `已切换到 ${next.current}`);
    } catch (e) {
      fail(e);
      load();
    } finally {
      setSwitching("");
    }
  };

  return (
    <>
      <header className="page-head">
        <h1 className="page-title" title={state ? `${modeNames[state.mode] ?? state.mode}模式` : undefined}>外网节点</h1>
        <div className="page-actions">
          {state?.mode !== "rule" && <div className="search" style={{ width: 220 }}>
            <Search size={15} />
            <input className="input" placeholder="搜索节点" value={query} onChange={(e) => setQuery(e.target.value)} />
          </div>}
          <button className="btn icon-btn" onClick={load} disabled={loading} aria-label="刷新" title="刷新">
            {loading ? <Spinner /> : <RefreshCw size={15} />}
          </button>
        </div>
      </header>
      <div className="page-body">
        {error ? (
          <Empty icon={Globe} title="无法读取 Clash 节点" action={<button className="btn" onClick={load}>重试</button>}>{error}</Empty>
        ) : !state ? (
          <Empty icon={Globe} title="正在读取节点…" />
        ) : (
          <div className="stack">
            {state.problem && <Banner tone="warning">{state.problem}</Banner>}
            {state.mode === "rule" ? (
              <Empty icon={Globe} title="沿用 Clash 设置">规则模式下，外网连接继续使用 Clash 的现有分流和节点选择。</Empty>
            ) : choices.length === 0 ? (
              <Empty icon={Search} title={query ? "没有匹配的节点" : "没有可选节点"} />
            ) : (
              <div className="node-grid">
                {choices.map((name) => {
                  const current = name === state.current;
                  return (
                    <button key={name} className={`node${current ? " current" : ""}`} disabled={!!state.problem || !!switching}
                      onClick={() => select(name)} title={[name, state.types[name]].filter(Boolean).join("\n")}>
                      <span className="node-name">{name}</span>
                      {current && <Check size={16} className="node-mark" />}
                      {switching === name && <span className="node-mark"><Spinner /></span>}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
