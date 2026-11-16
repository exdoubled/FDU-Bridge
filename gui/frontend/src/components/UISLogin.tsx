import { useEffect, useState } from "react";
import { errorText } from "../lib/api";
import type { Values } from "../lib/settingsForm";

const fields = [
  { key: "username", label: "UIS 账号", type: "text", autoComplete: "username" },
  { key: "password", label: "UIS 密码", type: "password", autoComplete: "current-password" },
  { key: "totp_secret", label: "TOTP 密钥", type: "password", autoComplete: "off" },
] as const;
export function UISLogin({ values, saved, disabled, onChange, onSave }: {
  values: Values;
  saved: Values;
  disabled: boolean;
  onChange: (key: string, value: string) => void;
  onSave: (key: string, value: string) => Promise<void>;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const complete = fields.every(({ key }) => !!values[key] && values[key] === saved[key]);
  const ready = complete && !pending && !error;

  useEffect(() => { setError(""); }, [saved]);

  const save = async (key: string) => {
    const value = String(values[key] ?? "");
    if (value === saved[key]) { setError(""); return; }
    setPending(true);
    setError("");
    try {
      await onSave(key, value);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(false);
    }
  };

  return (
    <section className="card section" id="uis-login" style={{ padding: 0 }} aria-label="UIS 自动登录">
      <div className="section-head">UIS 自动登录</div>
      <div className="muted" style={{ padding: "0 20px 12px" }}>填写账号、密码和 TOTP 密钥，自动完成验证，无需打开登录网页。</div>
      {fields.map(({ key, label, type, autoComplete }) => (
        <label className="setting" key={key}>
          <span className="setting-label">{label}</span>
          <span className="setting-control">
            <input className="input" type={type} autoComplete={autoComplete} spellCheck={false} disabled={disabled}
              value={String(values[key] ?? "")} aria-label={label} onBlur={() => save(key)}
              onChange={(e) => onChange(key, e.target.value)} />
          </span>
        </label>
      ))}
      <div className="muted" style={{ padding: "12px 20px 16px" }}>
        <div>TOTP 密钥是绑定验证器时的 Base32 密钥，不是每 30 秒变化的六位验证码。</div>
        <div role="status">{pending ? "正在自动保存…" : ready ? "已自动保存，后续登录自动生成动态验证码。" : "填写后离开输入框即自动保存到本机配置。"}</div>
        {error && <div className="field-error" role="alert">{error}</div>}
      </div>
    </section>
  );
}
