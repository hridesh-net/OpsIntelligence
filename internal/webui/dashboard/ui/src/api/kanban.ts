// Kanban API client. Mirrors the handlers in internal/gateway/kanban_api.go.
//
// Important shape notes (verified against gateway/kanban_api.go and
// datastore/types.go on v1.0.68):
//   - Move is POST /boards/{bid}/cards/{cid}/move with body { column_id }.
//   - Card priority is "p0".."p3"; status is queued|running|awaiting|completed|failed|stopped.
//   - Card.column_id is the column key; description, card_type live under those names.

import { api } from "./client";
import type { Agent, Board, BoardSummary, Card, Column } from "./types";

interface BoardListResp { boards: { id: string; name: string }[] }
interface BoardDetailResp {
  board: { id: string; name: string };
  columns: RawColumn[];
  cards: RawCard[];
}

interface RawColumn {
  id: string;
  name: string;
  position: number;
  color?: string;
  wip_limit?: number | null;
}

interface RawCard {
  id: string;
  column_id: string;
  title: string;
  description?: string;
  card_type?: string;
  priority?: string;          // p0..p3
  status?: string;            // queued | running | awaiting | completed | failed | stopped
  assignee?: string;
  branch?: string;
  cost_usd?: number;
  token_in?: number;
  token_out?: number;
  metadata?: Record<string, unknown>;
  updated_at?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
}

interface AgentsResp { agents: Agent[] }

const PRIO: Record<string, Card["prio"]> = {
  p0: "H", p1: "H", p2: "M", p3: "L",
  high: "H", med: "M", medium: "M", low: "L",
};

const STATUS: Record<string, Card["status"]> = {
  queued: "queued",
  running: "running",
  awaiting: "awaiting",
  completed: "done",
  done: "done",
  failed: "blocked",
  stopped: "blocked",
};

function mapCard(raw: RawCard): Card {
  const meta = raw.metadata ?? {};
  const labels = Array.isArray(meta.labels) ? (meta.labels as string[]) : [];
  const ac = Array.isArray(meta.acceptance_criteria) ? (meta.acceptance_criteria as string[]) : [];
  const order = typeof meta.order === "number" ? (meta.order as number) : undefined;
  const when = raw.updated_at ?? raw.created_at;
  return {
    id: raw.id,
    col: raw.column_id,
    title: raw.title,
    desc: raw.description ?? "",
    prio: PRIO[(raw.priority ?? "p2").toLowerCase()] ?? "M",
    type: raw.card_type,
    status: STATUS[(raw.status ?? "queued").toLowerCase()] ?? "queued",
    agents: raw.assignee ? [raw.assignee] : [],
    labels,
    ac,
    branch: raw.branch,
    cost: raw.cost_usd,
    tokens: (raw.token_in ?? 0) + (raw.token_out ?? 0),
    when,
    order,
  };
}

function mapColumn(raw: RawColumn): Column {
  return {
    id: raw.id,
    name: raw.name,
    dot: raw.color,
    wip: raw.wip_limit ?? undefined,
  };
}

export async function listBoards(): Promise<BoardSummary[]> {
  const res = await api<BoardListResp>("/api/v1/boards");
  return res.boards ?? [];
}

export async function getBoard(boardId: string): Promise<Board> {
  const res = await api<BoardDetailResp>(`/api/v1/boards/${boardId}`);
  const agentsResp = await api<AgentsResp>(`/api/v1/boards/${boardId}/agents`).catch(() => ({ agents: [] }));
  const columns = (res.columns ?? [])
    .map(mapColumn)
    .sort((a, b) => {
      const ap = (res.columns.find((c) => c.id === a.id)?.position) ?? 0;
      const bp = (res.columns.find((c) => c.id === b.id)?.position) ?? 0;
      return ap - bp;
    });
  return {
    id: res.board.id,
    name: res.board.name,
    columns,
    cards: (res.cards ?? []).map(mapCard),
    agents: agentsResp.agents ?? [],
  };
}

export interface CreateBoardInput {
  name: string;
  mode?: "local" | "github";
  repo_url?: string;
  repo_path?: string;
  preset?: string;
}

export async function createBoard(input: CreateBoardInput): Promise<{ id: string; name: string }> {
  return api(`/api/v1/boards`, { method: "POST", body: input });
}

export async function moveCard(boardId: string, cardId: string, columnId: string): Promise<void> {
  await api(`/api/v1/boards/${boardId}/cards/${cardId}/move`, {
    method: "POST",
    body: { column_id: columnId },
  });
}

export interface CreateCardInput {
  title: string;
  description?: string;
  column_id: string;
  card_type?: string;
  priority?: string;
}

export async function createCard(boardId: string, input: CreateCardInput): Promise<Card> {
  const raw = await api<RawCard>(`/api/v1/boards/${boardId}/cards`, { method: "POST", body: input });
  return mapCard(raw);
}

export async function dispatchCard(boardId: string, cardId: string, agentId?: string): Promise<void> {
  await api(`/api/v1/boards/${boardId}/cards/${cardId}/dispatch`, {
    method: "POST",
    body: agentId ? { agent_id: agentId } : {},
  });
}
