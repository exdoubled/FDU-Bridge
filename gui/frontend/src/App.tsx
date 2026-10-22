import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { AppService, CoreService } from "./lib/api";
import { StoreProvider, useStore } from "./store";
import { Sidebar } from "./components/Sidebar";
import { ConfirmDialog, Toasts } from "./components/Overlays";
import { ChallengeDialog, type Challenge } from "./components/ChallengeDialog";
import { Home } from "./pages/Home";
import { Nodes } from "./pages/Nodes";
import { Rules } from "./pages/Rules";
import { Logs } from "./pages/Logs";
import { Settings } from "./pages/Settings";

const pages = { home: Home, nodes: Nodes, rules: Rules, logs: Logs, settings: Settings };

function useTheme(theme: string | undefined) {
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark = theme === "dark" || ((!theme || theme === "system") && media.matches);
      document.documentElement.dataset.theme = dark ? "dark" : "light";
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [theme]);
}

function Shell() {
  const { page, config, status, dirty, confirm, fail } = useStore();
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const latest = useRef({ status, dirty });
  latest.current = { status, dirty };
  useTheme(config?.theme);

  useEffect(() => {
    CoreService.PendingChallenge().then((c) => c && Object.keys(c).length && setChallenge(c)).catch(() => undefined);
    return Events.On("core:challenge", (event) => setChallenge(event.data));
  }, []);

  // Only reached when "close to tray" is off; the backend already cancelled the close.
  useEffect(() => Events.On("app:close-requested", async () => {
    const { status: s, dirty: d } = latest.current;
    if (Object.values(d).some(Boolean) && !(await confirm({ title: "退出并丢弃修改？", message: "有未保存的修改，退出后会丢失。", confirm: "退出", danger: true }))) return;
    if ((s.running || s.starting) && !(await confirm({ title: "关闭校内访问并退出？", message: "退出前会先关闭校内访问。", confirm: "关闭并退出" }))) return;
    try {
      if (!(await AppService.QuitApp(false)) && (await confirm({ title: "断开超时", message: "FDU 尚未退出，强制结束并退出？", confirm: "强制退出", danger: true }))) {
        await AppService.QuitApp(true);
      }
    } catch (e) {
      fail(e);
    }
  }), [confirm, fail]);

  const closeChallenge = useCallback(() => setChallenge(null), []);
  const Page = pages[page];
  return (
    <div className="shell">
      <Sidebar />
      <main className="main">
        <Page />
      </main>
      <ConfirmDialog />
      <Toasts />
      {challenge && <ChallengeDialog key={String(challenge.id)} challenge={challenge} onClose={closeChallenge} />}
    </div>
  );
}

export function App() {
  return (
    <StoreProvider>
      <Shell />
    </StoreProvider>
  );
}
