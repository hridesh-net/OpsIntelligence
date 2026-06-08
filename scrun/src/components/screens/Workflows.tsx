import { Fragment, useRef } from "react";
import { useStore } from "../../store";
import { Gate, Grip, Trash } from "../Icons";
import s from "./Screens.module.css";

function Rules({ autoAssign, autoValidate }: { autoAssign: string | null; autoValidate: boolean }) {
  const rules: JSX.Element[] = [];
  if (autoAssign === "auto")
    rules.push(
      <div className={s.rl} key="a">
        <span className={s.ri}><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="M12 2v4M12 18v4M2 12h4M18 12h4" /><circle cx="12" cy="12" r="3" /></svg></span>
        Auto-assign best agent on entry
      </div>,
    );
  if (autoAssign === "review")
    rules.push(
      <div className={s.rl} key="r">
        <span className={s.ri}><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="m9 11 3 3L22 4" /><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11" /></svg></span>
        Route to <b>Code Review Agent</b>
      </div>,
    );
  if (autoAssign === "keep")
    rules.push(
      <div className={s.rl} key="k">
        <span className={s.ri}><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="M5 12h14M12 5v14" /></svg></span>
        Keep current agent working
      </div>,
    );
  if (autoValidate)
    rules.push(
      <div className={s.rl} key="v">
        <span className={s.ri}><svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="m9 11 3 3L22 4" /></svg></span>
        Auto-validate when tests pass
      </div>,
    );
  if (!rules.length) rules.push(<div className={s.rl} key="n" style={{ color: "var(--text-faint)" }}>No automation rules</div>);
  return <>{rules}</>;
}

export default function Workflows() {
  const st = useStore();
  const dragId = useRef<string | null>(null);

  return (
    <div className={s.sbody}>
      <div className={s.shead}>
        <div>
          <h1>Workflow Builder</h1>
          <p>Design the stages your agents move work through. Drag to reorder, rename inline, set WIP limits, and add approval or auto-validate gates between stages.</p>
        </div>
        <div className={s.right}>
          <button className="btn">Import</button>
          <button className="btn btn-primary">Save workflow</button>
        </div>
      </div>

      <div className={s.flow}>
        {st.workflow.map((stage, i) => (
          <Fragment key={stage.id}>
            <div
              className={s.stage}
              draggable
              onDragStart={(e) => {
                dragId.current = stage.id;
                (e.currentTarget as HTMLElement).classList.add(s.dragging);
              }}
              onDragEnd={(e) => (e.currentTarget as HTMLElement).classList.remove(s.dragging)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                if (dragId.current) st.reorderStages(dragId.current, stage.id);
              }}
            >
              <div className={s["stage-top"]}>
                <span className={s.grip}><Grip /></span>
                <span className={s.cdot} style={{ background: stage.dot }} />
                <span
                  className={s.sname}
                  contentEditable
                  suppressContentEditableWarning
                  onBlur={(e) => st.renameStage(stage.id, e.currentTarget.textContent || "")}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      (e.currentTarget as HTMLElement).blur();
                    }
                  }}
                >
                  {stage.name}
                </span>
                <span className={s.sidx}>0{i + 1}</span>
              </div>
              <div className={s["stage-meta"]}>
                <span className={s.chip + (stage.wip ? " " + s.on : "")} onClick={() => st.toggleStageWip(stage.id)}>
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="M3 12h18M3 6h18M3 18h18" /></svg>
                  {stage.wip ? "WIP " + stage.wip : "No WIP"}
                </span>
                <span className={s.chip + (stage.gate === "human" ? " " + s.on : "")} onClick={() => st.toggleStageGate(stage.id)}>
                  {stage.gate === "human" ? <><Gate />Human gate</> : stage.gate === "auto" ? <><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}><path d="m9 11 3 3L22 4" /></svg>Auto gate</> : "No gate"}
                </span>
              </div>
              <div className={s["stage-rule"]}>
                <Rules autoAssign={stage.rules.autoAssign} autoValidate={stage.rules.autoValidate} />
              </div>
              <div className={s["stage-foot"]}>
                <span className={s["sf-count"]}>
                  {st.cards.filter((k) => k.col === stage.id).length} task{st.cards.filter((k) => k.col === stage.id).length !== 1 ? "s" : ""}
                </span>
                <span className={s["sf-actions"]}>
                  <span title="Colour" onClick={() => st.cycleStageDye(stage.id)}>
                    <svg width="13" height="13" viewBox="0 0 24 24" fill={stage.dot} stroke={stage.dot}><circle cx="12" cy="12" r="7" /></svg>
                  </span>
                  <span title="Delete stage" onClick={() => st.deleteStage(stage.id)}><Trash /></span>
                </span>
              </div>
            </div>
            {i < st.workflow.length - 1 && (
              <div className={s.transition + (st.workflow[i + 1].gate ? " " + s.gated : "")}>
                <div className={s.glabel}>{st.workflow[i + 1].gate === "human" ? "approve" : st.workflow[i + 1].gate === "auto" ? "validate" : "auto"}</div>
                <div className={s.arrow} />
              </div>
            )}
          </Fragment>
        ))}
        <div className={s["stage-add"]} onClick={st.addStage}>+</div>
      </div>

      <div className={s["wf-legend"]}>
        <div className={s.lg}><span className={s.sw} style={{ background: "var(--border-strong)" }} />Auto transition</div>
        <div className={s.lg}><span className={s.sw} style={{ background: "var(--warning)" }} />Gated transition (approval / validate)</div>
        <div className={s.lg}><span className={s.sw} style={{ background: "var(--accent)" }} />Automation rule active</div>
      </div>

      <div style={{ marginTop: 26 }}>
        <div className="p-lbl" style={{ marginBottom: 10, fontSize: 10, textTransform: "uppercase", letterSpacing: ".1em", color: "var(--text-faint)", fontWeight: 700 }}>
          Start from a preset
        </div>
        <div className={s["wf-presets"]}>
          <div className={s.preset} onClick={() => st.applyWorkflowPreset("dev")}><b>Software delivery</b><small>backlog → todo → build → test → review → done</small></div>
          <div className={s.preset} onClick={() => st.applyWorkflowPreset("research")}><b>Research spike</b><small>intake → analyse → synthesise → report</small></div>
          <div className={s.preset} onClick={() => st.applyWorkflowPreset("support")}><b>Triage &amp; resolve</b><small>inbox → triage → fix → verify → closed</small></div>
        </div>
      </div>
    </div>
  );
}
