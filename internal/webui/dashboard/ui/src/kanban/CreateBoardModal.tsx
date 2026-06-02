// Scrun-style board setup wizard. Multi-step:
//   1. Identify  — name, mode, repo URL
//   2. Workflow  — pick a preset; editable column list
//   3. Agents    — optional, register one default agent
//   4. Review    — confirm, fire POST /api/v1/boards
//
// All steps map cleanly to the createBoardRequest in gateway/kanban_api.go.

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";

interface CreateBoardModalProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (boardId: string) => void;
}

interface WizardColumn {
  name: string;
  position: number;
  color?: string;
  wip_limit?: number;
  gate?: "" | "human" | "auto-validate";
}

interface Preset {
  id: string;
  label: string;
  sub: string;
  columns: WizardColumn[];
}

const PRESETS: Preset[] = [
  {
    id: "default",
    label: "Default",
    sub: "Inbox → Backlog → Todo → In Progress → Review → Done",
    columns: [
      { name: "Inbox",       position: 0 },
      { name: "Backlog",     position: 1 },
      { name: "Todo",        position: 2 },
      { name: "In Progress", position: 3 },
      { name: "Review",      position: 4, gate: "human" },
      { name: "Done",        position: 5 },
    ],
  },
  {
    id: "dev",
    label: "Development",
    sub: "Standard agent dev flow with auto-validate gate before Review",
    columns: [
      { name: "Backlog",     position: 0 },
      { name: "Todo",        position: 1 },
      { name: "In Progress", position: 2 },
      { name: "Validate",    position: 3, gate: "auto-validate" },
      { name: "Review",      position: 4, gate: "human" },
      { name: "Done",        position: 5 },
    ],
  },
  {
    id: "research",
    label: "Research",
    sub: "Lighter pipeline tuned for spikes and discovery",
    columns: [
      { name: "Ideas",       position: 0 },
      { name: "Exploring",   position: 1 },
      { name: "Synthesizing", position: 2 },
      { name: "Shared",      position: 3 },
    ],
  },
  {
    id: "support",
    label: "Support",
    sub: "Triage queue for inbound bugs / Sentry imports",
    columns: [
      { name: "Inbox",       position: 0 },
      { name: "Triage",      position: 1 },
      { name: "In Progress", position: 2 },
      { name: "Waiting",     position: 3 },
      { name: "Resolved",    position: 4 },
    ],
  },
  {
    id: "ops",
    label: "Operations",
    sub: "Change-management flow with a human gate on rollout",
    columns: [
      { name: "Proposed",    position: 0 },
      { name: "Approved",    position: 1, gate: "human" },
      { name: "Rolling Out", position: 2 },
      { name: "Done",        position: 3 },
    ],
  },
];

type Step = 0 | 1 | 2 | 3;
const STEPS = ["Identify", "Workflow", "Agents", "Review"];

interface AgentSpec {
  name: string;
  agent_type: string;
  provider_id?: string;
  is_default?: boolean;
}

export function CreateBoardModal({ open, onClose, onCreated }: CreateBoardModalProps) {
  const qc = useQueryClient();

  const [step, setStep] = useState<Step>(0);

  // step 1
  const [name, setName] = useState("");
  const [mode, setMode] = useState<"local" | "github">("local");
  const [repoUrl, setRepoUrl] = useState("");
  const [repoPath, setRepoPath] = useState("");

  // step 2
  const [presetId, setPresetId] = useState("default");
  const [columns, setColumns] = useState<WizardColumn[]>(PRESETS[0].columns);

  // step 3
  const [skipAgents, setSkipAgents] = useState(true);
  const [agent, setAgent] = useState<AgentSpec>({
    name: "Default agent",
    agent_type: "claude-code",
    is_default: true,
  });

  // common
  const [error, setError] = useState<string | null>(null);

  function pickPreset(id: string) {
    setPresetId(id);
    const p = PRESETS.find((p) => p.id === id);
    if (p) setColumns(p.columns.map((c) => ({ ...c })));
  }

  function updateColumn(i: number, patch: Partial<WizardColumn>) {
    setColumns((cols) => cols.map((c, idx) => (idx === i ? { ...c, ...patch } : c)));
  }
  function addColumn() {
    setColumns((cols) => [...cols, { name: "New", position: cols.length }]);
  }
  function removeColumn(i: number) {
    setColumns((cols) => cols.filter((_, idx) => idx !== i).map((c, idx) => ({ ...c, position: idx })));
  }

  const mut = useMutation({
    mutationFn: () => api<{ id: string; name: string }>("/api/v1/boards", {
      method: "POST",
      body: {
        name: name.trim(),
        mode,
        repo_url: mode === "github" ? repoUrl.trim() || undefined : undefined,
        repo_path: mode === "local" ? repoPath.trim() || undefined : undefined,
        preset: presetId,
        columns: columns.map((c, i) => ({
          name: c.name,
          position: i,
          color: c.color,
          wip_limit: c.wip_limit,
          gate: c.gate,
        })),
        agents: skipAgents ? undefined : [agent],
      },
    }),
    onSuccess: (board) => {
      qc.invalidateQueries({ queryKey: ["boards"] });
      onCreated?.(board.id);
      reset();
      onClose();
    },
    onError: (err: Error) => setError(err.message),
  });

  function reset() {
    setStep(0);
    setName("");
    setMode("local");
    setRepoUrl("");
    setRepoPath("");
    setPresetId("default");
    setColumns(PRESETS[0].columns.map((c) => ({ ...c })));
    setSkipAgents(true);
    setError(null);
  }

  if (!open) return null;

  const canNext = step === 0 ? !!name.trim() : true;

  return (
    <div
      onClick={(e) => { if (e.target === e.currentTarget) { reset(); onClose(); } }}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(40, 30, 15, 0.45)",
        backdropFilter: "blur(4px)",
        zIndex: 90,
        display: "grid",
        placeItems: "center",
        padding: 24,
      }}
    >
      <div style={{
        width: 600,
        maxWidth: "100%",
        background: "var(--bg-elev)",
        border: "1px solid var(--border-strong)",
        borderRadius: "var(--radius-lg)",
        boxShadow: "var(--shadow-overlay)",
        display: "flex",
        flexDirection: "column",
        maxHeight: "90vh",
      }}>
        <header style={{
          padding: "18px 24px",
          borderBottom: "1px solid var(--border)",
          display: "flex",
          alignItems: "center",
          gap: 16,
        }}>
          <h2 style={{ flex: 1 }}>New board</h2>
          {STEPS.map((label, i) => (
            <div key={label} style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              fontSize: 12,
              color: i === step ? "var(--fg)" : "var(--fg-muted)",
              fontWeight: i === step ? 600 : 500,
            }}>
              <span style={{
                width: 18,
                height: 18,
                borderRadius: 9,
                background: i <= step ? "var(--accent)" : "var(--bg-elev-2)",
                color: i <= step ? "#fff" : "var(--fg-muted)",
                display: "grid",
                placeItems: "center",
                fontSize: 10,
                fontWeight: 600,
              }}>{i + 1}</span>
              {label}
            </div>
          ))}
        </header>

        <div style={{ padding: 24, overflow: "auto", display: "flex", flexDirection: "column", gap: 14 }}>
          {step === 0 && (
            <>
              <label style={labelStyle}>Board name
                <input value={name} onChange={(e) => setName(e.target.value)} style={inputStyle} autoFocus required placeholder="e.g. Backend revamp" />
              </label>
              <label style={labelStyle}>Mode
                <select value={mode} onChange={(e) => setMode(e.target.value as "local" | "github")} style={inputStyle}>
                  <option value="local">Local (no GitHub sync)</option>
                  <option value="github">GitHub (mirror cards to issues)</option>
                </select>
              </label>
              {mode === "github" ? (
                <label style={labelStyle}>Repo URL
                  <input value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} style={inputStyle} placeholder="https://github.com/org/repo" />
                </label>
              ) : (
                <label style={labelStyle}>Repo path <span style={{ color: "var(--fg-muted)" }}>(optional)</span>
                  <input value={repoPath} onChange={(e) => setRepoPath(e.target.value)} style={inputStyle} placeholder="/abs/path/to/repo" />
                </label>
              )}
            </>
          )}

          {step === 1 && (
            <>
              <label style={labelStyle}>Workflow preset
                <select value={presetId} onChange={(e) => pickPreset(e.target.value)} style={inputStyle}>
                  {PRESETS.map((p) => (
                    <option key={p.id} value={p.id}>{p.label} — {p.sub}</option>
                  ))}
                </select>
              </label>

              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginTop: 4 }}>
                <span style={{ fontSize: 12, color: "var(--fg-dim)", fontWeight: 600 }}>Columns</span>
                <button type="button" className="btn ghost" onClick={addColumn}>+ Column</button>
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                {columns.map((c, i) => (
                  <div key={i} style={{
                    display: "grid",
                    gridTemplateColumns: "32px 1fr 100px 130px 36px",
                    gap: 6,
                    alignItems: "center",
                  }}>
                    <span style={{ fontFamily: "var(--mono)", color: "var(--fg-muted)", fontSize: 11, textAlign: "center" }}>{i + 1}</span>
                    <input value={c.name} onChange={(e) => updateColumn(i, { name: e.target.value })} style={inputStyle} />
                    <input
                      type="number"
                      min={0}
                      value={c.wip_limit ?? ""}
                      onChange={(e) => updateColumn(i, { wip_limit: e.target.value === "" ? undefined : Number(e.target.value) })}
                      style={inputStyle}
                      placeholder="WIP"
                    />
                    <select
                      value={c.gate ?? ""}
                      onChange={(e) => updateColumn(i, { gate: e.target.value as WizardColumn["gate"] })}
                      style={inputStyle}
                    >
                      <option value="">no gate</option>
                      <option value="human">human</option>
                      <option value="auto-validate">auto-validate</option>
                    </select>
                    <button type="button" onClick={() => removeColumn(i)} title="Remove" style={{
                      background: "transparent", border: "1px solid var(--border)",
                      color: "var(--fg-muted)", borderRadius: "var(--radius-sm)",
                      cursor: "pointer", fontSize: 14,
                    }}>×</button>
                  </div>
                ))}
              </div>
            </>
          )}

          {step === 2 && (
            <>
              <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
                <input type="checkbox" checked={skipAgents} onChange={(e) => setSkipAgents(e.target.checked)} />
                Skip agent setup (you can add agents later from the board)
              </label>
              {!skipAgents && (
                <>
                  <label style={labelStyle}>Agent name
                    <input value={agent.name} onChange={(e) => setAgent({ ...agent, name: e.target.value })} style={inputStyle} />
                  </label>
                  <label style={labelStyle}>Agent type
                    <select value={agent.agent_type} onChange={(e) => setAgent({ ...agent, agent_type: e.target.value })} style={inputStyle}>
                      <option value="claude-code">claude-code</option>
                      <option value="codex">codex</option>
                      <option value="gemini">gemini</option>
                      <option value="cursor-agent">cursor-agent</option>
                      <option value="amp">amp</option>
                      <option value="droid">droid</option>
                      <option value="opencode">opencode</option>
                      <option value="qwen">qwen</option>
                      <option value="ccr">ccr</option>
                      <option value="acp">acp</option>
                      <option value="gh-copilot">gh-copilot</option>
                    </select>
                  </label>
                  <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
                    <input type="checkbox" checked={!!agent.is_default} onChange={(e) => setAgent({ ...agent, is_default: e.target.checked })} />
                    Make this the default agent
                  </label>
                </>
              )}
            </>
          )}

          {step === 3 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 12, fontSize: 13 }}>
              <Row label="Name" value={name} />
              <Row label="Mode" value={mode === "github" ? `GitHub — ${repoUrl || "(no URL)"}` : `Local${repoPath ? ` — ${repoPath}` : ""}`} />
              <Row label="Preset" value={PRESETS.find((p) => p.id === presetId)?.label ?? presetId} />
              <Row label="Columns" value={columns.map((c) => c.name + (c.gate ? ` ⛨${c.gate}` : "")).join(" → ")} />
              <Row label="Agent" value={skipAgents ? "(skip)" : `${agent.name} · ${agent.agent_type}`} />
              {error && <div style={{ color: "var(--danger)" }}>{error}</div>}
            </div>
          )}
        </div>

        <footer style={{
          padding: "14px 24px",
          borderTop: "1px solid var(--border)",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: 8,
        }}>
          <button type="button" className="btn ghost" onClick={() => { reset(); onClose(); }}>Cancel</button>
          <div style={{ display: "flex", gap: 8 }}>
            {step > 0 && (
              <button type="button" className="btn" onClick={() => setStep((s) => (s - 1) as Step)}>Back</button>
            )}
            {step < 3 && (
              <button type="button" className="btn primary" disabled={!canNext} onClick={() => setStep((s) => (s + 1) as Step)}>Next</button>
            )}
            {step === 3 && (
              <button type="button" className="btn primary" disabled={mut.isPending} onClick={() => mut.mutate()}>
                {mut.isPending ? "Creating…" : "Create board"}
              </button>
            )}
          </div>
        </footer>
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "grid", gridTemplateColumns: "100px 1fr", gap: 12 }}>
      <span style={{ color: "var(--fg-muted)", fontWeight: 500 }}>{label}</span>
      <span>{value}</span>
    </div>
  );
}

const labelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  fontSize: 12,
  color: "var(--fg-dim)",
  fontWeight: 500,
};
const inputStyle: React.CSSProperties = {
  padding: "8px 12px",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  background: "var(--bg)",
  color: "var(--fg)",
  fontSize: 14,
  outline: "none",
  fontFamily: "inherit",
  width: "100%",
};
