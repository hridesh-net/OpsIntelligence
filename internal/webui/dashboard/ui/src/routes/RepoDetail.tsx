import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { CodeGraph } from "@/components/CodeGraph";
import { getRepoMemory, getCallGraph, getScan, syncRepo } from "@/api/repos";
import type { Repo, RepoMemory, CallGraph, ScanStatus } from "@/api/repos";

type Tab = "intel" | "graph" | "scan";

export function RepoDetail({ repo, onBack }: { repo: Repo; onBack: () => void }) {
  const qc = useQueryClient();
  const [tab, setTab] = useState<Tab>("intel");

  const memQ = useQuery({ queryKey: ["repo-memory", repo.id], queryFn: () => getRepoMemory(repo.id) });
  const graphQ = useQuery({ queryKey: ["repo-callgraph", repo.id], queryFn: () => getCallGraph(repo.id), enabled: tab === "graph" });
  const scanQ = useQuery({ queryKey: ["repo-scan", repo.id], queryFn: () => getScan(repo.id), enabled: tab === "scan" });

  const sync = useMutation({
    mutationFn: () => syncRepo(repo.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["repo-memory", repo.id] });
      qc.invalidateQueries({ queryKey: ["repo-callgraph", repo.id] });
    },
  });

  const name = repo.full_name || repo.id;
  const risk = (repo.risk_level || "none").toLowerCase();

  return (
    <>
      <Topbar
        title="Repo intelligence"
        sub={name}
        actions={<button className="btn primary" disabled={sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? "Syncing…" : "Sync"}</button>}
      />
      <div className="view">
        <div className="rd-head">
          <button className="rd-back" onClick={onBack}>← Repos</button>
          <div>
            <div className="rd-title">{name}</div>
            <div className="rd-id">{repo.id}</div>
          </div>
          <div className="rd-badges">
            {repo.platform && <span className="rd-badge">{repo.platform}</span>}
            {repo.index_status && <span className="rd-badge">index: {repo.index_status}</span>}
            <span className={`rd-badge risk-${risk}`}>risk: {risk}</span>
          </div>
        </div>

        <div className="rd-tabs">
          <button className={`rd-tab${tab === "intel" ? " active" : ""}`} onClick={() => setTab("intel")}>Intelligence</button>
          <button className={`rd-tab${tab === "graph" ? " active" : ""}`} onClick={() => setTab("graph")}>Code graph</button>
          <button className={`rd-tab${tab === "scan" ? " active" : ""}`} onClick={() => setTab("scan")}>Security scan</button>
        </div>

        {tab === "intel" && <IntelTab loading={memQ.isLoading} mem={memQ.data ?? null} />}
        {tab === "graph" && <GraphTab loading={graphQ.isLoading} graph={graphQ.data ?? null} />}
        {tab === "scan" && <ScanTab loading={scanQ.isLoading} scan={scanQ.data ?? null} />}
      </div>
    </>
  );
}

function IntelTab({ loading, mem }: { loading: boolean; mem: RepoMemory | null }) {
  if (loading) return <div className="empty">Loading intelligence…</div>;
  if (!mem || (!mem.architecture && !mem.languages?.length && !mem.dependencies?.length)) {
    return (
      <div className="rd-empty">
        <h2 style={{ marginBottom: 8 }}>No intelligence yet</h2>
        <p>This repo hasn't been indexed, or memory extraction hasn't run.<br />Click <b>Sync</b> above to index and extract its intelligence.</p>
      </div>
    );
  }
  const langs = mem.languages?.length ? mem.languages : (mem.primary_lang ? [mem.primary_lang] : []);
  return (
    <div className="rd-grid">
      {mem.architecture && (
        <div className="rd-card wide"><h3>Architecture</h3><p>{mem.architecture}</p></div>
      )}
      {langs.length > 0 && (
        <div className="rd-card"><h3>Languages</h3><div className="rd-chips">{langs.map((l) => <span key={l} className="rd-chip">{l}</span>)}</div></div>
      )}
      {mem.key_files?.length ? (
        <div className="rd-card"><h3>Key files</h3><div className="rd-list">{mem.key_files.map((f) => <span key={f} className="rd-li bullet" style={{ fontFamily: "var(--mono)", fontSize: 12 }}>{f}</span>)}</div></div>
      ) : null}
      {mem.dependencies?.length ? (
        <div className="rd-card"><h3>Key dependencies</h3><div className="rd-list">
          {mem.dependencies.slice(0, 14).map((d) => (
            <span key={d.name} className="rd-li"><b>{d.name}</b>{d.version ? <span className="ver"> {d.version}</span> : null}{d.purpose ? ` — ${d.purpose}` : ""}</span>
          ))}
        </div></div>
      ) : null}
      {mem.conventions?.length ? (
        <div className="rd-card"><h3>Conventions</h3><div className="rd-list">
          {mem.conventions.map((c) => <span key={c.name} className="rd-li"><b>{c.name}</b> — {c.pattern}</span>)}
        </div></div>
      ) : null}
      {mem.common_issues?.length ? (
        <div className="rd-card"><h3>Common issues</h3><div className="rd-list">{mem.common_issues.map((c, i) => <span key={i} className="rd-li bullet">{c}</span>)}</div></div>
      ) : null}
      {mem.ci_summary && <div className="rd-card"><h3>CI / CD</h3><p>{mem.ci_summary}</p></div>}
      {mem.test_patterns && <div className="rd-card"><h3>Test patterns</h3><p>{mem.test_patterns}</p></div>}
      {mem.review_hints && <div className="rd-card wide"><h3>Review focus</h3><p>{mem.review_hints}</p></div>}
    </div>
  );
}

function GraphTab({ loading, graph }: { loading: boolean; graph: CallGraph | null }) {
  if (loading) return <div className="empty">Loading code graph…</div>;
  if (!graph || graph.nodes.length === 0) {
    return (
      <div className="rd-empty">
        <h2 style={{ marginBottom: 8 }}>No code graph yet</h2>
        <p>The call graph is built during indexing.<br />Click <b>Sync</b> above, then reopen this tab once indexing completes.</p>
      </div>
    );
  }
  return <CodeGraph graph={graph} />;
}

function ScanTab({ loading, scan }: { loading: boolean; scan: ScanStatus | null }) {
  if (loading) return <div className="empty">Loading scan…</div>;
  if (!scan) {
    return (
      <div className="rd-empty">
        <h2 style={{ marginBottom: 8 }}>No scan results</h2>
        <p>No security scan has completed for this repo yet.</p>
      </div>
    );
  }
  const sev = [
    { k: "critical", n: scan.critical ?? 0 },
    { k: "high", n: scan.high ?? 0 },
    { k: "medium", n: scan.medium ?? 0 },
    { k: "low", n: scan.low ?? 0 },
  ];
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div className="rd-sev">
        {sev.map((s) => (
          <div key={s.k} className={`rd-sev-card ${s.k}`}><div className="n">{s.n}</div><div className="l">{s.k}</div></div>
        ))}
      </div>
      <div className="rd-card">
        <h3>Scan summary</h3>
        <div className="rd-list">
          <span className="rd-li"><b>Status:</b> {scan.status || "—"}</span>
          <span className="rd-li"><b>Total findings:</b> {scan.total_findings ?? 0}</span>
          {scan.last_scan_at && <span className="rd-li"><b>Last scan:</b> {new Date(scan.last_scan_at).toLocaleString()}</span>}
        </div>
      </div>
    </div>
  );
}
