import { useEffect, useState } from "react";
import { Eye, EyeOff, Plus, X } from "lucide-react";
import { availability, dnsColumns, forwardingColumns, getValue, listText, parseList, tableRows, type Field, type Values } from "../lib/settingsForm";
import { Switch } from "./ui";

const authTypes = ["auth/httpsOauth2", "auth/psw", "auth/cas", "auth/smsCheckCode"];

function SecretInput({ value, onChange, disabled }: { value: string; onChange: (v: string) => void; disabled: boolean }) {
  const [shown, setShown] = useState(false);
  return (
    <div className="secret">
      <input className="input" type={shown ? "text" : "password"} value={value} disabled={disabled} autoComplete="off"
        onChange={(e) => onChange(e.target.value)} />
      <button type="button" className="secret-toggle" onClick={() => setShown(!shown)} aria-label={shown ? "隐藏" : "显示"} title={shown ? "隐藏" : "显示"}>
        {shown ? <EyeOff size={15} /> : <Eye size={15} />}
      </button>
    </div>
  );
}

// Keeps the raw text locally so typing a newline is not swallowed by parsing.
function ListInput({ value, onChange, disabled }: { value: unknown; onChange: (v: string[]) => void; disabled: boolean }) {
  const [text, setText] = useState(listText(value));
  useEffect(() => {
    if (JSON.stringify(parseList(text)) !== JSON.stringify(Array.isArray(value) ? value : [])) setText(listText(value));
  }, [value]);
  return (
    <textarea className="textarea" value={text} disabled={disabled} placeholder="每行一项"
      onChange={(e) => { setText(e.target.value); onChange(parseList(e.target.value)); }} />
  );
}

function TableInput({ value, columns, onChange, disabled, forwarding }: {
  value: unknown;
  columns: { key: string; label: string }[];
  onChange: (rows: Record<string, string>[]) => void;
  disabled: boolean;
  forwarding: boolean;
}) {
  const rows = tableRows(value, columns);
  const template = `${columns.map((c) => (c.key === "network_type" ? "96px" : "minmax(0, 1fr)")).join(" ")} 32px`;
  const update = (index: number, key: string, cell: string) => onChange(rows.map((r, i) => (i === index ? { ...r, [key]: cell } : r)));
  return (
    <div className="table-edit">
      {rows.map((row, index) => (
        <div key={index} className="tr" style={{ gridTemplateColumns: template }}>
          {columns.map((c) => c.key === "network_type" ? (
            <select key={c.key} className="select" value={row[c.key] || "tcp"} disabled={disabled} onChange={(e) => update(index, c.key, e.target.value)}>
              <option value="tcp">tcp</option>
              <option value="udp">udp</option>
            </select>
          ) : (
            <input key={c.key} className="input mono" value={row[c.key]} placeholder={c.label} disabled={disabled} onChange={(e) => update(index, c.key, e.target.value)} />
          ))}
          <button type="button" className="btn ghost icon-btn" disabled={disabled} aria-label="删除" title="删除"
            onClick={() => onChange(rows.filter((_, i) => i !== index))}><X size={15} /></button>
        </div>
      ))}
      <div>
        <button type="button" className="btn sm" disabled={disabled}
          onClick={() => onChange([...rows, Object.fromEntries(columns.map((c) => [c.key, forwarding && c.key === "network_type" ? "tcp" : ""]))])}>
          <Plus size={14} />添加一行
        </button>
      </div>
    </div>
  );
}

const wideKinds = new Set(["list", "forwarding", "dns"]);

export function SettingRow({ field, values, locked, onChange }: { field: Field; values: Values; locked: boolean; onChange: (key: string, value: unknown) => void }) {
  const raw = getValue(values, field.key);
  const state = availability(field, values);
  const disabled = locked || !state.enabled;
  const text = raw === undefined || raw === null ? "" : String(raw);
  const set = (v: unknown) => onChange(field.key, v);
  const wide = wideKinds.has(field.kind);
  let control;
  switch (field.kind) {
    case "bool":
      control = <Switch checked={raw === true} disabled={disabled} onChange={set} label={field.label} />;
      break;
    case "protocol":
      control = (
        <select className="select" value={text} disabled={disabled} onChange={(e) => set(e.target.value)}>
          <option value="atrust">aTrust</option>
          <option value="easyconnect">EasyConnect</option>
        </select>
      );
      break;
    case "auth":
      control = (
        <>
          <input className="input mono" list="auth-types" value={text} disabled={disabled} onChange={(e) => set(e.target.value)} />
          <datalist id="auth-types">{authTypes.map((a) => <option key={a} value={a} />)}</datalist>
        </>
      );
      break;
    case "secret":
      control = <SecretInput value={text} disabled={disabled} onChange={set} />;
      break;
    case "number":
      control = <input className="input number" inputMode="numeric" value={text} disabled={disabled} onChange={(e) => set(e.target.value)} />;
      break;
    case "list":
      control = <ListInput value={raw} disabled={disabled} onChange={set} />;
      break;
    case "forwarding":
      control = <TableInput value={raw} columns={forwardingColumns} disabled={disabled} onChange={set} forwarding />;
      break;
    case "dns":
      control = <TableInput value={raw} columns={dnsColumns} disabled={disabled} onChange={set} forwarding={false} />;
      break;
    default:
      control = <input className={`input${field.kind === "file" ? " mono" : ""}`} value={text} disabled={disabled} onChange={(e) => set(e.target.value)} />;
  }
  return (
    <div className={`setting${wide ? " top" : ""}`} title={state.enabled ? undefined : state.reason}>
      <div className={`setting-label${state.enabled ? "" : " dim"}`}>{field.label}</div>
      <div className={`setting-control${wide ? " wide" : ""}`}>{control}</div>
    </div>
  );
}
