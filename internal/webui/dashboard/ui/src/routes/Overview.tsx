import { useQueries, useQuery } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { listBoards, getBoard } from "@/api/kanban";
import { listRepos } from "@/api/repos";
import { whoami } from "@/api/auth";
import { api } from "@/api/client";
import type { Board, Card } from "@/api/types";
import "./overview.css";

interface AgentTask {
  id: string;
  status: string;
  task_preview?: string;
  updated_at?: string;
  created_at?: string;
}

async function listAgentTasks(): Promise<AgentTask[]> {
  const res = await api<{ tasks?: AgentTask[] }>("/api/v1/agent-tasks").catch(() => ({ tasks: [] }));
  return res.tasks ?? [];
}

// Card status → colour, shared by pipeline bar, board progress and the run list.
const STATUS_COLOR: Record<Card["status"], string> = {
  done: "#10b981",
  running: "var(--accent)",
  awaiting: "#f4bc2e",
  queued: "var(--border-strong)",
  blocked: "#f04438",
};

const RISK_COLOR: Record<string, string> = {
  critical: "#b42318",
  high: "#f04438",
  medium: "#f4bc2e",
  low: "#10b981",
  none: "var(--border-strong)",
};

const DAYS = ["S", "M", "T", "W", "T", "F", "S"];

function greeting(): string {
  const h = new Date().getHours();
  if (h < 5) return "Working late";
  if (h < 12) return "Good morning";
  if (h < 17) return "Good afternoon";
  return "Good evening";
}

export function Overview() {
  const meQ = useQuery({ queryKey: ["whoami"], queryFn: whoami, staleTime: 60_000 });
  const boardsQ = useQuery({ queryKey: ["boards"], queryFn: listBoards, refetchInterval: 5000 });
  const reposQ = useQuery({ queryKey: ["repos"], queryFn: listRepos, refetchInterval: 5000 });
  const tasksQ = useQuery({ queryKey: ["agent-tasks"], queryFn: listAgentTasks, refetchInterval: 3000 });

  // Fan out to each board's detail so we can aggregate real card stats.
  const boardIds = (boardsQ.data ?? []).map((b) => b.id);
  const detailQs = useQueries({
    queries: boardIds.map((id) => ({
      queryKey: ["board", id],
      queryFn: () => getBoard(id),
      refetchInterval: 8000,
    })),
  });
  const boards = detailQs.map((q) => q.data).filter(Boolean) as Board[];
  const allCards = boards.flatMap((b) => b.cards);

  // ---- aggregates ----
  const statusCounts: Record<Card["status"], number> = {
    queued: 0, running: 0, awaiting: 0, done: 0, blocked: 0,
  };
  for (const c of allCards) statusCounts[c.status]++;
  const totalCards = allCards.length;
  const activeRuns = statusCounts.running + statusCounts.awaiting;

  const repos = reposQ.data ?? [];
  const indexed = repos.filter((r) => {
    const s = (r.index_status ?? "").toLowerCase();
    return s === "ready" || s === "indexed";
  }).length;
  const indexPct = repos.length ? Math.round((indexed / repos.length) * 100) : 0;

  const riskCounts: Record<string, number> = { critical: 0, high: 0, medium: 0, low: 0, none: 0 };
  for (const r of repos) {
    const k = (r.risk_level ?? "none").toLowerCase();
    riskCounts[k in riskCounts ? k : "none"]++;
  }
  const atRisk = riskCounts.critical + riskCounts.high + riskCounts.medium;

  // weekly activity: bucket cards by weekday of last update
  const week = [0, 0, 0, 0, 0, 0, 0];
  for (const c of allCards) {
    if (!c.when) continue;
    const d = new Date(c.when);
    if (!Number.isNaN(d.getTime())) week[d.getDay()]++;
  }
  const weekMax = Math.max(1, ...week);
  const today = new Date().getDay();

  // active task feed (running/awaiting first, then most recent)
  const tasks = (tasksQ.data ?? []).slice().sort((a, b) => {
    const rank = (s: string) => (s === "running" ? 0 : s === "awaiting" ? 1 : 2);
    return rank(a.status?.toLowerCase() ?? "") - rank(b.status?.toLowerCase() ?? "");
  });

  const name = meQ.data?.display_name || meQ.data?.username || "there";
  const role = meQ.data?.roles?.[0] || meQ.data?.type || "operator";

  // per-board completion for the boards mini grid
  const boardStats = boards.map((b) => {
    const done = b.cards.filter((c) => c.status === "done").length;
    return { id: b.id, name: b.name, total: b.cards.length, done, active: b.cards.filter((c) => c.status === "running" || c.status === "awaiting").length };
  });

  return (
    <>
      <Topbar title="Overview" sub="Operational summary" />
      <div className="view">
        <div className="ov">
          {/* ---- hero ---- */}
          <div className="ov-hero">
            <div>
              <h1 className="ov-hello">{greeting()}, <em>{name}</em></h1>
              <div className="ov-hello-sub">
                Here's what your autonomous workforce is doing across {boards.length || boardIds.length} board{boardIds.length === 1 ? "" : "s"} and {repos.length} repositor{repos.length === 1 ? "y" : "ies"}.
              </div>
            </div>
            <div className="ov-stats">
              <Stat num={activeRuns} label="Active runs" ico="↯" />
              <Stat num={boardIds.length} label="Boards" ico="▣" />
              <Stat num={repos.length} label="Repos" ico="{}" />
            </div>
          </div>

          {/* ---- pipeline segmented bar ---- */}
          <PipelineBar counts={statusCounts} total={totalCards} />

          {/* ---- bento grid ---- */}
          <div className="ov-grid">
            {/* spotlight / workspace */}
            <div className="ov-card accent span-4 ov-spot">
              <div className="who">
                <div className="ava">{(name[0] || "?").toUpperCase()}</div>
                <div>
                  <div className="name">{name}</div>
                  <div className="role">{role}</div>
                </div>
              </div>
              <span className="ov-chip">● On-premise · zero egress</span>
              <div className="blurb">
                Your data never leaves this server. Answers, runs and repo intelligence
                are scoped to your access level.
              </div>
              <div className="cta">
                <a className="btn primary" href="/dashboard/kanban" target="_blank" rel="noopener">Open boards</a>
                <a className="btn" href="#/chat">Ask a question</a>
              </div>
            </div>

            {/* weekly activity */}
            <div className="ov-card span-5">
              <div className="ov-card-head">
                <span className="ov-card-title">Activity</span>
                <span className="ov-card-meta">{totalCards} cards · last 7 days</span>
              </div>
              <div className="ov-bars">
                {week.map((v, i) => (
                  <div key={i} className={`col${i === today ? " hot" : v > 0 ? " has" : ""}`}>
                    <div className="bar" style={{ height: `${(v / weekMax) * 100}%` }} />
                    <span className="day">{DAYS[i]}</span>
                  </div>
                ))}
              </div>
            </div>

            {/* index coverage ring */}
            <div className="ov-card span-3">
              <div className="ov-card-head">
                <span className="ov-card-title">Index coverage</span>
              </div>
              <Ring pct={indexPct} caption={`${indexed} of ${repos.length} repos indexed`} />
            </div>

            {/* risk posture */}
            <div className="ov-card span-4">
              <div className="ov-card-head">
                <span className="ov-card-title">Risk posture</span>
                <span className="ov-card-meta">{atRisk} at risk</span>
              </div>
              {repos.length === 0 ? (
                <div className="ov-empty">No repositories connected yet.</div>
              ) : (
                <div className="ov-rows">
                  {(["critical", "high", "medium", "low"] as const).map((lvl) => (
                    <MeterRow
                      key={lvl}
                      label={lvl}
                      color={RISK_COLOR[lvl]}
                      value={riskCounts[lvl]}
                      total={repos.length}
                    />
                  ))}
                </div>
              )}
            </div>

            {/* active runs — dark contrast card */}
            <div className="ov-card dark span-4">
              <div className="ov-card-head">
                <span className="ov-card-title">Active runs</span>
                <span className="ov-card-meta">{activeRuns} live · {tasks.length} total</span>
              </div>
              {tasks.length === 0 ? (
                <div className="ov-empty" style={{ color: "#b8b0a2" }}>No agent runs yet. Dispatch a card to begin.</div>
              ) : (
                <div className="ov-list">
                  {tasks.slice(0, 5).map((t) => (
                    <RunItem key={t.id} task={t} />
                  ))}
                </div>
              )}
            </div>

            {/* boards mini grid */}
            <div className="ov-card span-4">
              <div className="ov-card-head">
                <span className="ov-card-title">Boards</span>
                <a className="ov-card-meta" href="/dashboard/kanban" target="_blank" rel="noopener" style={{ color: "var(--accent)" }}>View all →</a>
              </div>
              {boardStats.length === 0 ? (
                <div className="ov-empty">No boards yet.</div>
              ) : (
                <div className="ov-boards">
                  {boardStats.slice(0, 4).map((b) => (
                    <a key={b.id} className="ov-board" href="/dashboard/kanban" target="_blank" rel="noopener">
                      <div className="bn">{b.name}</div>
                      <div className="bstat"><span><b>{b.done}</b>/{b.total} done</span><span><b>{b.active}</b> active</span></div>
                      <div className="bbar"><i style={{ width: `${b.total ? (b.done / b.total) * 100 : 0}%` }} /></div>
                    </a>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

function Stat({ num, label, ico }: { num: number; label: string; ico: string }) {
  return (
    <div className="ov-stat">
      <div className="num">{num}</div>
      <div className="lbl"><span className="ico">{ico}</span>{label}</div>
    </div>
  );
}

function PipelineBar({ counts, total }: { counts: Record<Card["status"], number>; total: number }) {
  const order: Card["status"][] = ["running", "awaiting", "queued", "done", "blocked"];
  const labels: Record<Card["status"], string> = {
    running: "Running", awaiting: "Awaiting", queued: "Queued", done: "Done", blocked: "Blocked",
  };
  return (
    <div className="ov-pipeline">
      <div className="track">
        {total === 0 ? null : order.map((s) => {
          const w = (counts[s] / total) * 100;
          if (w === 0) return null;
          return <span key={s} className="seg" style={{ width: `${w}%`, background: STATUS_COLOR[s] }} />;
        })}
      </div>
      <div className="legend">
        {order.map((s) => (
          <span key={s} className="leg">
            <span className="dot" style={{ background: STATUS_COLOR[s] }} />
            {labels[s]} <b>{counts[s]}</b>
          </span>
        ))}
      </div>
    </div>
  );
}

function Ring({ pct, caption }: { pct: number; caption: string }) {
  const r = 50;
  const circ = 2 * Math.PI * r;
  const dash = (pct / 100) * circ;
  return (
    <div className="ov-ring">
      <svg width="132" height="132" viewBox="0 0 132 132">
        <circle cx="66" cy="66" r={r} fill="none" stroke="var(--bg-elev-2)" strokeWidth="11" />
        <circle
          cx="66" cy="66" r={r} fill="none"
          stroke="var(--accent)" strokeWidth="11" strokeLinecap="round"
          strokeDasharray={`${dash} ${circ}`}
          transform="rotate(-90 66 66)"
          style={{ transition: "stroke-dasharray 0.6s cubic-bezier(0.22,1,0.36,1)" }}
        />
        <text x="66" y="72" textAnchor="middle" className="center" fill="var(--fg)" style={{ font: "600 26px var(--sans)" }}>{pct}%</text>
      </svg>
      <div className="cap">{caption}</div>
    </div>
  );
}

function MeterRow({ label, color, value, total }: { label: string; color: string; value: number; total: number }) {
  const pct = total ? (value / total) * 100 : 0;
  return (
    <div className="ov-row">
      <div className="top">
        <span className="k"><span className="dot" style={{ width: 9, height: 9, borderRadius: 3, background: color, display: "inline-block" }} />{label}</span>
        <span className="v">{value}</span>
      </div>
      <div className="meter"><div className="fill" style={{ width: `${pct}%`, background: color }} /></div>
    </div>
  );
}

function RunItem({ task }: { task: AgentTask }) {
  const s = (task.status ?? "").toLowerCase();
  const kind = s === "running" ? "run" : s === "awaiting" ? "wait" : s === "completed" || s === "done" ? "done" : s === "failed" ? "fail" : "";
  const glyph = kind === "done" ? "✓" : kind === "fail" ? "✕" : kind === "run" ? "●" : kind === "wait" ? "◔" : "·";
  const when = task.updated_at || task.created_at;
  return (
    <div className="ov-item">
      <span className={`tick ${kind}`}>{glyph}</span>
      <div className="body">
        <div className="t1">{task.task_preview || "Agent run"}</div>
        <div className="t2">{task.id.slice(0, 8)} · {task.status}</div>
      </div>
      {when && <span className="when">{relTime(when)}</span>}
    </div>
  );
}

function relTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return "now";
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  return `${Math.floor(diff / 86400)}d`;
}
