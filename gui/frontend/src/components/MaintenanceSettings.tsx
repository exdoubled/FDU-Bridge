import { useState } from "react";
import { Wrench } from "lucide-react";
import { AppService, MaintenanceService } from "../lib/api";
import { useStore } from "../store";
import { Hint, Spinner } from "./ui";
import { useUpdates } from "./UpdateControl";

export function MaintenanceSettings({ disabled, migrationDisabled, onMigrated }: { disabled: boolean; migrationDisabled: boolean; onMigrated: () => Promise<void> }) {
  const { status, dirty, confirm, fail, toast, setConfig } = useStore();
  const updates = useUpdates();
  const [busy, setBusy] = useState(false);
  const locked = Object.values(dirty).some(Boolean) || status.running || status.external || status.starting || status.stopping || updates.busy || busy;

  const remove = async () => {
    if (!(await confirm({ title: "移除 FDU 接入？", message: "移除当前接入记录对应客户端/订阅中可核验的 FDU 节点、监听与规则扩展，保留订阅和个人设置。下次启用需重新确认接入。", confirm: "移除接入", danger: true }))) return;
    setBusy(true);
    try {
      const backup = await MaintenanceService.RemoveIntegration();
      setConfig(await AppService.Config());
      toast("success", `已移除 FDU 接入，恢复备份：${backup}`);
    } catch (error) { fail(error); }
    finally { setBusy(false); }
  };

  const migrate = async () => {
    setBusy(true);
    try {
      const preview = await MaintenanceService.PreviewMigration();
      if (!preview) return;
      if (preview.conflicts.length) {
        await confirm({ title: "目标已有不同数据", message: `为避免覆盖，不能迁移以下文件：\n${preview.conflicts.join("\n")}`, confirm: "知道了" });
        return;
      }
      if (!(await confirm({ title: "复制旧 GUI 配置？", message: `来源：${preview.directory}\n\n将复制 ${preview.files.length} 个受管文件：\n${preview.files.join("\n")}\n\n保留旧目录，不复制 Clash 接入恢复记录。`, confirm: "复制配置" }))) return;
      await MaintenanceService.Migrate();
      setConfig(await AppService.Config());
      await onMigrated();
      toast("success", "已复制旧 GUI 配置，原目录保持不变");
    } catch (error) { fail(error); }
    finally { setBusy(false); }
  };

  return <section className="card section" style={{ padding: 0 }}>
    <div className="section-head"><Wrench size={16} />维护</div>
    <div className="setting">
      <div className="setting-label">移除 FDU 接入<Hint text="关闭连接并保存或放弃草稿后可用" /></div>
      <div className="setting-control"><button className="btn danger" disabled={locked || disabled} onClick={remove}>{busy && <Spinner />}移除接入</button></div>
    </div>
    <div className="setting">
      <div className="setting-label">从旧目录迁移<Hint text="仅复制受管配置、规则和登录状态" /></div>
      <div className="setting-control"><button className="btn" disabled={locked || migrationDisabled} onClick={migrate}>选择旧目录</button></div>
    </div>
  </section>;
}
