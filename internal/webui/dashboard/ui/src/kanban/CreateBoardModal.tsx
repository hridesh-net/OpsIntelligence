import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createBoard } from "@/api/kanban";

interface CreateBoardModalProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (boardId: string) => void;
}

// Presets mirror workflowPresets in gateway/kanban_api.go. Backend exposes
// GET /api/v1/workflow-presets; we hardcode the slug list here and let
// the user pick. Adding a preset on the server doesn't require a UI change.
const PRESETS: Array<{ id: string; label: string; sub: string }> = [
  { id: "default", label: "Default", sub: "Inbox → Backlog → Todo → In Progress → Review → Done" },
  { id: "dev", label: "Development", sub: "Standard agent dev flow with auto-validate gate before Review" },
  { id: "research", label: "Research", sub: "Lighter pipeline tuned for spikes and discovery" },
  { id: "support", label: "Support", sub: "Triage queue for inbound bugs / Sentry imports" },
  { id: "ops", label: "Operations", sub: "Change-management flow with a human gate on rollout" },
];

export function CreateBoardModal({ open, onClose, onCreated }: CreateBoardModalProps) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [preset, setPreset] = useState("default");
  const [mode, setMode] = useState<"local" | "github">("local");
  const [repoUrl, setRepoUrl] = useState("");
  const [error, setError] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => createBoard({
      name: name.trim(),
      preset,
      mode,
      repo_url: mode === "github" ? repoUrl.trim() || undefined : undefined,
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
    setName("");
    setPreset("default");
    setMode("local");
    setRepoUrl("");
    setError(null);
  }

  if (!open) return null;

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
      <form
        onSubmit={(e) => { e.preventDefault(); if (!mut.isPending) mut.mutate(); }}
        style={{
          width: 460,
          maxWidth: "100%",
          background: "var(--bg-elev)",
          border: "1px solid var(--border-strong)",
          borderRadius: "var(--radius-lg)",
          boxShadow: "var(--shadow-overlay)",
          padding: 24,
          display: "flex",
          flexDirection: "column",
          gap: 14,
        }}
      >
        <h2>New board</h2>

        <label style={labelStyle}>Name
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            style={inputStyle}
            placeholder="e.g. Backend revamp"
            required
            autoFocus
          />
        </label>

        <label style={labelStyle}>Workflow preset
          <select value={preset} onChange={(e) => setPreset(e.target.value)} style={inputStyle}>
            {PRESETS.map((p) => (
              <option key={p.id} value={p.id}>{p.label} — {p.sub}</option>
            ))}
          </select>
        </label>

        <label style={labelStyle}>Mode
          <select value={mode} onChange={(e) => setMode(e.target.value as "local" | "github")} style={inputStyle}>
            <option value="local">Local (no GitHub sync)</option>
            <option value="github">GitHub (mirror cards to issues)</option>
          </select>
        </label>

        {mode === "github" && (
          <label style={labelStyle}>Repo URL
            <input
              value={repoUrl}
              onChange={(e) => setRepoUrl(e.target.value)}
              style={inputStyle}
              placeholder="https://github.com/org/repo"
            />
          </label>
        )}

        {error && <div style={{ color: "var(--danger)", fontSize: 12 }}>{error}</div>}

        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 6 }}>
          <button type="button" className="btn ghost" onClick={() => { reset(); onClose(); }}>
            Cancel
          </button>
          <button type="submit" className="btn primary" disabled={!name.trim() || mut.isPending}>
            {mut.isPending ? "Creating…" : "Create board"}
          </button>
        </div>
      </form>
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
};
