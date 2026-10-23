import { Activity, Globe, House, Route, ScrollText, Settings } from "lucide-react";
import { useStore, type Page } from "../store";
import { stateLabel, stateTone } from "../lib/status";
import { UpdateControl } from "./UpdateControl";

const items: { page: Page; label: string; icon: typeof House }[] = [
  { page: "home", label: "首页", icon: House },
  { page: "nodes", label: "外网节点", icon: Globe },
  { page: "rules", label: "校内资源", icon: Route },
  { page: "network", label: "网络检测", icon: Activity },
  { page: "logs", label: "诊断", icon: ScrollText },
  { page: "settings", label: "设置", icon: Settings },
];

export function Sidebar() {
  const { page, navigate, status, dirty } = useStore();
  const tone = stateTone(status);
  return (
    <nav className="sidebar" aria-label="主导航">
      <div className="brand">
        <div className="brand-mark">F</div>
        FDU Connect
      </div>
      {items.map(({ page: target, label, icon: Icon }) => (
        <button key={target} className={`nav-item${page === target ? " active" : ""}`} onClick={() => navigate(target)}
          aria-current={page === target ? "page" : undefined}>
          <Icon size={18} />
          {label}
          {dirty[target] && <span className="dirty-dot" title="有未保存的修改" />}
        </button>
      ))}
      <div className="sidebar-foot">
        <UpdateControl />
        <div className="sidebar-status">
          <span className={`dot ${tone === "off" ? "" : tone}`} />
          <span className="ellipsis" title={stateLabel(status)}>{stateLabel(status)}</span>
        </div>
      </div>
    </nav>
  );
}
