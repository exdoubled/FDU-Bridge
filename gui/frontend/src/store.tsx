import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Events } from "@wailsio/runtime";
import { AppService, ClashService, CoreService, errorText, type AppConfig, type ClashOverview, type CoreStatus } from "./lib/api";

export type Page = "home" | "nodes" | "rules" | "logs" | "settings";
export type ToastKind = "success" | "error" | "info";

export interface Toast {
  id: number;
  kind: ToastKind;
  text: string;
}

export interface ConfirmOptions {
  title: string;
  message: string;
  confirm?: string;
  cancel?: string;
  danger?: boolean;
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

const idleStatus: CoreStatus = {
  state: "idle", running: false, starting: false, enabled: false, connected: false, external: false, stopping: false, tool: "",
  server: "", protocol: "", socks: "", http: "", connectedAt: 0, admin: false, saved: false,
  coverage: "", problem: "", authRequired: false,
};

interface Store {
  page: Page;
  anchor: string;
  navigate: (page: Page, anchor?: string) => Promise<boolean>;
  status: CoreStatus;
  overview: ClashOverview | null;
  inspect: () => Promise<ClashOverview | null>;
  setOverview: (o: ClashOverview) => void;
  config: AppConfig | null;
  setConfig: (c: AppConfig) => void;
  versions: Record<string, number>;
  dirty: Partial<Record<Page, boolean>>;
  setDirty: (page: Page, dirty: boolean) => void;
  toasts: Toast[];
  toast: (kind: ToastKind, text: string) => void;
  fail: (error: unknown) => void;
  pending: PendingConfirm | null;
  confirm: (options: ConfirmOptions) => Promise<boolean>;
  settle: (ok: boolean) => void;
}

const Context = createContext<Store | null>(null);

export function useStore(): Store {
  const store = useContext(Context);
  if (!store) throw new Error("store missing");
  return store;
}

export function StoreProvider({ children }: { children: ReactNode }) {
  const [page, setPage] = useState<Page>("home");
  const [anchor, setAnchor] = useState("");
  const [status, setStatus] = useState<CoreStatus>(idleStatus);
  const [overview, setOverview] = useState<ClashOverview | null>(null);
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [versions, setVersions] = useState<Record<string, number>>({});
  const [dirty, setDirtyState] = useState<Partial<Record<Page, boolean>>>({});
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [pending, setPending] = useState<PendingConfirm | null>(null);
  const nextToast = useRef(1);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;

  const toast = useCallback((kind: ToastKind, text: string) => {
    const id = nextToast.current++;
    setToasts((list) => [...list.slice(-3), { id, kind, text }]);
    setTimeout(() => setToasts((list) => list.filter((t) => t.id !== id)), kind === "error" ? 8000 : 3500);
  }, []);
  const fail = useCallback((error: unknown) => toast("error", errorText(error)), [toast]);

  const confirm = useCallback((options: ConfirmOptions) => new Promise<boolean>((resolve) => setPending({ ...options, resolve })), []);
  const settle = useCallback((ok: boolean) => {
    setPending((current) => {
      current?.resolve(ok);
      return null;
    });
  }, []);

  const setDirty = useCallback((p: Page, value: boolean) => {
    setDirtyState((d) => (Boolean(d[p]) === value ? d : { ...d, [p]: value }));
  }, []);

  const navigate = useCallback(async (target: Page, nextAnchor = "") => {
    if (target !== page && dirtyRef.current[page]) {
      const ok = await confirm({ title: "放弃未保存的修改？", message: "离开此页会丢弃尚未保存的修改。", confirm: "放弃修改", danger: true });
      if (!ok) return false;
      setDirty(page, false);
    }
    setAnchor(nextAnchor);
    setPage(target);
    return true;
  }, [page, confirm, setDirty]);

  const inspect = useCallback(async () => {
    try {
      const o = await ClashService.Inspect();
      setOverview(o);
      return o;
    } catch {
      return null;
    }
  }, []);

  useEffect(() => {
    CoreService.Status().then(setStatus).catch(() => undefined);
    AppService.Config().then(setConfig).catch(() => undefined);
    inspect();
    const offStatus = Events.On("core:status", (event) => setStatus(event.data));
    const offChanged = Events.On("app:changed", (event) => {
      const what = event.data;
      setVersions((v) => ({ ...v, [what]: (v[what] ?? 0) + 1 }));
      if (what === "mode" || what === "clash") ClashService.Cached().then(setOverview).catch(() => undefined);
      if (what === "settings") inspect();
    });
    return () => {
      offStatus();
      offChanged();
    };
  }, [inspect]);

  const value = useMemo<Store>(() => ({
    page, anchor, navigate, status, overview, inspect, setOverview, config, setConfig, versions,
    dirty, setDirty, toasts, toast, fail, pending, confirm, settle,
  }), [page, anchor, navigate, status, overview, inspect, config, versions, dirty, setDirty, toasts, toast, fail, pending, confirm, settle]);

  return <Context.Provider value={value}>{children}</Context.Provider>;
}
