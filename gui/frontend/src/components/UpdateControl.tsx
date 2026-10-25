import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Events } from "@wailsio/runtime";
import { Download, RefreshCw } from "lucide-react";
import { UpdateService, errorText, type UpdateStatus } from "../lib/api";
import { useStore } from "../store";
import { Spinner, Switch } from "./ui";

interface Updates {
  update: UpdateStatus | null;
  error: string;
  busy: boolean;
  check: () => Promise<void>;
  download: () => Promise<void>;
  install: () => Promise<void>;
  setAutoCheck: (enabled: boolean) => Promise<void>;
}

const Context = createContext<Updates | null>(null);

export function useUpdates() {
  const updates = useContext(Context);
  if (!updates) throw new Error("updates missing");
  return updates;
}

export function UpdateProvider({ children }: { children: ReactNode }) {
  const { setConfig } = useStore();
  const [update, setUpdate] = useState<UpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  useEffect(() => {
    let active = true;
    let received = false;
    void UpdateService.Ready().catch(() => undefined);
    const off = Events.On("app:update", (event) => {
      received = true;
      setUpdate(event.data);
      setError("");
    });
    UpdateService.Status().then((next: UpdateStatus) => { if (active && !received) setUpdate(next); })
      .catch((e: unknown) => { if (active && !received) setError(errorText(e)); });
    return () => { active = false; off(); };
  }, []);

  const run = async (action: () => Promise<void>) => {
    setPending(true);
    setError("");
    try { await action(); } catch (e) { setError(errorText(e)); }
    finally { setPending(false); }
  };

  const busy = pending || update?.phase === "checking" || update?.phase === "downloading" || update?.phase === "installing";
  return <Context.Provider value={{
    update, error, busy,
    check: () => run(async () => setUpdate(await UpdateService.Check())),
    download: () => run(async () => setUpdate(await UpdateService.Download())),
    install: () => run(async () => { await UpdateService.Install(); }),
    setAutoCheck: (enabled) => run(async () => {
      setConfig(await UpdateService.SetAutoCheck(enabled));
      setUpdate((current) => current && { ...current, autoCheck: enabled });
    }),
  }}>{children}</Context.Provider>;
}

function updateMessage(update: UpdateStatus | null): string {
  if (!update) return "";
  switch (update.phase) {
    case "checking": return "正在检查…";
    case "current": return "已是最新版本";
    case "available": return `发现新版本 ${update.latest}`;
    case "downloading": return `正在下载 ${Math.round(update.progress)}%`;
    case "ready": return "已下载，可安装";
    case "installing": return "正在安装…";
    default: return update.message;
  }
}

/** Short enough for the sidebar; the full message stays in the tooltip and dialog. */
function entryLabel(update: UpdateStatus | null): string {
  switch (update?.phase) {
    case "checking": return "正在检查更新…";
    case "downloading": return `正在下载 ${Math.round(update.progress)}%`;
    case "ready": return "更新已就绪";
    case "installing": return "正在安装…";
    case "error": return "更新失败";
    default: return "有新版本";
  }
}

export function UpdateSettings() {
  const { update, error, busy, check, setAutoCheck } = useUpdates();
  const message = error || updateMessage(update);
  const failed = !!error || update?.phase === "error";
  return (
    <section className="card section" style={{ padding: 0 }}>
      <div className="section-head"><Download size={16} />应用更新</div>
      <div className="setting">
        <div className="setting-label">当前版本</div>
        <div className="setting-control">
          {message && !failed && <span className="muted">{message}</span>}
          <span className="mono selectable">{update?.current || "—"}</span>
          <button className="btn" onClick={check} disabled={busy}>
            {update?.phase === "checking" ? <Spinner /> : <RefreshCw size={15} />}检查更新
          </button>
        </div>
      </div>
      <div className="setting">
        <div className="setting-label">自动检查更新</div>
        <div className="setting-control">
          <Switch checked={!!update?.autoCheck} onChange={setAutoCheck} disabled={!update || busy} label="自动检查更新" />
        </div>
      </div>
      {message && failed && <div className="field-error selectable" style={{ padding: "0 20px 14px", marginTop: 0 }}>{message}</div>}
    </section>
  );
}

export function UpdateControl() {
  const { update, error, busy, download, install } = useUpdates();
  const { status, dirty } = useStore();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const locked = status.running || status.starting || status.stopping || status.external || !!status.tool;
  const unsaved = Object.values(dirty).some(Boolean);
  const ready = update?.phase === "ready";
  const visible = !!update?.latest && update.phase !== "idle" && update.phase !== "current";
  const close = () => { setOpen(false); trigger.current?.focus(); };

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") { setOpen(false); trigger.current?.focus(); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  if (!visible && !open) return null;
  return <>
    {visible && <button ref={trigger} className="update-entry" title={updateMessage(update)} onClick={() => setOpen(true)}>
      {busy ? <Spinner size={16} /> : <Download size={16} />}<span className="ellipsis">{entryLabel(update)}</span>
    </button>}
    {open && update && <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="update-title">
        <h2 className="dialog-title" id="update-title">应用更新</h2>
        <div className="muted selectable">{update.current} → {update.latest}</div>
        {update.notes && <div className="dialog-body selectable" style={{ marginTop: 14, maxHeight: 220, overflow: "auto", overflowWrap: "anywhere" }}>{update.notes}</div>}
        {update.phase === "downloading" && <progress value={update.progress} max={100} aria-label="下载进度"
          style={{ display: "block", width: "100%", marginTop: 14, accentColor: "var(--accent)" }} />}
        <div className={error || update.phase === "error" ? "field-error selectable" : "muted"} style={{ marginTop: 12 }}>
          {error || updateMessage(update)}
        </div>
        {ready && (locked || unsaved) && <div className="note">{locked ? "关闭校内访问并结束工具后可安装" : "保存或放弃未保存的修改后可安装"}</div>}
        <div className="dialog-actions">
          <button className="btn" autoFocus onClick={close}>关闭</button>
          {ready ? <button className="btn primary" onClick={install} disabled={busy || locked || unsaved}>安装并重启</button> :
            <button className="btn primary" onClick={download} disabled={busy || !update.latest}>
              {update.phase === "downloading" ? <Spinner /> : <Download size={15} />}下载更新
            </button>}
        </div>
      </div>
    </div>}
  </>;
}
