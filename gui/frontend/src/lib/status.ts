export type Tone = "off" | "on" | "busy" | "bad";

const labels: Record<string, string> = {
  idle: "未启用校内访问",
  preparing: "正在准备校内访问…",
  connecting: "正在连接校园 VPN…",
  enabling: "正在启用校内访问…",
  authentication: "等待登录验证",
  connected: "校内访问已启用",
  paused: "校内访问已暂停",
  error: "启用失败",
  "campus-unavailable": "校内连接不可用",
  stopped: "已关闭校内访问",
  "tool-running": "正在执行工具…",
  "tool-finished": "工具已结束",
};

export interface StatusLike {
  state: string;
  running: boolean;
  starting: boolean;
  connected: boolean;
  external: boolean;
  stopping: boolean;
}

export function stateLabel(s: StatusLike): string {
  if (s.stopping) return "正在关闭校内访问…";
  if (!s.running && !s.starting && s.external) return "其他 FDU 正在运行";
  if (!s.running && !s.starting && (s.state === "connecting" || s.state === "authentication" || s.state === "connected")) return labels.idle;
  return labels[s.state] ?? s.state;
}

export function stateTone(s: StatusLike): Tone {
  if (s.stopping || s.starting) return "busy";
  if (s.connected) return "on";
  if (s.state === "error" || s.state === "paused" || (s.state === "campus-unavailable" && s.running)) return "bad";
  if (s.running && s.state !== "tool-finished") return "busy";
  return "off";
}

export function formatUptime(since: number, now: number): string {
  const total = Math.max(0, Math.floor((now - since) / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(h)}:${pad(m)}:${pad(s)}`;
}
