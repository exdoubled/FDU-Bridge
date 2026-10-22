import { useCallback, useEffect, useState } from "react";
import { ChevronRight, FileText, FolderOpen, Layers, Palette, RefreshCw, Upload, Wrench } from "lucide-react";
import { AppService, ClashService, CoreService, SettingsService, type SettingsView } from "../lib/api";
import { advancedOrder, availability, fieldsOf, getValue, prepareForSave, primaryGroups, setValue, type Values } from "../lib/settingsForm";
import { useStore } from "../store";
import { Banner, Empty, Segmented, Spinner, Switch } from "../components/ui";
import { SettingRow } from "../components/SettingField";
import { IntegrationSettings } from "../components/IntegrationSettings";
import { UpdateSettings } from "../components/UpdateControl";

function PathSetting({ label, path, onBrowse, disabled }: { label: string; path?: string; onBrowse: () => void; disabled: boolean }) {
  return (
    <div className="setting">
      <div className="setting-label">{label}</div>
      <div className="setting-control">
        <span className="path ellipsis" title={path}>{path || "—"}</span>
        <button className="btn" onClick={onBrowse} disabled={disabled}><FolderOpen size={15} />浏览</button>
      </div>
    </div>
  );
}

function Setting({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="setting" title={hint}>
      <div className="setting-label">{label}</div>
      <div className="setting-control">{children}</div>
    </div>
  );
}

export function Settings() {
  const { status, anchor, setDirty, confirm, toast, fail, config, setConfig, overview, setOverview, navigate } = useStore();
  const [view, setView] = useState<SettingsView | null | undefined>(undefined);
  const [values, setValues] = useState<Values>({});
  const [baseline, setBaseline] = useState("");
  const [saving, setSaving] = useState(false);
  const [integrationDirty, setIntegrationDirty] = useState(false);
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const locked = status.running || status.starting || status.stopping;
  // Only real edits block navigation; an untouched draft stays in the backend until saved.
  const edited = !!view && JSON.stringify(values) !== baseline;
  const dirty = edited || !!view?.dirty;

  useEffect(() => setDirty("settings", edited || integrationDirty), [edited, integrationDirty, setDirty]);
  useEffect(() => () => setDirty("settings", false), [setDirty]);

  const show = useCallback((next: SettingsView | null) => {
    setView(next);
    setValues(next?.values ?? {});
    setBaseline(JSON.stringify(next?.values ?? {}));
  }, []);

  useEffect(() => {
    SettingsService.Current().then(show).catch(() => show(null));
  }, [show]);

  useEffect(() => {
    if (anchor && view) document.getElementById(anchor)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [anchor, view]);

  const change = (key: string, value: unknown) => setValues((v) => setValue(v, key, value));

  const save = async () => {
    if (!view) return;
    const prepared = prepareForSave(view.fields, values);
    if (prepared.error) {
      toast("error", prepared.error);
      return;
    }
    setSaving(true);
    try {
      show(await SettingsService.Save(prepared.values));
      toast("success", "设置已保存，下次连接时生效");
    } catch (e) {
      fail(e);
    } finally {
      setSaving(false);
    }
  };

  const discardThen = async (action: () => Promise<void>) => {
    if (edited && !(await confirm({ title: "放弃未保存的修改？", message: "当前修改尚未保存。", confirm: "放弃修改", danger: true }))) return;
    try {
      await action();
    } catch (e) {
      fail(e);
    }
  };
  const reload = () => discardThen(async () => show(await SettingsService.Reload()));
  const discard = async () => {
    try {
      // A draft (imported or adjusted) has no clean copy in memory; read the saved file again.
      if (view?.dirty) show(await SettingsService.Reload());
      else show(view ?? null);
    } catch (e) {
      fail(e);
    }
  };
  const importConfig = () => discardThen(async () => {
    const next = await SettingsService.Import();
    if (next) {
      show(next);
      toast("info", "已读入配置草稿，保存后生效");
    }
  });

  const savePrefs = async (patch: Partial<{ theme: string; autoStart: boolean; autoConnect: boolean; closeToTray: boolean }>) => {
    if (!config) return;
    const next = { ...config, ...patch };
    try {
      setConfig(await AppService.SavePreferences(next.theme, next.autoStart, next.autoConnect, next.closeToTray));
    } catch (e) {
      fail(e);
    }
  };

  const pickConfig = async () => {
    if (!overview) return;
    try {
      const picked = await AppService.PickConfig("选择 FDU 配置文件");
      if (!picked) return;
      setOverview(await ClashService.SetPaths("", picked));
    } catch (e) {
      fail(e);
    }
  };

  const runTool = async (action: string, label: string, risky: boolean) => {
    if (risky && !(await confirm({ title: label, message: "将修改 VPN 服务端的设备授信状态，继续？", confirm: "继续" }))) return;
    try {
      await CoreService.RunTool(action);
      toast("info", `已开始“${label}”，结果见诊断页`);
      navigate("logs");
    } catch (e) {
      fail(e);
    }
  };

  if (view === undefined) return <div className="page-body"><Empty icon={Layers} title="正在读取设置…" /></div>;

  const protocol = String(getValue(values, "protocol") ?? "");
  const fields = view?.fields ?? [];

  return (
    <>
      <header className="page-head">
        <h1 className="page-title" title={view?.path}>设置</h1>
      </header>
      <div className="page-body">
        <div className="stack">
          <IntegrationSettings onDirtyChange={setIntegrationDirty} />

          <UpdateSettings />

          <section className="card section" style={{ padding: 0 }}>
            <div className="section-head"><Palette size={16} />外观与启动</div>
            <Setting label="主题">
              <div style={{ width: 280 }}>
                <Segmented value={config?.theme ?? "system"} onChange={(theme) => savePrefs({ theme })}
                  options={[{ value: "system", label: "跟随系统" }, { value: "light", label: "浅色" }, { value: "dark", label: "深色" }]} />
              </div>
            </Setting>
            <Setting label="开机自启">
              <Switch checked={!!config?.autoStart} onChange={(autoStart) => savePrefs({ autoStart })} label="开机自启" />
            </Setting>
            <Setting label="启动后启用校内访问">
              <Switch checked={!!config?.autoConnect} onChange={(autoConnect) => savePrefs({ autoConnect })} label="启动后启用校内访问" />
            </Setting>
            <Setting label="关闭窗口时最小化到托盘">
              <Switch checked={!!config?.closeToTray} onChange={(closeToTray) => savePrefs({ closeToTray })} label="关闭到托盘" />
            </Setting>
          </section>

          <section className="card section" style={{ padding: 0 }}>
            <button className={`collapse-head${open.advanced ? " open" : ""}`} aria-expanded={!!open.advanced}
              onClick={() => setOpen({ ...open, advanced: !open.advanced })}>
              <Layers size={16} />高级设置
              <ChevronRight size={16} className="chev" />
            </button>
            {open.advanced && <>
              <div className="row" style={{ padding: "6px 20px 16px", justifyContent: "flex-end" }}>
                <button className="btn" onClick={importConfig} disabled={locked}><Upload size={15} />导入 VPN 配置</button>
                {view?.saved && <button className="btn icon-btn" onClick={reload} disabled={locked} aria-label="重新读取" title="重新读取"><RefreshCw size={15} /></button>}
              </div>
              {locked && <Banner tone="warning">校内访问已开启，关闭后才能修改连接配置</Banner>}
              {view?.dirty && <Banner tone="info">{view.saved ? "已读入配置草稿，保存后生效" : "启用校内访问时会自动保存默认配置，也可在这里调整"}</Banner>}
              {!view ? (
                <Empty icon={FileText} title="无法读取 VPN 配置">可导入现有的 FDU 配置</Empty>
              ) : <>
                <PathSetting label="VPN 配置文件" path={overview?.fduConfig} onBrowse={pickConfig} disabled={!overview || locked} />
                {[...primaryGroups.map(({ group }) => group), ...advancedOrder].map((group) => {
                  const groupFields = fieldsOf(fields, group).filter((field) => field.key !== "tun_mode" && field.key !== "routing.enabled");
                  if (!groupFields.length) return null;
                  const reason = availability(groupFields[0], values);
                  const expanded = !!open[group];
                  return (
                    <div key={group} className="collapse">
                      <button className={`collapse-head${expanded ? " open" : ""}${reason.enabled ? "" : " dim"}`} title={reason.enabled ? undefined : reason.reason}
                        aria-expanded={expanded} onClick={() => setOpen({ ...open, [group]: !expanded })}>
                        {primaryGroups.find((item) => item.group === group)?.title ?? group}
                        <ChevronRight size={16} className="chev" />
                      </button>
                      {expanded && groupFields.map((f) => <SettingRow key={f.key} field={f} values={values} locked={locked} onChange={change} />)}
                    </div>
                  );
                })}
                <div className="section-head"><Wrench size={16} />工具</div>
                {[["auth-info", "查看认证方式", false], ["trust-device", "授信此设备", true], ["untrust-device", "取消设备授信", true]].map(([action, label, risky]) => (
                  <Setting key={action as string} label={label as string} hint={protocol !== "atrust" ? "仅 aTrust 可用" : dirty || !view.saved ? "请先保存设置" : undefined}>
                    <button className="btn" disabled={locked || dirty || !view.saved || protocol !== "atrust"} onClick={() => runTool(action as string, label as string, risky as boolean)}>运行</button>
                  </Setting>
                ))}
              </>}
            </>}
          </section>
        </div>
      </div>
      {dirty && open.advanced && (
        <footer className="savebar">
          <span className="grow" style={{ color: "var(--warning)" }}>有未保存的修改</span>
          <button className="btn" onClick={discard} disabled={saving || locked || !view?.saved}>放弃</button>
          <button className="btn primary" onClick={save} disabled={saving || locked}>{saving && <Spinner />}保存</button>
        </footer>
      )}
    </>
  );
}
