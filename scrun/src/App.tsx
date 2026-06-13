import { useEffect } from "react";
import { useStore } from "./store";
import { applyAccentVars, applyThemeAttr, setFavicon } from "./lib/theme";
import { logoInner } from "./lib/logoSvg";

import NavRail from "./components/shell/NavRail";
import TopBar from "./components/shell/TopBar";
import StatsStrip from "./components/shell/StatsStrip";
import Board from "./components/board/Board";
import Workflows from "./components/screens/Workflows";
import Agents from "./components/screens/Agents";
import ActivityScreen from "./components/screens/Activity";
import Analytics from "./components/screens/Analytics";
import TaskDetailModal from "./components/modals/TaskDetailModal";
import TaskFormModal from "./components/modals/TaskFormModal";
import AgentConfigModal from "./components/modals/AgentConfigModal";
import SetupWizard from "./components/setup/SetupWizard";
import BoardsHome from "./components/screens/BoardsHome";
import ErrorBoundary from "./components/ErrorBoundary";
import Tweaks from "./components/tweaks/Tweaks";
import Toast from "./components/Toast";

import shell from "./components/shell/Shell.module.css";

export default function App() {
  const phase = useStore((s) => s.phase);
  const theme = useStore((s) => s.theme);
  const accent = useStore((s) => s.accent);
  const screen = useStore((s) => s.screen);
  const simRunning = useStore((s) => s.simRunning);
  const simSpeed = useStore((s) => s.simSpeed);
  const selectedId = useStore((s) => s.selectedId);
  const hasTaskDraft = useStore((s) => s.taskDraft != null);
  const hasAgentDraft = useStore((s) => s.agentDraft != null);

  /* theme + favicon */
  useEffect(() => applyThemeAttr(theme), [theme]);
  useEffect(() => setFavicon(logoInner("#ffffff")), []);

  /* hydrate from live /api/v1/boards on first mount */
  useEffect(() => {
    useStore.getState().hydrateFromApi();
  }, []);

  /* accent — only drive it from app state once launched (the wizard owns it during setup) */
  useEffect(() => {
    if (phase !== "setup") applyAccentVars(accent);
  }, [accent, phase]);

  /* live simulation loop */
  useEffect(() => {
    if (phase !== "app" || !simRunning) return;
    const tick = useStore.getState().tick;
    const id = setInterval(tick, simSpeed);
    return () => clearInterval(id);
  }, [phase, simRunning, simSpeed]);

  /* global shortcuts */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const st = useStore.getState();
      if (e.key === "Escape") {
        st.closePanel();
        st.closeTaskForm();
        st.closeAgentConfig();
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        document.getElementById("scrun-search")?.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  if (phase === "setup") {
    return (
      <>
        <SetupWizard />
        <Toast />
      </>
    );
  }

  if (phase === "boards") {
    return (
      <>
        <BoardsHome />
        <Toast />
      </>
    );
  }

  return (
    <div className={shell.app}>
      <div className={shell.dots} />
      <NavRail />
      <div className={shell.main}>
        <TopBar />
        <StatsStrip />
        <div className={shell.screenWrap}>
          <section className={shell.screen}>
            <ErrorBoundary onReset={() => useStore.getState().goToBoards()}>
              {screen === "board" && <Board />}
              {screen === "workflows" && <Workflows />}
              {screen === "agents" && <Agents />}
              {screen === "activity" && <ActivityScreen />}
              {screen === "analytics" && <Analytics />}
            </ErrorBoundary>
          </section>
        </div>
      </div>

      {selectedId && <TaskDetailModal />}
      {hasTaskDraft && <TaskFormModal />}
      {hasAgentDraft && <AgentConfigModal />}

      <Toast />
      <Tweaks />
    </div>
  );
}
