/* ============================================================
   SCRUN — Shared domain types
   ============================================================ */
export type AgentKey = string;

export type Autonomy = "supervised" | "auto" | "full";
export type MemoryMode = "persistent" | "session" | "none";
export type MemoryScope = "project" | "global" | "task";

export interface Memory {
  mode: MemoryMode;
  scope: MemoryScope;
  contextK: number;
  retention: string;
}

export interface Agent {
  name: string;
  color: string;
  ini: string;
  model: string;
  provider: string;
  caps: string[];
  /* configurable profile (merged in from AGENT_CFG) */
  role: string;
  instructions: string;
  knowledge: [string, string][];
  memory: Memory;
  autonomy: Autonomy;
  spendCap: number;
  maxParallel: number;
}

export type StageGate = "human" | "auto" | null;

export interface StageRules {
  autoAssign: "auto" | "keep" | "review" | null;
  autoValidate: boolean;
}

export interface Stage {
  id: string;
  name: string;
  dot: string;
  wip: number;
  gate: StageGate;
  rules: StageRules;
}

export type LogKind = "" | "ok" | "wr" | "ac";
export interface LogLine {
  t: string;
  k: LogKind;
  x: string;
}

export interface Hitl {
  q: string;
  opts: string[];
}

export type Status = "queued" | "running" | "awaiting" | "blocked" | "done";

export interface Card {
  id: string;
  col: string;
  type: string;
  prio: "H" | "M" | "L";
  title: string;
  agents: AgentKey[];
  status: Status;
  labels: string[];
  desc?: string;
  ac?: string[];
  when: string;
  logs: LogLine[];
  progress?: number;
  branch?: string;
  add?: number;
  del?: number;
  cost?: number;
  tokens?: number;
  duration?: string;
  eta?: string;
  conf?: number;
  tests?: string;
  hitl?: Hitl;
}

export interface AgentStat {
  tasks: number;
  success: number;
  spend: number;
}

export interface ActivityItem {
  id: string;
  agent: AgentKey;
  tag: string;
  text: string;
  meta: string;
  time: string;
}

export type Layout = "columns" | "compact" | "lanes";
export type Density = "rich" | "balanced" | "lean";
export type Theme = "light" | "dark";
export type Screen = "board" | "workflows" | "agents" | "activity" | "analytics";
export type PanelTab =
  | "details"
  | "conversation"
  | "artifacts"
  | "timeline"
  | "metrics";

export interface Filters {
  agent: string;
  prio: string;
  type: string;
  q: string;
}

/* setup wizard model */
export interface SetupState {
  step: number;
  name: string;
  key: string;
  desc: string;
  color: string;
  preset: string;
  /* [id, name, dot, gate?] tuples; null = use preset default */
  stages: [string, string, string, StageGate?][] | null;
  agents: AgentKey[];
  done: boolean;
}
