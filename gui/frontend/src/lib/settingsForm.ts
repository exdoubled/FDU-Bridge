// Pure helpers that map the backend's field schema onto the settings page layout.

export interface Field {
  key: string;
  label: string;
  group: string;
  kind: string;
}

export type Values = { [key: string]: unknown };

/** Groups with their own section; the rest fold into 高级. */
export const primaryGroups: { group: string; title: string }[] = [
  { group: "登录", title: "账户与服务器" },
  { group: "本地代理", title: "本地代理" },
  { group: "分流", title: "分流" },
];

export const advancedOrder = ["DNS", "网络", "aTrust", "EasyConnect", "端口转发", "调试"];

/** Fields the core refuses to combine with selective routing. */
export const routingConflicts = new Set([
  "tun_mode", "add_route", "dns_hijack", "fake_ip", "proxy_all", "tcp_tunnel_mode", "dial_direct_proxy",
  "disable_server_config", "shadowsocks_url", "dns_server_bind", "port_forwarding", "custom_proxy_domain",
]);

export function getValue(values: Values, key: string): unknown {
  let current: unknown = values;
  for (const part of key.split(".")) {
    if (!current || typeof current !== "object") return undefined;
    current = (current as Values)[part];
  }
  return current;
}

/** Returns a copy with key set, creating nested objects for dotted keys. */
export function setValue(values: Values, key: string, value: unknown): Values {
  const parts = key.split(".");
  const root: Values = { ...values };
  let target = root;
  for (const part of parts.slice(0, -1)) {
    const child = target[part];
    const copy: Values = child && typeof child === "object" && !Array.isArray(child) ? { ...(child as Values) } : {};
    target[part] = copy;
    target = copy;
  }
  target[parts[parts.length - 1]] = value;
  return root;
}

export function fieldsOf(fields: Field[], group: string): Field[] {
  return fields.filter((f) => f.group === group);
}

export interface Availability {
  enabled: boolean;
  reason?: string;
}

export function availability(field: Field, values: Values): Availability {
  const protocol = String(getValue(values, "protocol") ?? "");
  if (field.group === "aTrust" && protocol !== "atrust") return { enabled: false, reason: "仅 aTrust 可用" };
  if (field.group === "EasyConnect" && protocol === "atrust") return { enabled: false, reason: "仅 EasyConnect 可用" };
  if (getValue(values, "routing.enabled") === true && routingConflicts.has(field.key)) return { enabled: false, reason: "启用分流时不可用" };
  return { enabled: true };
}

/** Lists travel as one entry per line in a textarea. */
export function listText(value: unknown): string {
  return Array.isArray(value) ? value.map(String).join("\n") : "";
}

export function parseList(text: string): string[] {
  return text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
}

export function numberError(field: Field, value: unknown): string | null {
  if (field.kind !== "number") return null;
  const n = Number(value);
  return Number.isInteger(n) && n >= 0 ? null : `${field.label}需要非负整数`;
}

/** Converts edited number strings back to numbers before saving. */
export function prepareForSave(fields: Field[], values: Values): { values: Values; error: string | null } {
  let result = values;
  for (const field of fields) {
    if (field.kind !== "number") continue;
    const raw = getValue(values, field.key);
    const error = numberError(field, raw);
    if (error) return { values, error };
    result = setValue(result, field.key, Number(raw));
  }
  return { values: result, error: null };
}

export const forwardingColumns = [
  { key: "network_type", label: "tcp / udp" },
  { key: "bind_address", label: "本地地址:端口" },
  { key: "remote_address", label: "远端地址:端口" },
];

export const dnsColumns = [
  { key: "host_name", label: "域名" },
  { key: "ip", label: "IP" },
];

export function tableRows(value: unknown, columns: { key: string }[]): Record<string, string>[] {
  if (!Array.isArray(value)) return [];
  return value.map((item) => Object.fromEntries(columns.map((c) => [c.key, String((item as Values)?.[c.key] ?? "")])));
}
