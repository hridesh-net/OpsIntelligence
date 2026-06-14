import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Topbar } from "@/chrome/Topbar";
import { listRepos, syncRepo, type Repo } from "@/api/repos";
import { RepoDetail } from "./RepoDetail";
import "./repos.css";

const PILL_BG: Record<string, { bg: string; fg: string }> = {
  ready: { bg: "rgba(15,118,110,0.10)", fg: "var(--ok)" },
  indexed: { bg: "rgba(15,118,110,0.10)", fg: "var(--ok)" },
  running: { bg: "var(--accent-soft)", fg: "var(--accent)" },
  pending: { bg: "var(--bg-elev-2)", fg: "var(--fg-muted)" },
  failed: { bg: "rgba(180,35,24,0.12)", fg: "var(--danger)" },
};

function Pill({ value }: { value?: string }) {
  const v = (value ?? "pending").toLowerCase();
  const style = PILL_BG[v] ?? PILL_BG.pending;
  return (
    <span style={{
      background: style.bg,
      color: style.fg,
      padding: "2px 8px",
      borderRadius: 4,
      fontSize: 11,
      fontFamily: "var(--mono)",
      textTransform: "lowercase",
    }}>{value || "pending"}</span>
  );
}

export function Repos() {
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Repo | null>(null);

  const q = useQuery({
    queryKey: ["repos"],
    queryFn: listRepos,
    refetchInterval: 3000,
  });

  const sync = useMutation({
    mutationFn: (id: string) => syncRepo(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["repos"] }),
    onError: (err: Error) => setError(err.message),
  });

  // All hooks above this line — the detail view is a full-screen replacement.
  if (selected) {
    return <RepoDetail repo={selected} onBack={() => setSelected(null)} />;
  }

  return (
    <>
      <Topbar
        title="Repos"
        sub="Indexed repositories"
        actions={
          <button className="btn" onClick={() => q.refetch()}>Refresh</button>
        }
      />
      <div className="view">
        {error && (
          <div style={{
            background: "rgba(180,35,24,0.08)",
            color: "var(--danger)",
            border: "1px solid rgba(180,35,24,0.2)",
            borderRadius: "var(--radius-sm)",
            padding: "8px 12px",
            marginBottom: 16,
            fontSize: 13,
          }}>{error}</div>
        )}

        {q.isLoading && <div className="empty">Loading repos…</div>}
        {q.error && <div className="empty" style={{ color: "var(--danger)" }}>Failed: {(q.error as Error).message}</div>}

        {q.data && q.data.length === 0 && (
          <div style={{
            background: "var(--bg-elev)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius)",
            padding: 32,
            textAlign: "center",
          }}>
            <h2 style={{ marginBottom: 8 }}>No repos configured</h2>
            <p style={{ color: "var(--fg-muted)" }}>
              Add one via CLI: <code>opsintelligence repos add owner/name --platform github</code>
            </p>
          </div>
        )}

        {q.data && q.data.length > 0 && (
          <div style={{
            background: "var(--bg-elev)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius)",
            overflow: "hidden",
            boxShadow: "var(--shadow-card)",
          }}>
            <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
              <thead>
                <tr style={{ background: "var(--bg-elev-1)", borderBottom: "1px solid var(--border)" }}>
                  <th style={th}>Repo</th>
                  <th style={th}>Platform</th>
                  <th style={th}>Index</th>
                  <th style={th}>Scan</th>
                  <th style={th}>Risk</th>
                  <th style={th}>Users</th>
                  <th style={{ ...th, textAlign: "right" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {q.data.map((r: Repo) => (
                  <tr key={r.id} className="repo-row" onClick={() => setSelected(r)}>
                    <td style={td}>
                      <div className="rn">
                        <div>
                          <div style={{ fontWeight: 500 }}>{r.full_name || r.id}</div>
                          <div style={{ fontSize: 11, color: "var(--fg-muted)", fontFamily: "var(--mono)" }}>{r.id}</div>
                        </div>
                      </div>
                    </td>
                    <td style={td}>{r.platform || "—"}</td>
                    <td style={td}><Pill value={r.index_status} /></td>
                    <td style={td}><Pill value={r.scan_status} /></td>
                    <td style={td}>{r.risk_level || "—"}</td>
                    <td style={td}>{r.user_count ?? 0}</td>
                    <td style={{ ...td, textAlign: "right" }}>
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 10 }}>
                        <button
                          className="btn ghost"
                          disabled={sync.isPending}
                          onClick={(e) => { e.stopPropagation(); sync.mutate(r.id); }}
                        >
                          {sync.isPending && sync.variables === r.id ? "…" : "Sync"}
                        </button>
                        <span className="chev">›</span>
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}

const th: React.CSSProperties = {
  textAlign: "left",
  padding: "10px 14px",
  fontWeight: 600,
  fontSize: 11,
  textTransform: "uppercase",
  letterSpacing: "0.06em",
  color: "var(--fg-muted)",
};
const td: React.CSSProperties = {
  padding: "12px 14px",
  verticalAlign: "top",
};
