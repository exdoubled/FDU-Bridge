// One import point for the generated Wails bindings.
export {
  AppService,
  ClashService,
  CoreService,
  RulesService,
  SettingsService,
  MaintenanceService,
  UpdateService,
  ClashOverview,
  CoreStatus,
  RulesView,
  RulesImport,
} from "../../bindings/github.com/mythologyli/zju-connect/gui/services";
export { Document as RuleDocument, Kind as RuleKind } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/rules";
export type { ApplyState } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/rules";
export type { NodeState } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/clash";
export type { View as SettingsView } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/settings";
export type { SettingField } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/clashsetup";
export type { Config as AppConfig } from "../../bindings/github.com/mythologyli/zju-connect/gui/internal/appconf";
export type { UpdateStatus } from "../../bindings/github.com/mythologyli/zju-connect/gui/services";

/** Go errors arrive as rejected promises; show their message, never a stack. */
export function errorText(error: unknown): string {
  if (error && typeof error === "object" && "message" in error) {
    const message = String((error as { message: unknown }).message);
    return message.replace(/^Error:\s*/, "");
  }
  return String(error);
}
