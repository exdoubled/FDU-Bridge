import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, RefreshCw, Route, Search, Trash2 } from "lucide-react";
import { RuleKind, RulesService, errorText, type ApplyState } from "../lib/api";
import { addRule, cloneDraft, emptyDraft, filterRows, removeRules, rows, sameDraft, setEnabled, typeLabels, type Draft, type RuleType } from "../lib/rulesDraft";
import { useStore } from "../store";
import { Empty, Spinner, Switch } from "../components/ui";

const kinds: Record<RuleType, RuleKind> = { domain: RuleKind.Domain, suffix: RuleKind.Suffix, cidr: RuleKind.CIDR };
const placeholders: Record<RuleType, string> = { domain: "校内网址或域名，例如 https://jwfw.fudan.edu.cn", suffix: "例如 fudan.edu.cn", cidr: "例如 10.20.0.0/16 或 10.20.26.253" };
const tagTone: Record<RuleType, string> = { domain: "accent", suffix: "success", cidr: "warning" };

export function Rules() {
  const { setDirty, confirm, toast, fail, versions } = useStore();
  const [path, setPath] = useState("");
  const [loadError, setLoadError] = useState("");
  const [saved, setSaved] = useState<Draft | null>(null);
  const [draft, setDraft] = useState<Draft>(emptyDraft());
  const [hash, setHash] = useState("");
  const [applied, setApplied] = useState<ApplyState | null>(null);
  const [type, setType] = useState<RuleType>("domain");
  const [value, setValue] = useState("");
  const [addError, setAddError] = useState("");
  const [filter, setFilter] = useState<RuleType | "all">("all");
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const [adding, setAdding] = useState(false);
  const busy = saving || adding;

  const dirty = saved !== null && !sameDraft(draft, saved);
  useEffect(() => setDirty("rules", dirty), [dirty, setDirty]);
  useEffect(() => () => setDirty("rules", false), [setDirty]);

  const load = useCallback(async () => {
    try {
      const view = await RulesService.Load();
      const doc = view.snapshot.document as Draft;
      setPath(view.path);
      setSaved(cloneDraft(doc));
      setDraft(cloneDraft(doc));
      setHash(view.snapshot.hash);
      setApplied(view.status);
      setLoadError("");
    } catch (e) {
      setSaved(null);
      setLoadError(errorText(e));
    }
  }, []);

  // Reload when another page changes the rules file, but never over an unsaved draft.
  useEffect(() => {
    if (!dirty) load();
  }, [load, versions.settings, versions.clash]);

  useEffect(() => {
    if (dirty || !hash) return;
    const timer = setInterval(() => RulesService.Status(hash).then(setApplied).catch(() => undefined), 2000);
    return () => clearInterval(timer);
  }, [dirty, hash]);

  const all = useMemo(() => rows(draft), [draft]);
  const visible = useMemo(() => filterRows(all, filter, query), [all, filter, query]);
  const countOf = (t: RuleType) => all.filter((r) => r.type === t).length;

  const add = async () => {
    if (busy || !value.trim()) return;
    setAdding(true);
    try {
      const canonical = await RulesService.Validate(kinds[type], value);
      const next = addRule(draft, type, canonical);
      if (!next) {
        setAddError("该校内资源已存在");
        return;
      }
      setDraft(next);
      setValue("");
      setAddError("");
    } catch (e) {
      setAddError(errorText(e));
    } finally {
      setAdding(false);
    }
  };

  const save = async () => {
    if (busy) return;
    setSaving(true);
    try {
      const view = await RulesService.Save(draft, hash);
      const doc = view.snapshot.document as Draft;
      setSaved(cloneDraft(doc));
      setDraft(cloneDraft(doc));
      setHash(view.snapshot.hash);
      setApplied(view.status);
      toast("success", "校内资源已保存");
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  const reload = async () => {
    if (dirty && !(await confirm({ title: "重新读取校内资源？", message: "重新读取将丢弃未保存的修改。", confirm: "丢弃并读取", danger: true }))) return;
    load();
  };

  return (
    <>
      <header className="page-head">
        <h1 className="page-title" title={path}>校内资源</h1>
        <div className="page-actions">
          <button className="btn icon-btn" onClick={reload} disabled={busy} aria-label="重新读取" title="重新读取"><RefreshCw size={15} /></button>
        </div>
      </header>
      <div className="page-body">
        {loadError ? (
          <Empty icon={Route} title="无法读取校内资源" action={<button className="btn" onClick={load}>重试</button>}>{loadError}</Empty>
        ) : (
          <div className="stack">
            <section className="card" style={{ padding: 14 }}>
              <form className="toolbar" onSubmit={(e) => { e.preventDefault(); add(); }}>
                <select className="select" style={{ width: 140 }} value={type} disabled={busy} onChange={(e) => { setType(e.target.value as RuleType); setAddError(""); }}>
                  {(Object.keys(typeLabels) as RuleType[]).map((t) => <option key={t} value={t}>{typeLabels[t]}</option>)}
                </select>
                <input className={`input mono${addError ? " invalid" : ""}`} placeholder={placeholders[type]} value={value} disabled={busy}
                  onChange={(e) => { setValue(e.target.value); setAddError(""); }} />
                <button className="btn primary" type="submit" disabled={busy || !value.trim()}><Plus size={15} />添加</button>
              </form>
              {addError && <div className="field-error">{addError}</div>}
            </section>

            <div className="toolbar">
              <div className="chips">
                <button className={`chip${filter === "all" ? " active" : ""}`} onClick={() => setFilter("all")}>全部 {all.length}</button>
                {(Object.keys(typeLabels) as RuleType[]).map((t) => (
                  <button key={t} className={`chip${filter === t ? " active" : ""}`} onClick={() => setFilter(t)}>{typeLabels[t]} {countOf(t)}</button>
                ))}
              </div>
              <span className="spacer" />
              <div className="search" style={{ width: 200 }}>
                <Search size={15} />
                <input className="input" placeholder="搜索校内资源" value={query} onChange={(e) => setQuery(e.target.value)} />
              </div>
            </div>

            {all.length === 0 ? (
              <Empty icon={Route} title="添加需要通过校园 VPN 访问的网址、域名或网段" />
            ) : visible.length === 0 ? (
              <Empty icon={Search} title="没有匹配的校内资源" />
            ) : (
              <div className="list">
                {visible.map((r) => (
                  <div key={`${r.type}:${r.value}`} className={`list-row${r.enabled ? "" : " off"}`}>
                    <span className="type-tag"><span className={`pill ${tagTone[r.type]}`}>{typeLabels[r.type]}</span></span>
                    <span className="rule-value" title={r.value}>{r.value}</span>
                    <Switch checked={r.enabled} disabled={busy} label={`启用 ${r.value}`} onChange={(on) => setDraft(setEnabled(draft, r.type, r.value, on))} />
                    <button className="btn ghost icon-btn sm" disabled={busy} aria-label={`删除 ${r.value}`} title="删除"
                      onClick={() => setDraft(removeRules(draft, [r]))}><Trash2 size={15} /></button>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
      {!loadError && (
        <footer className="savebar">
          {dirty ? (
            <>
              <span className="grow" style={{ color: "var(--warning)" }}>有未保存的修改</span>
              <button className="btn" onClick={() => saved && setDraft(cloneDraft(saved))} disabled={busy}>放弃</button>
              <button className="btn primary" onClick={save} disabled={busy}>{saving && <Spinner />}保存</button>
            </>
          ) : applied ? (
            <>
              <span className={`dot ${applied.applied ? "on" : "busy"}`} />
              <span className="grow muted">{applied.message}</span>
            </>
          ) : <span className="grow muted">尚未读取校内资源</span>}
        </footer>
      )}
    </>
  );
}
