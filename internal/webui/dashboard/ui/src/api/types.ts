// Types mirror /api/v1/boards/* and related endpoints. Kept narrow — fields not
// consumed by the UI are intentionally omitted; cast at the boundary if needed.

export type CardPriority = "H" | "M" | "L";
export type CardStatus = "queued" | "running" | "awaiting" | "done" | "blocked";

export interface BoardSummary {
  id: string;
  name: string;
}

export interface Column {
  id: string;
  name: string;
  dot?: string;
  wip?: number;
  gate?: string;
  rules?: string[];
}

export interface Agent {
  id: string;
  name: string;
  agent_type?: string;
  provider_id?: string;
}

export interface Card {
  id: string;
  col: string;
  type?: string;
  prio: CardPriority;
  title: string;
  desc?: string;
  agents: string[];
  status: CardStatus;
  labels?: string[];
  ac?: string[];
  progress?: number;
  branch?: string;
  add?: number;
  del?: number;
  cost?: number;
  tokens?: number;
  duration?: number;
  eta?: string;
  tests?: { pass: number; fail: number };
  when?: string;
  logs?: string[];
  hitl?: { runId: string; decisionId: string; prompt: string } | null;
  order?: number;
}

export interface Board {
  id: string;
  name: string;
  columns: Column[];
  cards: Card[];
  agents: Agent[];
}

export interface Principal {
  user_id: string;
  username: string;
  display_name?: string;
  type: string;
  roles?: string[];
}

