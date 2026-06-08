/* ============================================================
   SCRUN — localStorage persistence (UI prefs + saved setup)
   ============================================================ */
import type { AppData } from "./state";
import type { SetupState } from "../types";

const PREFS_KEY = "scrun";
const SETUP_KEY = "scrun_setup";

export interface SavedPrefs {
  layout?: AppData["layout"];
  density?: AppData["density"];
  simRunning?: boolean;
  theme?: AppData["theme"];
  showRail?: boolean;
  accent?: string;
  railCollapsed?: boolean;
}

export function loadPrefs(): SavedPrefs {
  try {
    return JSON.parse(localStorage.getItem(PREFS_KEY) || "{}");
  } catch {
    return {};
  }
}

export function savePrefs(s: AppData): void {
  try {
    const p: SavedPrefs = {
      layout: s.layout,
      density: s.density,
      simRunning: s.simRunning,
      theme: s.theme,
      showRail: s.showRail,
      accent: s.accent,
      railCollapsed: s.railCollapsed,
    };
    localStorage.setItem(PREFS_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
}

export interface SavedSetup extends Partial<SetupState> {
  done: boolean;
}

export function loadSetup(): SavedSetup | null {
  try {
    return JSON.parse(localStorage.getItem(SETUP_KEY) || "null");
  } catch {
    return null;
  }
}

export function saveSetup(setup: SetupState): void {
  try {
    localStorage.setItem(
      SETUP_KEY,
      JSON.stringify({
        done: true,
        name: setup.name,
        key: setup.key,
        color: setup.color,
        desc: setup.desc,
        preset: setup.preset,
        stages: setup.stages,
        agents: setup.agents,
      }),
    );
  } catch {
    /* ignore */
  }
}
