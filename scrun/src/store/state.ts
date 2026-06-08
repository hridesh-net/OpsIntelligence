/* ============================================================
   SCRUN — Mutable application data shape (no actions)
   Shared by the Zustand store and the pure logic mutators.
   ============================================================ */
import type {
  ActivityItem,
  Agent,
  AgentKey,
  AgentStat,
  Card,
  Density,
  Filters,
  Layout,
  PanelTab,
  Screen,
  SetupState,
  Stage,
  Theme,
} from "../types";

export interface AppData {
  /* domain */
  agents: Record<AgentKey, Agent>;
  workflow: Stage[];
  cards: Card[];
  agentStats: Record<AgentKey, AgentStat>;
  activity: ActivityItem[];
  seq: number;
  prefix: string;

  /* board identity */
  boardId: string | null;
  boardName: string;
  boardKey: string;
  boardColor: string;
  boardDesc: string;
  boardAgents: AgentKey[];

  /* data source */
  apiMode: "loading" | "live" | "demo";

  /* ui */
  phase: "setup" | "app";
  screen: Screen;
  layout: Layout;
  density: Density;
  theme: Theme;
  accent: string;
  simSpeed: number;
  simRunning: boolean;
  dragging: boolean;
  showRail: boolean;
  railCollapsed: boolean;
  filters: Filters;
  selectedId: string | null;
  panelTab: PanelTab;
  tickN: number;
  flashId: string | null;

  /* transient ui */
  toast: { msg: string; n: number };

  /* editing drafts */
  taskDraft: Card | null;
  taskIsNew: boolean;
  agentDraftKey: AgentKey | null;
  agentDraft: Agent | null;

  /* setup wizard */
  setup: SetupState;
}
