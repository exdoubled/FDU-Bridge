import { useState } from "react";
import { Wrench } from "lucide-react";
import { AppService, MaintenanceService } from "../lib/api";
import { useStore } from "../store";
import { Spinner } from "./ui";
import { useUpdates } from "./UpdateControl";

export function MaintenanceSettings({ disabled }: { disabled: boolean }) {
  const { status, dirty, confirm, fail, toast, setConfig } = useStore();
  const updates = useUpdates();
  const [busy, setBusy] = useState(false);
  const locked = disabled || Object.values(dirty).some(Boolean) || status.running || status.starting || status.stopping || !!status.tool || updates.busy || busy;

  const remove = async () => {
    if (!(await confirm({ title: "移除 FDU 接入？", message: "移除当前客户端中可核验的 FDU 节点、监听与规则扩展，保留订阅和个人设置。下次启用需重新确认接入。", confirm: "移除接入", danger: true }))) return;
    setBusy(true);
    try {
      const backup = await MaintenanceService.RemoveIntegration();
      setConfig(await AppService.Config());
      toast("success", `已移除 FDU 接入，恢复备份：${backup}`);
    } catch (error) { fail(error); }
    finally { setBusy(false); }
  };

  return <section className="card section" style={{ padding: 0 }}>
    <div className="section-head"><Wrench size={16} />维护</div>
    <div className="setting">
      <div className="setting-label">移除 FDU 接入<span className="muted" style={{ display: "block", fontSize: 12 }}>关闭连接并保存或放弃草稿后可用</span></div>
      <div className="setting-control"><button className="btn" disabled={locked} onClick={remove}>{busy && <Spinner />}移除接入</button></div>
    </div>
  </section>;
}
