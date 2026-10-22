import { useEffect } from "react";
import { useStore } from "../store";
import { toastIcons } from "./ui";

export function ConfirmDialog() {
  const { pending, settle } = useStore();
  useEffect(() => {
    if (!pending) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") settle(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pending, settle]);
  if (!pending) return null;
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && settle(false)}>
      <div className="dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
        <h2 className="dialog-title" id="confirm-title">{pending.title}</h2>
        <div className="dialog-body selectable">{pending.message}</div>
        <div className="dialog-actions">
          <button className="btn" onClick={() => settle(false)}>{pending.cancel ?? "取消"}</button>
          <button className={`btn ${pending.danger ? "danger" : "primary"}`} autoFocus onClick={() => settle(true)}>
            {pending.confirm ?? "确定"}
          </button>
        </div>
      </div>
    </div>
  );
}

export function Toasts() {
  const { toasts } = useStore();
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => {
        const Icon = toastIcons[t.kind];
        return (
          <div key={t.id} className={`toast ${t.kind}`}>
            <Icon size={16} />
            <div className="selectable">{t.text}</div>
          </div>
        );
      })}
    </div>
  );
}
