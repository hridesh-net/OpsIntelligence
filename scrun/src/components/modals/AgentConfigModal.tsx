import { useState } from "react";
import { useStore } from "../../store";
import { Close, Doc } from "../Icons";
import Modal from "./Modal";
import m from "./Modal.module.css";
import fm from "./forms.module.css";

const MODELS = ["claude-opus-4.7", "claude-sonnet-4.6", "gpt-4-turbo", "gpt-4.1", "llama-3.3-70b", "gemini-2.0-pro"];
const MEM_DESC: Record<string, string> = {
  persistent: "Remembers across sessions & tasks",
  session: "Remembers only within a session",
  none: "Stateless — no memory between runs",
};
const AUTONOMY_HINT: Record<string, string> = {
  supervised: "Pauses for human approval at gates.",
  auto: "Self-validates when tests pass; humans gate risk.",
  full: "Acts end-to-end without human gates.",
};

function Seg({ value, opts, onChange }: { value: string; opts: [string, string][]; onChange: (v: string) => void }) {
  return (
    <div className="segrow">
      {opts.map(([v, label]) => (
        <button key={v} className={v === value ? "on" : ""} onClick={() => onChange(v)}>{label}</button>
      ))}
    </div>
  );
}

export default function AgentConfigModal() {
  const st = useStore();
  const d = st.agentDraft;
  const [capInput, setCapInput] = useState("");
  if (!d) return null;
  const up = st.updateAgentDraft;

  return (
    <Modal onClose={st.closeAgentConfig}>
      <div className={m["p-head"]}>
        <div className={m["p-top"]}>
          <span className={fm.aav} style={{ width: 34, height: 34, borderRadius: 10, background: d.color, fontSize: 12 }}>{d.ini}</span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <input className="inp" value={d.name} style={{ fontSize: 16, fontWeight: 700, padding: "4px 8px", background: "transparent", borderColor: "transparent" }} onChange={(e) => up("name", e.target.value)} />
            <span className={fm["modal-sub"]} style={{ paddingLeft: 8 }}>{d.model} · {d.provider}</span>
          </div>
          <span className={m["p-close"]} onClick={st.closeAgentConfig}><Close /></span>
        </div>
        <div className={m["p-subtitle"]}>Agent configuration</div>
      </div>

      <div className={m["p-body"]}>
        <div className={fm["cfg-grid"]}>
          <div className={fm["cfg-col"]}>
            <div className="fld">
              <label className="fld-l">Role / assignment</label>
              <input className="inp" value={d.role} placeholder="e.g. Infrastructure Engineer" onChange={(e) => up("role", e.target.value)} />
            </div>

            <div className="fld">
              <label className="fld-l">Capabilities <span className="tip">enter to add</span></label>
              <div className="capedit">
                {d.caps.map((c, i) => (
                  <span className="cap" key={i}>
                    {c}<span className="x" onClick={() => up("caps", d.caps.filter((_, j) => j !== i))}>✕</span>
                  </span>
                ))}
                <input
                  value={capInput}
                  placeholder="add capability…"
                  onChange={(e) => setCapInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && capInput.trim()) {
                      up("caps", [...d.caps, capInput.trim().toLowerCase()]);
                      setCapInput("");
                    } else if (e.key === "Backspace" && !capInput && d.caps.length) {
                      up("caps", d.caps.slice(0, -1));
                    }
                  }}
                />
              </div>
              <div className="fld-hint">Defines what work the orchestrator routes to this agent.</div>
            </div>

            <div className="fld">
              <label className="fld-l">Model</label>
              <select className="inp" value={d.model} onChange={(e) => up("model", e.target.value)}>
                {MODELS.map((mm) => <option key={mm}>{mm}</option>)}
              </select>
            </div>

            <div className="fld">
              <label className="fld-l">Autonomy level</label>
              <Seg value={d.autonomy} opts={[["supervised", "Supervised"], ["auto", "Auto-validate"], ["full", "Full auto"]]} onChange={(v) => up("autonomy", v)} />
              <div className="fld-hint">{AUTONOMY_HINT[d.autonomy]}</div>
            </div>

            <div className="fld">
              <label className="fld-l">Daily spend cap</label>
              <div className="rng-row"><span>Budget / day</span><b>${d.spendCap}</b></div>
              <input type="range" className="rng" min={5} max={100} step={1} value={d.spendCap} onChange={(e) => up("spendCap", +e.target.value)} />
            </div>

            <div className="fld">
              <label className="fld-l">Max parallel tasks</label>
              <div className="rng-row"><span>Concurrency</span><b>{d.maxParallel}</b></div>
              <input type="range" className="rng" min={1} max={6} step={1} value={d.maxParallel} onChange={(e) => up("maxParallel", +e.target.value)} />
            </div>
          </div>

          <div className={fm["cfg-col"]}>
            <div className="fld">
              <label className="fld-l">Personal context · system instructions</label>
              <textarea className="inp" rows={6} value={d.instructions} onChange={(e) => up("instructions", e.target.value)} />
              <div className="fld-hint">The agent's persona and standing rules — prepended to every task it runs.</div>
            </div>

            <div className="fld">
              <label className="fld-l">Knowledge sources</label>
              <div className={fm.know}>
                {d.knowledge.map((k, i) => (
                  <div className={fm.ksrc} key={i}>
                    <span className={fm.kico}><Doc /></span>
                    <span className={fm.kname}>{k[0]}</span>
                    <span className={fm.kmeta}>{k[1]}</span>
                    <span className={fm.x} onClick={() => up("knowledge", d.knowledge.filter((_, j) => j !== i))}><Close size={13} /></span>
                  </div>
                ))}
              </div>
              <div className={fm["know-add"]} onClick={() => up("knowledge", [...d.knowledge, ["New source", "linked"]])}>+ Connect knowledge source</div>
            </div>

            <div className="fld">
              <label className="fld-l">Memory</label>
              <Seg value={d.memory.mode} opts={[["persistent", "Persistent"], ["session", "Session"], ["none", "None"]]} onChange={(v) => up("memory.mode", v)} />
              <div className="fld-hint">{MEM_DESC[d.memory.mode]}</div>
            </div>

            {d.memory.mode !== "none" && (
              <div className="fld">
                <label className="fld-l">Memory scope</label>
                <Seg value={d.memory.scope} opts={[["project", "Per-project"], ["global", "Global"], ["task", "Per-task"]]} onChange={(v) => up("memory.scope", v)} />
                <div style={{ height: 14 }} />
                <label className="fld-l">Working context window</label>
                <div className="rng-row"><span>Tokens held in context</span><b>{d.memory.contextK}K</b></div>
                <input type="range" className="rng" min={16} max={256} step={16} value={d.memory.contextK} onChange={(e) => up("memory.contextK", +e.target.value)} />
                <div style={{ height: 14 }} />
                <label className="fld-l">Retention</label>
                <Seg value={d.memory.retention} opts={[["7d", "7 days"], ["30d", "30 days"], ["90d", "90 days"], ["forever", "Forever"]]} onChange={(v) => up("memory.retention", v)} />
              </div>
            )}
          </div>
        </div>
      </div>

      <div className={m["p-actions"]}>
        <button className="btn" onClick={st.closeAgentConfig}>Cancel</button>
        <button className="btn btn-primary" onClick={st.saveAgentConfig}>Save configuration</button>
      </div>
    </Modal>
  );
}
