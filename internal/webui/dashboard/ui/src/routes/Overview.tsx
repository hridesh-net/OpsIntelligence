import { useQueries } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { listBoards } from "@/api/kanban";
import { listRepos } from "@/api/repos";
import { api } from "@/api/client";

interface AgentTask {
  id: string;
  status: string;
}

async function listAgentTasks(): Promise<AgentTask[]> {
  const res = await api<{ tasks?: AgentTask[] }>("/api/v1/agent-tasks").catch(() => ({ tasks: [] }));
  return res.tasks ?? [];
}

export function Overview() {
  const [boardsQ, reposQ, tasksQ] = useQueries({
    queries: [
      { queryKey: ["boards"], queryFn: listBoards, refetchInterval: 5000 },
      { queryKey: ["repos"], queryFn: listRepos, refetchInterval: 5000 },
      { queryKey: ["agent-tasks"], queryFn: listAgentTasks, refetchInterval: 3000 },
    ],
  });

  const boardCount = boardsQ.data?.length ?? null;
  const repoCount = reposQ.data?.length ?? null;
  const indexedRepos = reposQ.data?.filter((r) => {
    const s = (r.index_status ?? "").toLowerCase();
    return s === "ready" || s === "indexed";
  }).length ?? null;
  const activeTasks = tasksQ.data?.filter((t) => {
    const s = (t.status ?? "").toLowerCase();
    return s === "running" || s === "awaiting";
  }).length ?? null;

  return (
    <>
      <Topbar title="Overview" sub="Operational summary" />
      <div className="view">
        <div style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
          gap: 12,
        }}>
          <Kpi label="Active runs" value={activeTasks} hint={tasksQ.isLoading ? "…" : `${tasksQ.data?.length ?? 0} total`} />
          <Kpi label="Boards" value={boardCount} hint={boardsQ.error ? (boardsQ.error as Error).message : undefined} hintError={!!boardsQ.error} />
          <Kpi label="Repos indexed" value={indexedRepos} hint={repoCount != null ? `${repoCount} total` : undefined} />
          <Kpi label="Repos with risk" value={reposQ.data?.filter((r) => r.risk_level && r.risk_level !== "low" && r.risk_level !== "none").length ?? null} hint="medium / high / critical" />
        </div>

        {boardsQ.data && boardsQ.data.length > 0 && (
          <Section title="Boards">
            <div style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
              gap: 10,
            }}>
              {boardsQ.data.slice(0, 8).map((b) => (
                <a key={b.id} href="/dashboard/kanban" target="_blank" rel="noopener" style={cardLinkStyle}>
                  <div style={{ fontWeight: 500 }}>{b.name}</div>
                  <div style={{ fontFamily: "var(--mono)", fontSize: 11, color: "var(--fg-muted)" }}>{b.id}</div>
                </a>
              ))}
            </div>
          </Section>
        )}

        {tasksQ.data && tasksQ.data.length > 0 && (
          <Section title="Recent agent tasks">
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {tasksQ.data.slice(0, 6).map((t) => (
                <div key={t.id} style={taskRowStyle}>
                  <span style={{ fontFamily: "var(--mono)", fontSize: 11, color: "var(--fg-muted)", minWidth: 80 }}>{t.id.slice(0, 8)}</span>
                  <span style={{ flex: 1 }}>{(t as AgentTask & { task_preview?: string }).task_preview ?? "(no preview)"}</span>
                  <Pill value={t.status} />
                </div>
              ))}
            </div>
          </Section>
        )}
      </div>
    </>
  );
}

function Kpi({ label, value, hint, hintError }: { label: string; value: number | null; hint?: string; hintError?: boolean }) {
  return (
    <div style={{
      background: "var(--bg-elev)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius)",
      padding: 16,
      boxShadow: "var(--shadow-card)",
    }}>
      <div style={{ fontSize: 11, color: "var(--fg-muted)", textTransform: "uppercase", letterSpacing: "0.06em" }}>{label}</div>
      <div style={{ fontSize: 28, fontWeight: 600, marginTop: 6, fontFamily: "var(--mono)" }}>
        {value == null ? "—" : value}
      </div>
      {hint && (
        <div style={{ fontSize: 11, color: hintError ? "var(--danger)" : "var(--fg-muted)", marginTop: 4 }}>{hint}</div>
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ marginTop: 24 }}>
      <h2 style={{ fontSize: 14, marginBottom: 10, color: "var(--fg-dim)" }}>{title}</h2>
      {children}
    </div>
  );
}

function Pill({ value }: { value: string }) {
  const v = value.toLowerCase();
  const map: Record<string, { bg: string; fg: string }> = {
    running: { bg: "rgba(15,118,110,0.10)", fg: "var(--ok)" },
    awaiting: { bg: "rgba(180,83,9,0.12)", fg: "var(--warn)" },
    completed: { bg: "rgba(15,118,110,0.10)", fg: "var(--ok)" },
    failed: { bg: "rgba(180,35,24,0.12)", fg: "var(--danger)" },
  };
  const s = map[v] ?? { bg: "var(--bg-elev-2)", fg: "var(--fg-muted)" };
  return (
    <span style={{
      background: s.bg,
      color: s.fg,
      padding: "2px 8px",
      borderRadius: 4,
      fontSize: 11,
      fontFamily: "var(--mono)",
    }}>{value}</span>
  );
}

const cardLinkStyle: React.CSSProperties = {
  display: "block",
  background: "var(--bg-elev)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  padding: "10px 12px",
  color: "var(--fg)",
  textDecoration: "none",
};

const taskRowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 10,
  background: "var(--bg-elev)",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  padding: "8px 12px",
  fontSize: 13,
};
