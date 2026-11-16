import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, expect, it, vi } from "vitest";
import { Home } from "./Home";

const mock = vi.hoisted(() => ({ store: {} as Record<string, unknown> }));
vi.mock("../store", () => ({ useStore: () => mock.store }));
vi.mock("../lib/api", () => ({ ClashService: {}, CoreService: {}, RulesService: {}, errorText: String }));
vi.mock("../components/NetworkTools", () => ({ CampusChecks: () => null, TrafficCards: () => null }));

beforeEach(() => {
  mock.store = { status: { state: "idle", socks: "127.0.0.1:1080", http: "127.0.0.1:1081" }, versions: {},
    config: { integration: { mode: "proxy" }, standaloneMode: "rule", standaloneTun: false } };
});

it("shows standalone routing controls and proxy addresses instead of Clash nodes", () => {
  const html = renderToStaticMarkup(<Home />);
  expect(html).toContain("独立使用");
  expect(html).toContain("FDU TUN");
  expect(html).toContain("校园规则走 FDU，其余流量直连");
  expect(html).toContain("127.0.0.1:1080");
  expect(html).not.toContain("外网节点");
});

it("locks usage, routing and TUN controls while connecting", () => {
  mock.store.status = { state: "connecting", starting: true };
  const html = renderToStaticMarkup(<Home />);
  expect(html).toContain("断开后可切换");
  const radios = html.match(/<button[^>]+role="radio"[^>]*>/g) ?? [];
  expect(radios).toHaveLength(4);
  expect(radios.every((tag) => tag.includes("disabled"))).toBe(true);
});

it("keeps standalone controls out of Clash mode", () => {
  mock.store.config = { integration: { mode: "manual" } };
  const html = renderToStaticMarkup(<Home />);
  expect(html).toContain("外网节点");
  expect(html).not.toContain("FDU TUN");
});
