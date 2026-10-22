import { useEffect, useRef, useState, type MouseEvent } from "react";
import { ExternalLink, RotateCcw } from "lucide-react";
import { AppService, CoreService } from "../lib/api";
import { useStore } from "../store";

export type Challenge = { [key: string]: unknown };

function imageSource(data: string): string {
  const type = data.startsWith("/9j/") ? "jpeg" : data.startsWith("R0lG") ? "gif" : "png";
  return `data:image/${type};base64,${data}`;
}

const titles: Record<string, string> = {
  code: "输入验证码",
  "text-captcha": "输入图形验证码",
  "click-captcha": "按提示点选图片",
  "external-login": "在浏览器中登录",
};

export function ChallengeDialog({ challenge, onClose }: { challenge: Challenge; onClose: () => void }) {
  const { fail, status } = useStore();
  const kind = String(challenge.type ?? "");
  const [input, setInput] = useState("");
  const [skip, setSkip] = useState(false);
  const [points, setPoints] = useState<{ x: number; y: number }[]>([]);
  const [error, setError] = useState("");
  const imageRef = useRef<HTMLImageElement>(null);
  const message = String(challenge.message || "") || "请输入验证码。";
  const image = typeof challenge.image === "string" ? challenge.image : "";
  const canSkip = challenge.can_skip === true;

  // The core gave up (or exited); the prompt no longer has anyone to answer.
  const seenRunning = useRef(status.running);
  useEffect(() => {
    if (status.running) seenRunning.current = true;
    else if (seenRunning.current) onClose();
  }, [status.running, onClose]);

  const send = async (answer: Challenge) => {
    try {
      await CoreService.Respond(answer);
    } catch (e) {
      fail(e);
    }
    onClose();
  };

  const cancel = () => send({ id: challenge.id, error: "cancelled" });

  const submit = () => {
    if (kind === "click-captcha" ? points.length === 0 : !input.trim()) {
      setError(kind === "click-captcha" ? "请先在图片上点选" : kind === "external-login" ? "请粘贴回调地址" : "请输入内容");
      return;
    }
    const answer: Challenge = { id: challenge.id, code: input.trim(), skip, callback: input.trim() };
    if (kind === "click-captcha" && imageRef.current) {
      answer.points = points;
      answer.width = imageRef.current.naturalWidth;
      answer.height = imageRef.current.naturalHeight;
    }
    send(answer);
  };

  const pick = (e: MouseEvent<HTMLImageElement>) => {
    const img = e.currentTarget;
    const rect = img.getBoundingClientRect();
    const x = Math.round(((e.clientX - rect.left) * img.naturalWidth) / rect.width);
    const y = Math.round(((e.clientY - rect.top) * img.naturalHeight) / rect.height);
    if (x >= 0 && y >= 0 && x < img.naturalWidth && y < img.naturalHeight && points.length < 12) {
      setPoints([...points, { x, y }]);
      setError("");
    }
  };

  const openLogin = async () => {
    try {
      await AppService.OpenURL(String(challenge.login_url ?? ""));
    } catch (e) {
      fail(e);
    }
  };

  return (
    <div className="overlay">
      <form className="dialog wide" role="dialog" aria-modal="true" aria-labelledby="challenge-title"
        onSubmit={(e) => { e.preventDefault(); submit(); }}>
        <h2 className="dialog-title" id="challenge-title">{titles[kind] ?? "FDU 登录验证"}</h2>
        <div className="dialog-body selectable">{message}</div>
        {image && (
          <div className={`captcha${kind === "click-captcha" ? " clickable" : ""}`}>
            <img ref={imageRef} src={imageSource(image)} alt="验证码" draggable={false}
              onClick={kind === "click-captcha" ? pick : undefined} />
            {kind === "click-captcha" && imageRef.current && points.map((p, i) => (
              <span key={i} className="captcha-mark"
                style={{ left: `${(p.x / imageRef.current!.naturalWidth) * 100}%`, top: `${(p.y / imageRef.current!.naturalHeight) * 100}%` }}>
                {i + 1}
              </span>
            ))}
          </div>
        )}
        {kind === "click-captcha" && (
          <div className="row" style={{ marginTop: 10 }}>
            <span className="spacer" />
            <button type="button" className="btn sm" onClick={() => setPoints([])}><RotateCcw size={14} />重新点选</button>
          </div>
        )}
        {kind === "external-login" && (
          <button type="button" className="btn" style={{ marginTop: 14 }} onClick={openLogin}>
            <ExternalLink size={15} />打开登录网页
          </button>
        )}
        {kind !== "click-captcha" && (
          <div style={{ marginTop: 14 }}>
            <input className={`input${error ? " invalid" : ""}`} autoFocus value={input}
              placeholder={kind === "external-login" ? "粘贴浏览器地址栏中的回调地址" : "验证码"}
              onChange={(e) => { setInput(e.target.value); setError(""); }} />
          </div>
        )}
        {error && <div className="field-error">{error}</div>}
        {canSkip && (
          <label className="row" style={{ marginTop: 12, cursor: "pointer" }}>
            <input type="checkbox" checked={skip} onChange={(e) => setSkip(e.target.checked)} />
            跳过二次认证
          </label>
        )}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={cancel}>取消</button>
          <button type="submit" className="btn primary">确认</button>
        </div>
      </form>
    </div>
  );
}
