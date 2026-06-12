/* ============================================================
   SCRUN — Live /api/v1/boards client
   TypeScript port of the legacy assets/scrun/api.js, talking to
   the same handlers in internal/gateway/kanban_api.go.
   ============================================================ */
import type {
  Agent,
  AgentKey,
  AgentStat,
  Card,
  Stage,
  StageGate,
  Status,
} from "../types";

const BASE = "/api/v1";

/* ---------- HTTP helpers --------------------------------------------- */

function csrfHeaders(): Record<string, string> {
  const match = document.cookie.match(/(?:^|; )opi_csrf=([^;]*)/);
  const tok = match ? decodeURIComponent(match[1]) : "";
  return tok ? { "X-CSRF-Token": tok } : {};
}

async function jget<T>(path: string): Promise<T> {
  const r = await fetch(BASE + path, { credentials: "same-origin" });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText} on ${path}`);
  return r.json() as Promise<T>;
}

async function jsend<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(BASE + path, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...csrfHeaders() },
    body: body == null ? undefined : JSON.stringify(body),
  });
  if (!r.ok) {
    let msg = `${r.status} ${r.statusText} on ${path}`;
    try {
      const j = await r.json();
      if (j && j.error) msg = j.error;
    } catch { /* ignore */ }
    throw new Error(msg);
  }
  // Some DELETEs return 204
  if (r.status === 204) return undefined as unknown as T;
  return r.json() as Promise<T>;
}

/* ---------- Raw backend shapes --------------------------------------- */

export interface BoardSummary {
  id: string;
  name: string;
  key?: string;
  color?: string;
  description?: string;
  config?: BoardConfig;
}

export interface BoardConfig {
  column_overrides?: Record<string, ColumnOverride>;
  /* wizard metadata — the gateway stores these inside board.config */
  key?: string;
  color?: string;
  description?: string;
}

/** Board display metadata; top-level fields win, config-stored wizard values
    fall back (the gateway persists key/color/description inside config). */
export function boardMeta(b: BoardSummary | undefined): { key: string; color: string; desc: string } {
  return {
    key: b?.key || b?.config?.key || "AI",
    color: b?.color || b?.config?.color || "#e4572e",
    desc: b?.description || b?.config?.description || "",
  };
}

export interface ColumnOverride {
  gate?: string;
  automation?: { autoAssign: Stage["rules"]["autoAssign"]; autoValidate: boolean };
}

export interface BoardColumn {
  id: string;
  name: string;
  color?: string;
  wip_limit?: number;
  gate?: string;
  position?: number;
}

export interface BoardCard {
  id: string;
  column_id: string;
  card_type?: string;
  priority?: string;
  title: string;
  description?: string;
  assignee?: string;
  status?: string;
  branch?: string;
  cost_usd?: number;
  token_in?: number;
  token_out?: number;
  created_at?: string;
  updated_at?: string;
  metadata?: {
    labels?: string[];
    acceptance_criteria?: string[];
    progress?: number;
    branch?: string;
    add?: number;
    del?: number;
    duration?: string;
    eta?: string;
    confidence?: number;
    tests?: string;
    logs?: Card["logs"];
    hitl?: Card["hitl"];
  };
}

export interface BoardAgent {
  id: string;
  name: string;
  agent_type?: string;
  provider_id?: string;
  config?: {
    color?: string;
    ini?: string;
    model?: string;
    provider?: string;
    capabilities?: string[];
    caps?: string[];
    role?: string;
    instructions?: string;
    system_prompt?: string;
    knowledge?: [string, string][];
    memory?: Agent["memory"];
    autonomy?: Agent["autonomy"];
    spend_cap_daily?: number;
    spendCap?: number;
    max_parallel?: number;
    maxParallel?: number;
  };
}

export interface BoardDetail {
  board: BoardSummary;
  columns: BoardColumn[];
  cards: BoardCard[];
}

/* ---------- Mapping helpers ----------------------------------------- */

const AGENT_COLORS = [
  "#2898da", "#f4685f", "#2dd4bf", "#a78bfa",
  "#34d399", "#f5b042", "#60a5fa", "#fb7185",
];
const colorFor = (idx: number) => AGENT_COLORS[idx % AGENT_COLORS.length];

function ini(name: string): string {
  if (!name) return "AG";
  const parts = name.trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}

function shortType(t?: string): string {
  if (!t) return "feat";
  const lower = t.toLowerCase();
  if (lower === "feature") return "feat";
  if (lower === "bug") return "fix";
  if (lower === "infrastructure") return "infra";
  if (lower === "security") return "sec";
  return lower;
}

function shortPrio(p?: string): "H" | "M" | "L" {
  if (!p) return "M";
  if (p === "p0" || p === "p1") return "H";
  if (p === "p3") return "L";
  return "M";
}

function mapStatus(s?: string): Status {
  if (!s) return "queued";
  if (s === "completed") return "done";
  if (s === "queued" || s === "running" || s === "awaiting" || s === "blocked" || s === "done") {
    return s as Status;
  }
  return "queued";
}

function relTime(iso?: string): string {
  if (!iso) return "—";
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

function mapAgent(a: BoardAgent, idx: number): Agent {
  const cfg = a.config || {};
  return {
    name: a.name,
    color: cfg.color || colorFor(idx),
    ini: cfg.ini || ini(a.name),
    model: cfg.model || a.agent_type || "—",
    provider: cfg.provider || a.provider_id || "",
    caps: cfg.capabilities || cfg.caps || [],
    role: cfg.role || "",
    instructions: cfg.instructions || cfg.system_prompt || "",
    knowledge: cfg.knowledge || [],
    memory: cfg.memory || { mode: "session", scope: "project", contextK: 64, retention: "7d" },
    autonomy: cfg.autonomy || "supervised",
    spendCap: cfg.spend_cap_daily ?? cfg.spendCap ?? 10,
    maxParallel: cfg.max_parallel ?? cfg.maxParallel ?? 2,
  };
}

function mapColumn(col: BoardColumn, override: ColumnOverride | undefined): Stage {
  const ov = override || {};
  let gate: StageGate | string | null = ov.gate || col.gate || null;
  if (gate === "none" || gate === "") gate = null;
  if (gate === "auto-validate") gate = "auto";
  return {
    id: col.id,
    name: col.name,
    dot: col.color || "#586675",
    wip: col.wip_limit || 0,
    gate: gate as StageGate,
    rules: ov.automation || { autoAssign: null, autoValidate: gate === "auto" },
  };
}

function mapCard(c: BoardCard): Card {
  const md = c.metadata || {};
  return {
    id: c.id,
    col: c.column_id,
    type: shortType(c.card_type),
    prio: shortPrio(c.priority),
    title: c.title,
    desc: c.description || "",
    agents: c.assignee ? [c.assignee] : [],
    status: mapStatus(c.status),
    labels: md.labels || [],
    ac: md.acceptance_criteria || [],
    progress: md.progress || 0,
    branch: c.branch || md.branch || "",
    add: md.add || 0,
    del: md.del || 0,
    cost: c.cost_usd || 0,
    tokens: (c.token_in || 0) + (c.token_out || 0),
    duration: md.duration || "",
    eta: md.eta || "",
    conf: md.confidence || 0,
    tests: md.tests || "",
    when: relTime(c.updated_at || c.created_at),
    logs: md.logs || [],
    hitl: md.hitl || undefined,
  };
}

/* ---------- High-level endpoints ------------------------------------ */

export function listBoards(): Promise<BoardSummary[]> {
  return jget<{ boards?: BoardSummary[] }>("/boards").then((j) => j.boards || []);
}

export function getBoard(id: string): Promise<BoardDetail> {
  return jget<BoardDetail>(`/boards/${encodeURIComponent(id)}`);
}

export function listAgents(boardID: string): Promise<BoardAgent[]> {
  return jget<{ agents?: BoardAgent[] }>(`/boards/${encodeURIComponent(boardID)}/agents`).then(
    (j) => j.agents || [],
  );
}

export function createBoard(payload: Record<string, unknown>): Promise<BoardSummary> {
  return jsend("POST", `/boards`, payload);
}

export function moveCard(boardID: string, cardID: string, columnID: string): Promise<unknown> {
  return jsend(
    "POST",
    `/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}/move`,
    { column_id: columnID },
  );
}

export function createCard(boardID: string, card: Record<string, unknown>): Promise<unknown> {
  return jsend("POST", `/boards/${encodeURIComponent(boardID)}/cards`, card);
}

export function updateCard(boardID: string, cardID: string, patch: Record<string, unknown>): Promise<unknown> {
  return jsend(
    "PUT",
    `/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}`,
    patch,
  );
}

export function deleteCard(boardID: string, cardID: string): Promise<unknown> {
  return jsend(
    "DELETE",
    `/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}`,
  );
}

export function updateAgent(boardID: string, agentID: string, patch: Record<string, unknown>): Promise<unknown> {
  return jsend(
    "PUT",
    `/boards/${encodeURIComponent(boardID)}/agents/${encodeURIComponent(agentID)}`,
    patch,
  );
}

export function saveWorkflow(boardID: string, payload: Record<string, unknown>): Promise<unknown> {
  return jsend("PUT", `/boards/${encodeURIComponent(boardID)}/workflow`, payload);
}

/* ---------- Analytics ------------------------------------------------ */

export interface BoardAnalytics {
  kpis: {
    tasks_shipped: number;
    shipped_last7: number;
    avg_cycle_hours: number;
    success_rate: number;
    spend_today_usd: number;
    spend_total_usd: number;
  };
  status_counts: Record<string, number>;
  throughput: Array<{ date: string; day: string; shipped: number; started: number }>;
  spend_trend: Array<{ date: string; usd: number }>;
  stage_hours: Array<{ column_id: string; name: string; avg_hours: number; cards: number }>;
  leaderboard: Array<{
    agent_id: string;
    name: string;
    agent_type: string;
    model?: string;
    tasks: number;
    success_pct: number;
    spend_usd: number;
    active: number;
  }>;
}

export function getBoardAnalytics(boardID: string): Promise<BoardAnalytics> {
  return jget<BoardAnalytics>(`/boards/${encodeURIComponent(boardID)}/analytics`);
}

/* ---------- SSE run stream ------------------------------------------ */

export interface RunStreamOpts {
  onEvent?: (ev: unknown) => void;
  onLifecycle?: (ev: unknown) => void;
  onError?: (e: Event) => void;
}

export function streamRunEvents(runID: string, opts: RunStreamOpts): { close: () => void } {
  const url = `${BASE}/runs/${encodeURIComponent(runID)}/events`;
  let src: EventSource;
  try {
    src = new EventSource(url, { withCredentials: true });
  } catch (e) {
    opts.onError?.(e as Event);
    return { close: () => {} };
  }
  const parse = (raw: string): unknown => {
    try { return JSON.parse(raw); } catch { return null; }
  };
  src.addEventListener("event", (e) => {
    const ev = parse((e as MessageEvent).data);
    if (ev && opts.onEvent) opts.onEvent(ev);
  });
  src.addEventListener("lifecycle", (e) => {
    const ev = parse((e as MessageEvent).data);
    if (ev && opts.onLifecycle) opts.onLifecycle(ev);
  });
  src.onerror = (e) => { opts.onError?.(e); };
  return { close: () => { try { src.close(); } catch { /* ignore */ } } };
}

/* ---------- Top-level loader ---------------------------------------- */

export interface HydratedBoard {
  boardId: string;
  boardName: string;
  boardKey: string;
  boardColor: string;
  boardDesc: string;
  agents: Record<AgentKey, Agent>;
  workflow: Stage[];
  cards: Card[];
  agentStats: Record<AgentKey, AgentStat>;
}

export type LoadResult =
  | { ok: true; data: HydratedBoard }
  | { ok: false; reason: "no-boards" | "error"; error?: unknown };

/** Fetch one board's full detail (columns, cards, agents) by id. */
export async function loadBoard(boardID: string): Promise<LoadResult> {
  try {
    const [detail, agents] = await Promise.all([
      getBoard(boardID),
      listAgents(boardID).catch(() => [] as BoardAgent[]),
    ]);

    const overrides = detail.board?.config?.column_overrides || {};
    const workflow = (detail.columns || []).map((c) => mapColumn(c, overrides[c.id]));
    const cards = (detail.cards || []).map(mapCard);

    const agentsObj: Record<AgentKey, Agent> = {};
    const agentStats: Record<AgentKey, AgentStat> = {};
    agents.forEach((a, idx) => {
      agentsObj[a.id] = mapAgent(a, idx);
      agentStats[a.id] = { tasks: 0, success: 0, spend: 0 };
    });

    const meta = boardMeta(detail.board);
    return {
      ok: true,
      data: {
        boardId: boardID,
        boardName: detail.board?.name || "Board",
        boardKey: meta.key,
        boardColor: meta.color,
        boardDesc: meta.desc,
        agents: agentsObj,
        workflow,
        cards,
        agentStats,
      },
    };
  } catch (error) {
    console.warn("[scrun] API load failed:", error);
    return { ok: false, reason: "error", error };
  }
}

export async function loadFirstBoard(): Promise<LoadResult> {
  try {
    const boards = await listBoards();
    if (!boards.length) return { ok: false, reason: "no-boards" };

    let pick = boards[0];
    const saved = localStorage.getItem("scrun.lastBoard");
    if (saved) {
      const hit = boards.find((b) => b.id === saved);
      if (hit) pick = hit;
    }
    localStorage.setItem("scrun.lastBoard", pick.id);
    return loadBoard(pick.id);
  } catch (error) {
    console.warn("[scrun] API load failed:", error);
    return { ok: false, reason: "error", error };
  }
}

export function rememberBoard(boardID: string): void {
  try { localStorage.setItem("scrun.lastBoard", boardID); } catch { /* ignore */ }
}

export function activeBoardId(): string | null {
  try { return localStorage.getItem("scrun.lastBoard"); } catch { return null; }
}
