import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { CodeGraph } from "@/components/CodeGraph";
import { getRepoMemory, getCallGraph, getScan, syncRepo } from "@/api/repos";
import type { Repo, RepoMemory, CallGraph, ScanResult } from "@/api/repos";

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

function ScanTab({ loading, scan }: { loading: boolean; scan: ScanResult | null }) {
  if (loading) return <div className="empty">Loading scan…</div>;
  if (!scan) {
    return (
      <div className="rd-empty">
        <h2 style={{ marginBottom: 8 }}>No scan results</h2>
        <p>No security scan has completed for this repo yet.<br />Click <b>Sync</b> above to run one.</p>
      </div>
    );
  }
  const cves = scan.cves ?? [];
  const bottlenecks = scan.bottlenecks ?? [];
  const suggestions = scan.suggestions ?? [];
  const counts: Record<string, number> = { critical: 0, high: 0, medium: 0, low: 0 };
  for (const c of cves) {
    const s = (c.severity || "low").toLowerCase();
    if (s in counts) counts[s]++;
  }
  const risk = (scan.risk_level || "info").toLowerCase();

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div className="rd-sev">
        {(["critical", "high", "medium", "low"] as const).map((k) => (
          <div key={k} className={`rd-sev-card ${k}`}><div className="n">{counts[k]}</div><div className="l">{k} CVE</div></div>
        ))}
      </div>

      <div className="rd-card">
        <h3>Overall risk</h3>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: scan.summary ? 10 : 0 }}>
          <span className={`rd-badge risk-${risk}`} style={{ fontSize: 13 }}>{risk}</span>
          <span style={{ fontSize: 12, color: "var(--fg-muted)" }}>
            {cves.length} CVE{cves.length === 1 ? "" : "s"} · {bottlenecks.length} bottleneck{bottlenecks.length === 1 ? "" : "s"} · {suggestions.length} suggestion{suggestions.length === 1 ? "" : "s"}
            {scan.scanned_at ? ` · ${new Date(scan.scanned_at).toLocaleString()}` : ""}
          </span>
        </div>
        {scan.summary && <p>{scan.summary}</p>}
      </div>

      {cves.length > 0 && (
        <div className="rd-card">
          <h3>Vulnerabilities</h3>
          <div className="rd-findings">
            {cves.map((c, i) => (
              <div key={i} className="rd-finding">
                <span className={`sev sev-${(c.severity || "low").toLowerCase()}`}>{c.severity}</span>
                <div className="rd-finding-body">
                  <div className="rd-finding-top">
                    <b>{c.package}</b>{c.version ? <span className="ver"> {c.version}</span> : null}
                    {c.cve_ids?.length ? <span className="cve-ids">{c.cve_ids.join(", ")}</span> : null}
                  </div>
                  <div className="rd-finding-desc">{c.description}</div>
                  {c.fix && <div className="rd-finding-fix">→ {c.fix}{c.fixed_versions?.length ? ` (fixed in ${c.fixed_versions.join(", ")})` : ""}</div>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {bottlenecks.length > 0 && (
        <div className="rd-card">
          <h3>Performance bottlenecks</h3>
          <div className="rd-findings">
            {bottlenecks.map((b, i) => (
              <div key={i} className="rd-finding">
                <span className={`sev sev-${(b.severity || "low").toLowerCase()}`}>{b.severity}</span>
                <div className="rd-finding-body">
                  <div className="rd-finding-top"><b style={{ fontFamily: "var(--mono)", fontSize: 12 }}>{b.location}</b></div>
                  <div className="rd-finding-desc">{b.description}</div>
                  {b.fix && <div className="rd-finding-fix">→ {b.fix}</div>}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {suggestions.length > 0 && (
        <div className="rd-card">
          <h3>Architecture suggestions</h3>
          <div className="rd-findings">
            {suggestions.map((s, i) => (
              <div key={i} className="rd-finding">
                <span className={`sev sev-${(s.priority || "low").toLowerCase()}`}>{s.priority}</span>
                <div className="rd-finding-body">
                  <div className="rd-finding-top"><b>{s.area}</b></div>
                  <div className="rd-finding-desc">{s.suggestion}</div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {cves.length === 0 && bottlenecks.length === 0 && suggestions.length === 0 && (
        <div className="rd-empty">No findings — this scan came back clean.</div>
      )}
    </div>
  );
}
