import type { ReactNode } from "react";
import { CircleCheck, CircleX, Info, LoaderCircle, TriangleAlert, type LucideIcon } from "lucide-react";

export function Switch({ checked, onChange, disabled, large, label }: {
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
  large?: boolean;
  label?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`switch${checked ? " on" : ""}${large ? " lg" : ""}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  );
}

export function Segmented<T extends string>({ value, options, onChange, disabled }: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
  disabled?: boolean;
}) {
  return (
    <div className="segmented" role="radiogroup">
      {options.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={o.value === value}
          className={o.value === value ? "active" : ""} disabled={disabled} onClick={() => o.value !== value && onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

const bannerIcons = { info: Info, warning: TriangleAlert, danger: CircleX };

export function Banner({ tone = "info", children, action }: { tone?: "info" | "warning" | "danger"; children: ReactNode; action?: ReactNode }) {
  const Icon = bannerIcons[tone];
  return (
    <div className={`banner ${tone}`}>
      <Icon size={16} />
      <div className="grow">{children}</div>
      {action}
    </div>
  );
}

export function Empty({ icon: Icon, title, children, action, tone }: { icon: LucideIcon; title: string; children?: ReactNode; action?: ReactNode; tone?: "danger" }) {
  return (
    <div className={tone ? `empty ${tone}` : "empty"}>
      <div className="empty-icon"><Icon size={24} /></div>
      <div className="empty-title">{title}</div>
      {children && <div className="muted">{children}</div>}
      {action}
    </div>
  );
}

export function Spinner({ size = 14 }: { size?: number }) {
  return <LoaderCircle size={size} className="spin" />;
}

export function CardTitle({ icon: Icon, children, extra }: { icon: LucideIcon; children: ReactNode; extra?: ReactNode }) {
  return (
    <div className="card-title">
      <Icon size={15} />
      <span>{children}</span>
      {extra && <span style={{ marginLeft: "auto" }}>{extra}</span>}
    </div>
  );
}

export const toastIcons = { success: CircleCheck, error: CircleX, info: Info };
