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
  it("requires every credential for UIS login", () => {
    expect(missingUISCredentials({ ...uis, ...credentials })).toBe(false);
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
  it("reports saved once every field matches the stored value", () => {
    expect(render(credentials, credentials)).toContain("已自动保存");
  });

  it("does not report saved while a field is blank or unsaved", () => {
    expect(render({ ...credentials, totp_secret: "" }, { ...credentials, totp_secret: "" })).not.toContain("已自动保存");
    expect(render({ ...credentials, password: "changed" }, credentials)).not.toContain("已自动保存");
  });

  it("keeps inputs editable unless the caller disables them", () => {
    const inputs = (html: string) => html.match(/<input[^>]*>/g) ?? [];
    expect(inputs(render(credentials, credentials))).toHaveLength(3);
    expect(inputs(render(credentials, credentials)).some((tag) => tag.includes("disabled"))).toBe(false);
    expect(inputs(render(credentials, credentials, true)).every((tag) => tag.includes("disabled"))).toBe(true);
  });
});
