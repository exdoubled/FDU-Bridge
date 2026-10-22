// Pure edits on a rules draft; nothing here touches the file until the page saves.

export type RuleType = "domain" | "suffix" | "cidr";

export interface Draft {
  domains: string[];
  domain_suffixes: string[];
  ip_cidrs: string[];
  disabled: { domains: string[]; domain_suffixes: string[]; ip_cidrs: string[] };
}

export interface RuleRow {
  type: RuleType;
  value: string;
  enabled: boolean;
}

const lists = { domain: "domains", suffix: "domain_suffixes", cidr: "ip_cidrs" } as const;

export const typeLabels: Record<RuleType, string> = { domain: "精确域名", suffix: "域名及子域名", cidr: "IPv4 网段" };

export function emptyDraft(): Draft {
  return { domains: [], domain_suffixes: [], ip_cidrs: [], disabled: { domains: [], domain_suffixes: [], ip_cidrs: [] } };
}

export function cloneDraft(d: Draft): Draft {
  return {
    domains: [...d.domains],
    domain_suffixes: [...d.domain_suffixes],
    ip_cidrs: [...d.ip_cidrs],
    disabled: { domains: [...d.disabled.domains], domain_suffixes: [...d.disabled.domain_suffixes], ip_cidrs: [...d.disabled.ip_cidrs] },
  };
}

export function rows(d: Draft): RuleRow[] {
  const result: RuleRow[] = [];
  for (const type of ["domain", "suffix", "cidr"] as RuleType[]) {
    const key = lists[type];
    const off = new Set(d.disabled[key]);
    for (const value of d[key]) result.push({ type, value, enabled: !off.has(value) });
  }
  return result;
}

/** Returns null when the canonical value already exists in that type. */
export function addRule(d: Draft, type: RuleType, value: string): Draft | null {
  const key = lists[type];
  if (d[key].includes(value)) return null;
  const next = cloneDraft(d);
  next[key].push(value);
  return next;
}

export function removeRules(d: Draft, targets: { type: RuleType; value: string }[]): Draft {
  const next = cloneDraft(d);
  for (const { type, value } of targets) {
    const key = lists[type];
    next[key] = next[key].filter((v) => v !== value);
    next.disabled[key] = next.disabled[key].filter((v) => v !== value);
  }
  return next;
}

export function setEnabled(d: Draft, type: RuleType, value: string, enabled: boolean): Draft {
  const key = lists[type];
  const next = cloneDraft(d);
  next.disabled[key] = next.disabled[key].filter((v) => v !== value);
  if (!enabled) next.disabled[key].push(value);
  return next;
}

export function sameDraft(a: Draft, b: Draft): boolean {
  return JSON.stringify(normalizeOrder(a)) === JSON.stringify(normalizeOrder(b));
}

function normalizeOrder(d: Draft) {
  const sorted = (v: string[]) => [...v].sort();
  return { ...d, disabled: { domains: sorted(d.disabled.domains), domain_suffixes: sorted(d.disabled.domain_suffixes), ip_cidrs: sorted(d.disabled.ip_cidrs) } };
}

export function filterRows(all: RuleRow[], type: RuleType | "all", query: string): RuleRow[] {
  const q = query.trim().toLowerCase();
  return all.filter((r) => (type === "all" || r.type === type) && (!q || r.value.includes(q)));
}

export function counts(d: Draft) {
  const all = rows(d);
  const enabled = all.filter((r) => r.enabled).length;
  return { total: all.length, enabled, disabled: all.length - enabled };
}
