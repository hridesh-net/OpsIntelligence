import { useEffect, useRef, useState } from "react";
import { useStore, suStages, SU_WORKFLOWS, autoKey } from "../../store";
import { applyAccentVars } from "../../lib/theme";
import Logo from "../Logo";
import { ArrowRight, Check, ChevronRight, Close, Grip } from "../Icons";
import s from "./Setup.module.css";

const SU_COLORS = ["#e4572e", "#2898da", "#2dd4bf", "#a78bfa", "#f5b042", "#34d399", "#60a5fa"];
const STEPS = [
  ["Project", "Name, key & accent"],
  ["Workflow", "Stages & gates"],
  ["Agents", "Connect your workforce"],
  ["Review", "Confirm & launch"],
];

function ProjectPane() {
  const setup = useStore((st) => st.setup);
  const suPatch = useStore((st) => st.suPatch);
  const [keyEdited, setKeyEdited] = useState(setup.key !== autoKey(setup.name));

  return (
    <div className={s["su-pane"]}>
      <div className={s["su-eyebrow"]}>Project</div>
      <h1 className={s["su-h"]}>Name your board</h1>
      <p className={s["su-sub"]}>
        This board organises the work your agent workforce will pick up. Give it a clear name and a short key — the key prefixes every ticket (e.g. <b>{setup.key}-128</b>).
      </p>
      <div className={s["su-row"]}>
        <div className="fld">
          <label className="fld-l">Board name</label>
          <input
            className="inp"
            value={setup.name}
            placeholder="e.g. Platform Workforce"
            onChange={(e) => {
              const name = e.target.value;
              suPatch(keyEdited ? { name } : { name, key: autoKey(name) });
            }}
          />
        </div>
        <div className="fld">
          <label className="fld-l">Ticket key</label>
          <input
            className="inp"
            value={setup.key}
            maxLength={4}
            style={{ textAlign: "center", fontFamily: "var(--font-mono)", fontWeight: 700, textTransform: "uppercase" }}
            onChange={(e) => {
              setKeyEdited(true);
              suPatch({ key: e.target.value.toUpperCase().replace(/[^A-Z]/g, "").slice(0, 4) || "BD" });
            }}
          />
        </div>
      </div>
      <div className="fld">
        <label className="fld-l">Description <span className="tip">optional</span></label>
        <textarea className="inp" rows={2} value={setup.desc} placeholder="What this board is for and who it serves." onChange={(e) => suPatch({ desc: e.target.value })} />
      </div>
      <div className="fld">
        <label className="fld-l">Accent colour</label>
        <div className={s["su-iconpick"]}>
          {SU_COLORS.map((c) => (
            <span key={c} className={s["su-sw"] + (c === setup.color ? " " + s.on : "")} style={{ background: c, color: c }} onClick={() => suPatch({ color: c })} />
          ))}
        </div>
      </div>
      <div className="fld">
        <label className="fld-l">Preview</label>
        <div className={s["su-preview-card"]}>
          <div className={s["pc-top"]}>
            <span className={s["pc-ico"]} style={{ background: setup.color }}>{(setup.name[0] || "B").toUpperCase()}</span>
            <div>
              <div className={s["pc-name"]}>{setup.name || "Untitled board"}</div>
              <div className={s["pc-key"]}>{setup.key} · {SU_WORKFLOWS[setup.preset].name}</div>
            </div>
          </div>
          <div className={s["pc-demo"]}>{setup.key}-128 · first ticket will look like this</div>
        </div>
      </div>
    </div>
  );
}

function WorkflowPane() {
  const setup = useStore((st) => st.setup);
  const st = useStore();
  const stages = suStages(setup);
  const dragI = useRef<number | null>(null);

  return (
    <div className={s["su-pane"]}>
      <div className={s["su-eyebrow"]}>Workflow</div>
      <h1 className={s["su-h"]}>Choose your stages</h1>
      <p className={s["su-sub"]}>
        Pick a starting template, then rename, reorder or add stages. Gates (🔒 human approval, ✓ auto-validate) control how agents hand work between stages. You can refine this any time in the Workflow builder.
      </p>
      <div className={s["su-presets"]}>
        {Object.entries(SU_WORKFLOWS).map(([id, w]) => (
          <div key={id} className={s["su-pre"] + (id === setup.preset ? " " + s.on : "")} onClick={() => st.suPatch({ preset: id, stages: null })}>
            <span className={s["pre-check"]}><Check size={11} /></span>
            <b>{w.name}</b>
            <p>{w.desc}</p>
            <div className={s["pre-flow"]}>
              {w.stages.map((stg) => (
                <span className={s.pchip} key={stg[0]}><span className={s.cd} style={{ background: stg[2] }} />{stg[1]}</span>
              ))}
            </div>
          </div>
        ))}
      </div>
      <div className={s["su-stagelist"]}>
        {stages.map((stg, i) => (
          <div
            key={stg.id}
            className={s["su-stage"]}
            draggable
            onDragStart={() => (dragI.current = i)}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              if (dragI.current != null) st.suReorderStages(dragI.current, i);
            }}
          >
            <span className={s.grip}><Grip /></span>
            <span className={s.cd} style={{ background: stg.dot }} />
            <span
              className={s.nm}
              contentEditable
              suppressContentEditableWarning
              onBlur={(e) => st.suRenameStage(i, e.currentTarget.textContent || "")}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  (e.currentTarget as HTMLElement).blur();
                }
              }}
            >
              {stg.name}
            </span>
            <span className={s.badge2 + (stg.gate ? " " + s.gate : "")} onClick={() => st.suToggleGate(i)}>
              {stg.gate === "human" ? "🔒 approval" : stg.gate === "auto" ? "✓ auto" : "no gate"}
            </span>
            <span className={s.rm} onClick={() => st.suRemoveStage(i)}><Close size={14} /></span>
          </div>
        ))}
      </div>
      <div className={s["su-stage-add"]} onClick={st.suAddStage}>+ Add a custom stage</div>
    </div>
  );
}

function AgentsPane() {
  const setup = useStore((st) => st.setup);
  const st = useStore();
  const agents = Object.entries(st.agents);

  return (
    <div className={s["su-pane"]}>
      <div className={s["su-eyebrow"]}>Workforce</div>
      <h1 className={s["su-h"]}>Connect your agents</h1>
      <p className={s["su-sub"]}>
        Select which autonomous agents this board can route work to. The orchestrator assigns tickets to the best-matched agent by capability. You can connect more and fine-tune each agent later.
      </p>
      <div className={s["su-selall"]}>
        <span>{setup.agents.length} of {agents.length} connected</span>
        <span className={s.lnk} onClick={st.suSelectAllAgents}>Select all</span>
        <span className={s.lnk} onClick={st.suClearAgents}>Clear</span>
      </div>
      <div className={s["su-agrid"]}>
        {agents.map(([k, a]) => {
          const on = setup.agents.includes(k);
          return (
            <div key={k} className={s["su-ag"] + (on ? " " + s.on : "")} onClick={() => st.suToggleAgent(k)}>
              <span className={s.aav} style={{ background: a.color }}>{a.ini}</span>
              <div className={s.ai}><b>{a.name}</b><small>{a.role || a.model}</small></div>
              <span className={s.chk}><Check size={13} /></span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ReviewPane() {
  const setup = useStore((st) => st.setup);
  const st = useStore();
  const stages = suStages(setup);

  return (
    <div className={s["su-pane"]}>
      <div className={s["su-eyebrow"]}>Review</div>
      <h1 className={s["su-h"]}>Ready to launch</h1>
      <p className={s["su-sub"]}>Confirm your setup. Everything here stays fully editable inside the board.</p>
      <div className={s["su-review"]}>
        <div className={s["su-rev-card"]}>
          <div className={s["rc-h"]}>Project <span className={s.edit} onClick={() => st.suSetStep(0)}>Edit</span></div>
          <div className={s["pc-top"]}>
            <span className={s["pc-ico"]} style={{ width: 34, height: 34, borderRadius: 10, background: setup.color }}>{(setup.name[0] || "B").toUpperCase()}</span>
            <div>
              <div style={{ fontWeight: 700, fontSize: 15 }}>{setup.name}</div>
              <div style={{ fontFamily: "var(--font-mono)", fontSize: 11.5, color: "var(--text-faint)" }}>{setup.key} · tickets like {setup.key}-128</div>
            </div>
          </div>
          {setup.desc && <p style={{ fontSize: 12.5, color: "var(--text-dim)", margin: "11px 0 0", lineHeight: 1.5 }}>{setup.desc}</p>}
        </div>
        <div className={s["su-rev-card"]}>
          <div className={s["rc-h"]}>Workflow · {stages.length} stages <span className={s.edit} onClick={() => st.suSetStep(1)}>Edit</span></div>
          <div className={s["su-rev-grid"]}>
            {stages.map((stg) => (
              <span key={stg.id} className={s.pchip} style={{ fontSize: 11, padding: "4px 9px", borderRadius: 7 }}>
                <span className={s.cd} style={{ background: stg.dot }} />
                {stg.name}{stg.gate ? (stg.gate === "human" ? " 🔒" : " ✓") : ""}
              </span>
            ))}
          </div>
        </div>
        <div className={s["su-rev-card"]}>
          <div className={s["rc-h"]}>Workforce · {setup.agents.length} agents <span className={s.edit} onClick={() => st.suSetStep(2)}>Edit</span></div>
          <div className={s["su-rev-grid"]}>
            {setup.agents.length ? setup.agents.map((k) => {
              const a = st.agents[k];
              return (
                <span key={k} className={s.pchip} style={{ padding: "4px 10px 4px 4px", borderRadius: 20, fontSize: 12 }}>
                  <span className="av" style={{ width: 18, height: 18, borderRadius: 5, background: a.color, color: "#fff", fontSize: 9 }}>{a.ini}</span>
                  {a.name}
                </span>
              );
            }) : <span style={{ color: "var(--text-faint)", fontSize: 12.5 }}>No agents — orchestrator will auto-assign on demand</span>}
          </div>
        </div>
        <div className={s["su-launch"]}>
          <div className={s.li}><b>Launch {setup.name}</b><small>Your agents start picking up work the moment the board opens.</small></div>
          <ArrowRight size={34} style={{ stroke: "#fff" }} />
        </div>
      </div>
    </div>
  );
}

export default function SetupWizard() {
  const step = useStore((st) => st.setup.step);
  const color = useStore((st) => st.setup.color);
  const suNext = useStore((st) => st.suNext);
  const suBack = useStore((st) => st.suBack);

  useEffect(() => {
    applyAccentVars(color);
  }, [color]);

  const scrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = 0;
  }, [step]);

  return (
    <div className={s.setup}>
      <div className={s["su-rail"]}>
        <div className={s["su-brand"]}><Logo /> Scrun</div>
        <div className={s["su-tag"]}>Stand up an autonomous agent board in four quick steps.</div>
        <div className={s["su-steps"]}>
          {STEPS.map(([title, sub], i) => (
            <div key={title} className={s["su-step"] + (i === step ? " " + s.active : "") + (i < step ? " " + s.done : "")}>
              <span className={s.sdot}>{i < step ? <Check size={13} /> : i + 1}</span>
              <span className={s.sx}><b>{title}</b><small>{sub}</small></span>
            </div>
          ))}
        </div>
        <div className={s["su-railfoot"]}>.opsintel/board.db<br />on-prem · 0 telemetry</div>
      </div>
      <div className={s["su-main"]}>
        <div className={s["su-scroll"]} ref={scrollRef}>
          {step === 0 && <ProjectPane />}
          {step === 1 && <WorkflowPane />}
          {step === 2 && <AgentsPane />}
          {step === 3 && <ReviewPane />}
        </div>
        <div className={s["su-foot"]}>
          <span className={s.prog}>Step {step + 1} of 4</span>
          <div className={s.sp}>
            <button className="btn" style={{ visibility: step === 0 ? "hidden" : "visible" }} onClick={suBack}>Back</button>
            <button className="btn btn-primary" onClick={suNext}>
              {step === 3 ? <>Launch board <ArrowRight className="ico" size={16} /></> : <>Continue <ChevronRight className="ico" size={16} /></>}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
