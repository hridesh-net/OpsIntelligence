import { useState } from "react";
import { useStore } from "../../store";
import { Check, Close } from "../Icons";
import Avatar from "../Avatar";
import Modal from "./Modal";
import m from "./Modal.module.css";
import fm from "./forms.module.css";

const TYPE_OPTS: [string, string][] = [
  ["feat", "Feature"], ["fix", "Fix"], ["infra", "Infra"], ["sec", "Security"], ["research", "Research"], ["chore", "Chore"],
];
const PRIO_OPTS: [string, string][] = [["H", "High"], ["M", "Medium"], ["L", "Low"]];

export default function TaskFormModal() {
  const st = useStore();
  const d = st.taskDraft;
  const isNew = st.taskIsNew;
  const [acInput, setAcInput] = useState("");
  const [labInput, setLabInput] = useState("");
  if (!d) return null;

  const typeLabel = (TYPE_OPTS.find((t) => t[0] === d.type) || ["", ""])[1];

  return (
    <Modal onClose={st.closeTaskForm}>
      <div className={m["p-head"]}>
        <div className={m["p-top"]}>
          <span className="cid">{isNew ? "New ticket" : "#" + d.id}</span>
          <span className={"ttag " + d.type} style={{ marginLeft: 2 }}>{typeLabel}</span>
          <span className={m["p-close"]} onClick={st.closeTaskForm}><Close /></span>
        </div>
        <div className={m["p-subtitle"]}>{isNew ? "Create task" : "Edit task"}</div>
      </div>

      <div className={m["p-body"]}>
        <div className="fld">
          <label className="fld-l">Title <span className="tip">required</span></label>
          <input className="inp" value={d.title} placeholder="Short, action-oriented summary of the work" onChange={(e) => st.updateTaskDraft({ title: e.target.value })} />
        </div>

        <div className={fm["cfg-grid"]}>
          <div className={fm["cfg-col"]}>
            <div className="fld">
              <label className="fld-l">Work type</label>
              <div className="segrow">
                {TYPE_OPTS.map(([k, label]) => (
                  <button key={k} className={d.type === k ? "on" : ""} onClick={() => st.updateTaskDraft({ type: k })}>{label}</button>
                ))}
              </div>
            </div>
            <div className="fld">
              <label className="fld-l">Priority</label>
              <div className="segrow">
                {PRIO_OPTS.map(([k, label]) => (
                  <button key={k} className={d.prio === k ? "on" : ""} onClick={() => st.updateTaskDraft({ prio: k as typeof d.prio })}>{label}</button>
                ))}
              </div>
            </div>
            <div className="fld">
              <label className="fld-l">Workflow stage</label>
              <select className="inp" value={d.col} onChange={(e) => st.updateTaskDraft({ col: e.target.value })}>
                {st.workflow.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </div>
          </div>

          <div className={fm["cfg-col"]}>
            <div className="fld">
              <label className="fld-l">Assign agents</label>
              <div className={fm["apick-grid"]}>
                {Object.entries(st.agents).map(([k, a]) => {
                  const on = d.agents.includes(k);
                  return (
                    <span
                      key={k}
                      className={fm.apick + (on ? " " + fm.on : "")}
                      onClick={() => {
                        const next = on ? d.agents.filter((x) => x !== k) : [...d.agents, k];
                        st.updateTaskDraft({ agents: next });
                      }}
                    >
                      <Avatar color={a.color} ini={a.ini} />
                      {a.name}
                      {on && <span className={fm.chk}>✓</span>}
                    </span>
                  );
                })}
              </div>
              <div className="fld-hint">Leave empty to let the orchestrator auto-assign on pickup.</div>
            </div>
          </div>
        </div>

        <div className="fld">
          <label className="fld-l">Description</label>
          <textarea className="inp" rows={4} value={d.desc || ""} placeholder="What needs to be done and why. Context the agent should know." onChange={(e) => st.updateTaskDraft({ desc: e.target.value })} />
        </div>

        <div className="fld">
          <label className="fld-l">Acceptance criteria <span className="tip">enter to add</span></label>
          <div className={fm["ac-list"]}>
            {(d.ac || []).map((a, i) => (
              <div className={fm["ac-row"]} key={i}>
                <span className={fm["ac-box"]}><Check size={12} /></span>
                <span className={fm["ac-txt"]}>{a}</span>
                <span className={fm["ac-x"]} onClick={() => st.updateTaskDraft({ ac: d.ac!.filter((_, j) => j !== i) })}><Close size={13} /></span>
              </div>
            ))}
          </div>
          <div className={fm["ac-add"]}>
            <span className={fm["ac-box"]}>＋</span>
            <input
              value={acInput}
              placeholder="Add a criterion the agent must satisfy…"
              onChange={(e) => setAcInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && acInput.trim()) {
                  st.updateTaskDraft({ ac: [...(d.ac || []), acInput.trim()] });
                  setAcInput("");
                }
              }}
            />
          </div>
          <div className="fld-hint">The agent treats these as a definition-of-done checklist before requesting review.</div>
        </div>

        <div className="fld">
          <label className="fld-l">Labels <span className="tip">enter to add</span></label>
          <div className="capedit">
            {d.labels.map((l, i) => (
              <span className="cap" key={i}>
                {l}<span className="x" onClick={() => st.updateTaskDraft({ labels: d.labels.filter((_, j) => j !== i) })}>✕</span>
              </span>
            ))}
            <input
              value={labInput}
              placeholder="add label…"
              onChange={(e) => setLabInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && labInput.trim()) {
                  st.updateTaskDraft({ labels: [...d.labels, labInput.trim().toLowerCase()] });
                  setLabInput("");
                } else if (e.key === "Backspace" && !labInput && d.labels.length) {
                  st.updateTaskDraft({ labels: d.labels.slice(0, -1) });
                }
              }}
            />
          </div>
        </div>
      </div>

      <div className={m["p-actions"]}>
        {!isNew && (
          <button className="btn" style={{ marginRight: "auto", color: "var(--danger)", borderColor: "var(--danger-soft)" }} onClick={() => st.deleteTask(d.id)}>
            Delete
          </button>
        )}
        <button className="btn" onClick={st.closeTaskForm}>Cancel</button>
        <button className="btn btn-primary" onClick={st.saveTaskForm}>{isNew ? "Create task" : "Save changes"}</button>
      </div>
    </Modal>
  );
}
