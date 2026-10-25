import { useEffect, useRef, useState } from "react";
import { Copy, Download, FileText } from "lucide-react";
import { NetworkService } from "../lib/api";
import { useStore } from "../store";
import { Spinner } from "./ui";

// Mount with the diagnosis result so each result owns its preview dialog.
export function DiagnosisReport({ disabled = false }: { disabled?: boolean }) {
  const { toast, fail } = useStore();
  const [report, setReport] = useState("");
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const close = () => { if (!busy) setReport(""); };

  useEffect(() => {
    if (!report) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) setReport("");
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [report, busy]);

  const preview = async () => {
    setBusy(true);
    try { setReport(await NetworkService.PreviewDiagnosisReport()); }
    catch (error) { fail(error); }
    finally { setBusy(false); }
  };

  const copy = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(report);
      toast("success", "已复制脱敏报告");
    } catch {
      field.current?.focus();
      field.current?.select();
      toast("info", "已选中报告，可使用系统复制快捷键复制");
    }
  };

  const download = async () => {
    setBusy(true);
    try {
      const path = await NetworkService.ExportDiagnosisReport();
      if (path) toast("success", `已导出到 ${path}`);
    } catch (error) { fail(error); }
    finally { setBusy(false); }
  };

  return <>
    <button className="btn sm" disabled={disabled || busy} onClick={preview}>
      {busy && !report ? <Spinner /> : <FileText size={14} />}预览脱敏报告
    </button>
    {report && <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <div className="dialog wide" role="dialog" aria-modal="true" aria-labelledby="diagnosis-report-title" aria-describedby="diagnosis-report-note">
        <h2 className="dialog-title" id="diagnosis-report-title">脱敏诊断报告</h2>
        <p className="muted" id="diagnosis-report-note">仅包含本次诊断的安全摘要，不含原始配置或完整日志。自定义校园主机及 IP 同样隐藏，请在分享前检查下方内容。</p>
        <textarea ref={field} className="textarea diagnosis-report-preview" readOnly value={report} aria-label="脱敏诊断报告内容" rows={16} autoFocus />
        <div className="dialog-actions">
          <button className="btn" disabled={busy} onClick={close}>关闭</button>
          <button className="btn" disabled={busy} onClick={copy}><Copy size={14} />复制</button>
          <button className="btn primary" disabled={busy} onClick={download}>{busy ? <Spinner /> : <Download size={14} />}导出文本</button>
        </div>
      </div>
    </div>}
  </>;
}
