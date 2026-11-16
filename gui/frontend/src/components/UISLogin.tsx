import { useEffect, useState } from "react";
import { CheckCircle2, CircleAlert, Eye, EyeOff, KeyRound, ShieldCheck } from "lucide-react";
import { errorText } from "../lib/api";
import type { Values } from "../lib/settingsForm";
import { Hint } from "./ui";

const fields = [
  { key: "username", label: "UIS 账号", type: "text", autoComplete: "username", placeholder: "学号或工号", hint: "登录 UIS 时使用的账号。" },
  { key: "password", label: "UIS 密码", type: "password", autoComplete: "current-password", placeholder: "UIS 密码", hint: "只保存在本机，仅提交给 UIS。" },
  { key: "totp_secret", label: "TOTP 密钥", type: "password", autoComplete: "off", placeholder: "例如 JBSWY3DPEHPK3PXP", hint: "绑定验证器时提供的 Base32 密钥，大小写、空格和末尾 = 都能识别。" },
] as const;

type State = "idle" | "pending" | "saved" | "error";

export function UISLogin({ values, saved, disabled, onChange, onSave }: {
  values: Values;
  saved: Values;
  disabled: boolean;
  onChange: (key: string, value: string) => void;
  onSave: (key: string, value: string) => Promise<void>;
}) {
  const [pending, setPending] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const [error, setError] = useState("");

  const text = (key: string) => String(values[key] ?? "");
  const stored = (key: string) => String(saved[key] ?? "");
  const dirty = (key: string) => text(key) !== stored(key);
  const complete = fields.every(({ key }) => !!text(key) && !dirty(key));
  const anyFilled = fields.some(({ key }) => !!text(key));

  useEffect(() => { setError(""); }, [saved]);

  const save = async (key: string) => {
    if (!dirty(key)) return;
    setPending(key);
    setError("");
    try {
      await onSave(key, text(key));
    } catch (e) {
      setError(errorText(e));
    } finally {
      setPending(null);
    }
  };

  const stateOf = (key: string): State => {
    if (pending === key) return "pending";
    if (dirty(key)) return "idle";
    return stored(key) ? "saved" : "idle";
  };

  return (
    <section className="card section uis-card" id="uis-login" aria-label="UIS 自动登录">
      <div className="section-head">
        <ShieldCheck size={16} />UIS 自动登录
        <span className={`pill uis-status ${complete ? "success" : anyFilled ? "warning" : ""}`}>
          {complete ? "已就绪" : anyFilled ? "未填写完整" : "使用网页登录"}
        </span>
      </div>
      <p className="uis-intro">
        填写账号、密码和 TOTP 密钥后，启用校内访问时自动完成两步验证，不再弹出登录网页。
        三项全部留空则回退到网页登录，适用于需要图形验证码、只能短信验证或未绑定验证器的账号。
      </p>

      {fields.map(({ key, label, type, autoComplete, placeholder, hint }) => {
        const state = stateOf(key);
        const shown = !!revealed[key];
        const toggleable = type === "password";
        return (
          <div className="setting top uis-row" key={key}>
            <label className="setting-label uis-label" htmlFor={`uis-${key}`}>
              {label}<Hint text={hint} />
            </label>
            <div className="setting-control uis-control">
              <div className={`uis-input${toggleable ? " has-toggle" : ""}`}>
                <input
                  id={`uis-${key}`}
                  className="input"
                  type={toggleable && !shown ? "password" : "text"}
                  autoComplete={autoComplete}
                  spellCheck={false}
                  placeholder={placeholder}
                  disabled={disabled}
                  value={text(key)}
                  onBlur={() => save(key)}
                  onChange={(e) => onChange(key, e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") (e.currentTarget as HTMLInputElement).blur(); }}
                />
                {toggleable && (
                  <button type="button" className="uis-toggle" tabIndex={-1}
                    aria-label={shown ? `隐藏${label}` : `显示${label}`} title={shown ? "隐藏" : "显示"}
                    onClick={() => setRevealed((r) => ({ ...r, [key]: !shown }))}>
                    {shown ? <EyeOff size={15} /> : <Eye size={15} />}
                  </button>
                )}
              </div>
              <span className={`uis-save-state ${state}`} role="status">
                {state === "pending" && "保存中…"}
                {state === "saved" && <><CheckCircle2 size={13} />已保存</>}
                {state === "idle" && dirty(key) && "离开输入框后保存"}
                {state === "idle" && !dirty(key) && anyFilled && !text(key) && <span className="uis-missing">需要填写</span>}
              </span>
            </div>
          </div>
        );
      })}

      <div className="uis-foot">
        <div className="uis-note">
          <KeyRound size={14} />
          <span>
            密码和 TOTP 密钥以明文保存在本机 <code>client.toml</code>，接入 Clash 时生成的配置备份也会包含它们，请勿分享这些文件。
          </span>
        </div>
        {error && <div className="field-error uis-error" role="alert"><CircleAlert size={14} />{error}</div>}
      </div>
    </section>
  );
}
