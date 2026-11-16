import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { missingUISCredentials } from "../lib/settingsForm";
import { UISLogin } from "./UISLogin";

vi.mock("../lib/api", () => ({ errorText: String }));

const uis = { protocol: "atrust", auth_type: "auth/httpsOauth2", login_domain: "id.fudan.edu.cn" };
const credentials = { username: "2024000000", password: "secret", totp_secret: "JBSWY3DPEHPK3PXP" };

function render(values: Record<string, unknown>, saved: Record<string, unknown>, disabled = false) {
  return renderToStaticMarkup(<UISLogin values={values} saved={saved} disabled={disabled} onChange={() => undefined} onSave={async () => undefined} />);
}

describe("missingUISCredentials", () => {
  it("requires every credential once any is filled", () => {
    expect(missingUISCredentials({ ...uis, ...credentials })).toBe(false);
    expect(missingUISCredentials(uis)).toBe(false);
    for (const key of Object.keys(credentials)) {
      expect(missingUISCredentials({ ...uis, ...credentials, [key]: "  " })).toBe(true);
    }
  });

  it("ignores credentials when UIS login does not apply", () => {
    expect(missingUISCredentials({ ...uis, oauth2_code: "code" })).toBe(false);
    expect(missingUISCredentials({ ...uis, login_domain: "other.example" })).toBe(false);
    expect(missingUISCredentials({ protocol: "easyconnect" })).toBe(false);
  });
});

describe("UISLogin", () => {
  it("reports ready once every field matches the stored value", () => {
    const html = render(credentials, credentials);
    expect(html).toContain("已就绪");
    expect(html.match(/已保存/g) ?? []).toHaveLength(3);
  });

  it("flags partly filled credentials and browser fallback", () => {
    expect(render(uis, uis)).toContain("使用网页登录");
    expect(render({ ...credentials, totp_secret: "" }, { ...credentials, totp_secret: "" })).toContain("未填写完整");
    const unsaved = render({ ...credentials, password: "changed" }, credentials);
    expect(unsaved).not.toContain("已就绪");
    expect(unsaved).toContain("离开输入框后保存");
  });

  it("names each field and exposes password toggles", () => {
    const html = render(credentials, credentials);
    for (const [key, label] of [["username", "UIS 账号"], ["password", "UIS 密码"], ["totp_secret", "TOTP 密钥"]]) {
      expect(html).toContain(`id="uis-${key}"`);
      expect(html).toContain(`for="uis-${key}"`);
      expect(html).toContain(label);
    }
    expect(html).toContain("显示UIS 密码");
    expect(html).toContain("显示TOTP 密钥");
    expect(html).not.toContain("显示UIS 账号");
  });

  it("keeps inputs editable unless the caller disables them", () => {
    const inputs = (html: string) => html.match(/<input[^>]*>/g) ?? [];
    expect(inputs(render(credentials, credentials))).toHaveLength(3);
    expect(inputs(render(credentials, credentials)).some((tag) => tag.includes("disabled"))).toBe(false);
    expect(inputs(render(credentials, credentials, true)).every((tag) => tag.includes("disabled"))).toBe(true);
  });
});
