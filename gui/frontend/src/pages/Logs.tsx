import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, Download, Eraser, Pause, Play, Search } from "lucide-react";
import { Events } from "@wailsio/runtime";
import { CoreService } from "../lib/api";
import { useStore } from "../store";
import { CampusDiagnostics } from "../components/CampusDiagnostics";

const limit = 5000;

function lineClass(line: string) {
  if (/\b(error|fatal|panic|failed)\b|失败|错误/i.test(line)) return "log-line error";
  if (/\bwarn(ing)?\b|警告/i.test(line)) return "log-line warn";
  return "log-line";
}

export function Logs() {
  const { toast, fail, confirm } = useStore();
  const [lines, setLines] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [paused, setPaused] = useState(false);
  const [atBottom, setAtBottom] = useState(true);
  const view = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  const buffered = useRef<string[]>([]);
  pausedRef.current = paused;

  useEffect(() => {
    CoreService.Logs().then(setLines).catch(() => undefined);
    return Events.On("core:logs", (event) => {
      if (pausedRef.current) {
        buffered.current.push(...event.data);
        return;
      }
      setLines((current) => [...current, ...event.data].slice(-limit));
    });
  }, []);

  useEffect(() => {
    if (!paused && buffered.current.length) {
      const pending = buffered.current;
      buffered.current = [];
      setLines((current) => [...current, ...pending].slice(-limit));
    }
  }, [paused]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? lines.filter((l) => l.toLowerCase().includes(q)) : lines;
  }, [lines, query]);

  useEffect(() => {
    if (atBottom && view.current) view.current.scrollTop = view.current.scrollHeight;
  }, [visible, atBottom]);

  const onScroll = () => {
    const el = view.current;
    if (el) setAtBottom(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
  };

  const exportLogs = async () => {
    try {
      const path = await CoreService.ExportLogs();
      if (path) toast("success", `已导出到 ${path}`);
    } catch (e) {
      fail(e);
    }
  };

  const clear = async () => {
    if (!(await confirm({ title: "清空日志？", message: "只清空本窗口中的记录，不影响 FDU 运行。", confirm: "清空", danger: true }))) return;
    await CoreService.ClearLogs();
    setLines([]);
  };

  return (
    <>
      <header className="page-head">
        <h1 className="page-title">诊断</h1>
        <div className="page-actions">
          <div className="search" style={{ width: 220 }}>
            <Search size={15} />
            <input className="input" placeholder="筛选日志" value={query} onChange={(e) => setQuery(e.target.value)} />
          </div>
          <button className="btn icon-btn" onClick={() => setPaused(!paused)} aria-label={paused ? "继续" : "暂停"} title={paused ? "继续" : "暂停"}>
            {paused ? <Play size={15} /> : <Pause size={15} />}
          </button>
          <button className="btn icon-btn" onClick={clear} aria-label="清空" title="清空"><Eraser size={15} /></button>
          <button className="btn" onClick={exportLogs}><Download size={15} />导出</button>
        </div>
      </header>
      <div className="page-body flush diagnostics-body">
        <CampusDiagnostics />
        <div className="log-view" ref={view} onScroll={onScroll}>
          {visible.length === 0 ? (
            <span className="muted">{lines.length ? "没有匹配的日志" : "暂无日志"}</span>
          ) : visible.map((line, i) => <div key={i} className={lineClass(line)}>{line}</div>)}
        </div>
        {!atBottom && (
          <button className="btn sm to-bottom" onClick={() => setAtBottom(true)}><ArrowDown size={14} />回到底部</button>
        )}
      </div>
    </>
  );
}
