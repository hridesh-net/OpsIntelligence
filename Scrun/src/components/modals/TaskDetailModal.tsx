import { useStore } from "../../store";
import { stageOf } from "../../store/logic";
import type { LogLine, PanelTab } from "../../types";
import Avatar from "../Avatar";
import { Check, Close, Edit, Warn } from "../Icons";
import { STATUS_LABEL, PRIO_NAME } from "../../lib/helpers";
import Modal from "./Modal";
import m from "./Modal.module.css";

const TABS: PanelTab[] = ["details", "conversation", "artifacts", "timeline", "metrics"];
const PRE: Record<string, string> = { ok: "✓", wr: "!", ac: "›", "": "·" };

function PanelTerm({ logs }: { logs: LogLine[] }) {
  if (!logs.length)
    return (
      <div className={m["p-term"]}>
        <div className="tline"><span className={m.ts}>— no stream yet —</span></div>
      </div>
    );
  return (
    <div className={m["p-term"]}>
      {logs.map((l, i) => (
        <div className="tline" key={i}>
          <span className={m.ts}>{l.t}</span>{" "}
          <span className={l.k ? m[l.k] : ""}>{PRE[l.k]} {l.x}</span>
        </div>
      ))}
    </div>
  );
}

export default function TaskDetailModal() {
  const st = useStore();
  const k = st.cards.find((x) => x.id === st.selectedId);
  if (!k) return null;
  const tab = st.panelTab;
  const a0 = st.agents[k.agents[0]];

  const statusPill = (
    <span className={"pill " + k.status}><span className="pd" />{STATUS_LABEL[k.status]}</span>
  );

  const agentsSec = (
    <div className={m["p-sec"]}>
      <div className={m["p-lbl"]}>Assigned agents</div>
      <div className={m["p-agents"]}>
        {k.agents.map((ak) => {
          const a = st.agents[ak];
          return (
            <div className={m["p-agent"]} key={ak}>
              <Avatar color={a.color} ini={a.ini} name={a.name} />
              <span className="aname">{a.name}</span>
            </div>
          );
        })}
        <div className={m["p-agent"]} style={{ cursor: "pointer" }}>
          <span className="av" style={{ background: "var(--surface)", color: "var(--text-dim)" }}>+</span>
          <span className="aname">Assign</span>
        </div>
      </div>
    </div>
  );

  const metricsSec = (
    <div className={m["p-sec"]}>
      <div className={m["p-lbl"]}>Run metrics</div>
      <div className={m.metrics}>
        <div className={m.metric}><div className={m.ml}>Cost</div><div className={m.mv}>{k.cost != null ? "$" + k.cost.toFixed(2) : "—"}</div></div>
        <div className={m.metric}><div className={m.ml}>Tokens</div><div className={m.mv}>{k.tokens ? (k.tokens / 1000).toFixed(1) + "K" : "—"}</div></div>
        <div className={m.metric}><div className={m.ml}>Duration</div><div className={m.mv}>{k.duration || "—"}</div></div>
        <div className={m.metric}><div className={m.ml}>Confidence</div><div className={m.mv + " " + m.ok}>{k.conf ? k.conf + "%" : "—"}</div></div>
      </div>
    </div>
  );

  const propsSec = (
    <div className={m["p-sec"]}>
      <div className={m["p-lbl"]}>Properties</div>
      <div className={m.kv}><span className={m.k}>Priority</span><span className={m.v}>{PRIO_NAME[k.prio]}</span></div>
      <div className={m.kv}><span className={m.k}>Workflow stage</span><span className={m.v}>{stageOf(st, k.col)?.name}</span></div>
      <div className={m.kv}><span className={m.k}>Branch</span><span className={m.v}>{k.branch || "—"}</span></div>
      <div className={m.kv}><span className={m.k}>Lead model</span><span className={m.v}>{a0.model}</span></div>
    </div>
  );

  const labelsSec = (
    <div className={m["p-sec"]}>
      <div className={m["p-lbl"]}>Labels</div>
      <div className={m.labels}>
        {(k.labels || []).length ? k.labels.map((l) => <span className="lab" key={l}>{l}</span>) : <span className="lab">none</span>}
      </div>
    </div>
  );

  let content: React.ReactNode = null;
  if (tab === "details") {
    content = (
      <div className={m["p-grid"]}>
        <div className={m["pg-col"]}>
          <div className={m["p-sec"]}>
            <div className={m["p-lbl"]}>Description</div>
            <div className={m["p-desc"]}>{k.desc || <span style={{ color: "var(--text-faint)" }}>No description yet.</span>}</div>
          </div>
          {k.ac && k.ac.length > 0 && (
            <div className={m["p-sec"]}>
              <div className={m["p-lbl"]}>Acceptance criteria</div>
              <div className={m["ac-read"]}>
                {k.ac.map((a, i) => {
                  const met = k.status === "done" || (k.progress || 0) >= ((i + 1) / k.ac!.length) * 100;
                  return (
                    <div className={m.acr + (met ? " " + m.met : "")} key={i}>
                      <span className={m.b}>{met && <Check size={11} />}</span>
                      <span className={m.t}>{a}</span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
          {k.hitl && (
            <div className={m["p-sec"]}>
              <div className={m["p-lbl"]}>Human decision required</div>
              <div className={m.hitl}>
                <div className={m.q}><Warn />{k.hitl.q}</div>
                {k.hitl.opts.map((o, i) => (
                  <div className={m.opt + (i === 0 ? " " + m.pri : "")} key={i} onClick={() => st.resolveCardHitl(k.id, i)}>
                    <span className={m.n}>{i + 1}</span>{o}
                  </div>
                ))}
              </div>
            </div>
          )}
          <div className={m["p-sec"]}>
            <div className={m["p-lbl"]}>Progress</div>
            <div className={m["p-prog-row"]}>
              <span>{k.status === "done" ? "Complete" : k.status === "queued" ? "Queued" : k.status === "awaiting" ? "Awaiting human" : "Working"}</span>
              <span>{k.progress ?? 0}% · ETA {k.eta || "—"}</span>
            </div>
            <div className={m["p-bar"]}><i style={{ width: (k.progress ?? 0) + "%" }} /></div>
          </div>
          <div className={m["p-sec"]}>
            <div className={m["p-lbl"]}>Run log</div>
            <PanelTerm logs={(k.logs || []).slice(-6)} />
          </div>
        </div>
        <div className={m["pg-col"]}>{agentsSec}{metricsSec}{propsSec}{labelsSec}</div>
      </div>
    );
  } else if (tab === "conversation") {
    const rev = st.agents.review;
    content = (
      <div className={m["p-sec"]}>
        <div className={m.chat}>
          <div className={m.msg}>
            <Avatar color={rev.color} ini={rev.ini} />
            <div><div className={m.mname}>Orchestrator</div><div className={m.mbody}>Picked up <b>#{k.id}</b> and routed it to {a0.name} based on capability match ({a0.caps.slice(0, 2).join(", ")}).</div></div>
          </div>
          <div className={m.msg}>
            <Avatar color={a0.color} ini={a0.ini} />
            <div><div className={m.mname}>{a0.name}</div><div className={m.mbody}>{k.desc}</div></div>
          </div>
          <div className={m.msg}>
            <Avatar color={a0.color} ini={a0.ini} />
            <div><div className={m.mname}>{a0.name}</div><div className={m.mbody}>{k.status === "awaiting" ? "I need a human decision before I can safely proceed — see the gate on the details tab." : k.status === "done" ? "Task complete and merged. Closing out." : "Working through the plan now — streaming progress to the run log."}</div></div>
          </div>
        </div>
      </div>
    );
  } else if (tab === "artifacts") {
    content = (
      <div className={m["p-grid"]}>
        <div className={m["pg-col"]}>
          <div className={m["p-sec"]}><div className={m["p-lbl"]}>Run log</div><PanelTerm logs={k.logs || []} /></div>
        </div>
        <div className={m["pg-col"]}>
          <div className={m["p-sec"]}>
            <div className={m["p-lbl"]}>Artifacts</div>
            {k.branch ? (
              <>
                <div className={m.kv}><span className={m.k}>Branch</span><span className={m.v}>{k.branch}</span></div>
                <div className={m.kv}><span className={m.k}>Diff</span><span className={m.v}><span style={{ color: "var(--success)" }}>+{k.add ?? 0}</span> / <span style={{ color: "var(--danger)" }}>-{k.del ?? 0}</span></span></div>
                <div className={m.kv}><span className={m.k}>Pull request</span><span className={m.v}>{k.status === "done" ? "#" + (1200 + Number(k.id.slice(3))) + " merged" : "draft"}</span></div>
                {k.tests && <div className={m.kv}><span className={m.k}>Tests</span><span className={m.v} style={{ color: "var(--success)" }}>{k.tests}</span></div>}
              </>
            ) : (
              <div className={m["p-desc"]} style={{ color: "var(--text-faint)" }}>No artifacts produced yet.</div>
            )}
          </div>
        </div>
      </div>
    );
  } else if (tab === "timeline") {
    content = (
      <div className={m["p-sec"]}>
        <div className={m.timeline}>
          <div className={m.tl}><div className={m.tt}>Task created</div><div className={m.ts}>Queued into {stageOf(st, k.col)?.name}</div><div className={m.tm}>{k.when} ago</div></div>
          <div className={m.tl}><div className={m.tt}>{a0.name} assigned</div><div className={m.ts}>Routed by orchestrator via capability match</div><div className={m.tm}>{k.when} ago</div></div>
          {k.branch && <div className={m.tl}><div className={m.tt}>Branch {k.branch}</div><div className={m.ts}>{k.add != null ? `+${k.add} / -${k.del} lines` : "working tree initialised"}</div><div className={m.tm}>{k.duration || "—"} elapsed</div></div>}
          {k.tests && <div className={m.tl + " " + m.ok}><div className={m.tt}>Tests passed</div><div className={m.ts}>{k.tests}</div><div className={m.tm}>—</div></div>}
          {k.hitl && <div className={m.tl + " " + m.warn}><div className={m.tt}>Awaiting human decision</div><div className={m.ts}>{k.hitl.q}</div><div className={m.tm}>now</div></div>}
          {k.status === "done" && <div className={m.tl + " " + m.ok}><div className={m.tt}>Merged &amp; closed</div><div className={m.ts}>Deployed via CI/CD pipeline</div><div className={m.tm}>{k.when} ago</div></div>}
        </div>
      </div>
    );
  } else {
    content = (
      <div className={m["p-sec"]}>
        <div className={m["p-lbl"]}>Run metrics</div>
        <div className={m.metrics} style={{ gridTemplateColumns: "repeat(3,1fr)" }}>
          <div className={m.metric}><div className={m.ml}>Token spend</div><div className={m.mv}>{k.cost != null ? "$" + k.cost.toFixed(2) : "—"}</div></div>
          <div className={m.metric}><div className={m.ml}>Total tokens</div><div className={m.mv}>{k.tokens ? (k.tokens / 1000).toFixed(1) + "K" : "—"}</div></div>
          <div className={m.metric}><div className={m.ml}>Wall time</div><div className={m.mv}>{k.duration || "—"}</div></div>
          <div className={m.metric}><div className={m.ml}>Confidence</div><div className={m.mv + " " + m.ok}>{k.conf ? k.conf + "%" : "—"}</div></div>
          <div className={m.metric}><div className={m.ml}>Lines +</div><div className={m.mv + " " + m.ok}>{k.add != null ? "+" + k.add : "—"}</div></div>
          <div className={m.metric}><div className={m.ml}>Lines −</div><div className={m.mv}>{k.del != null ? "-" + k.del : "—"}</div></div>
        </div>
      </div>
    );
  }

  const actions = k.hitl ? (
    <>
      <button className="btn" onClick={() => st.panelAction("reject")}>Reject</button>
      <button className="btn btn-primary" onClick={() => st.panelAction("approve")}>Approve &amp; continue</button>
    </>
  ) : k.status === "done" ? (
    <>
      <button className="btn" onClick={() => st.panelAction("reopen")}>Reopen</button>
      <button className="btn btn-primary" onClick={() => st.panelAction("view")}>Open full view</button>
    </>
  ) : (
    <>
      <button className="btn" onClick={() => st.panelAction("reassign")}>Reassign</button>
      <button className="btn btn-primary" onClick={() => st.panelAction("advance")}>Advance stage</button>
    </>
  );

  return (
    <Modal onClose={st.closePanel}>
      <div className={m["p-head"]}>
        <div className={m["p-top"]}>
          <span className="cid">#{k.id}</span>
          {statusPill}
          <span className="model" style={{ marginLeft: 0 }}>{a0.model}</span>
          <span className={m["p-edit"]} title="Edit ticket" onClick={() => { st.closePanel(); st.openTaskForm(k.id); }}>
            <Edit />Edit
          </span>
          <span className={m["p-close"]} onClick={st.closePanel}><Close /></span>
        </div>
        <div className={m["p-title"]}>{k.title}</div>
        <div className={m["p-tabs"]}>
          {TABS.map((t) => (
            <div key={t} className={m["p-tab"] + (t === tab ? " " + m.on : "")} onClick={() => st.setPanelTab(t)}>
              {t[0].toUpperCase() + t.slice(1)}
            </div>
          ))}
        </div>
      </div>
      <div className={m["p-body"]}>{content}</div>
      <div className={m["p-actions"]}>{actions}</div>
    </Modal>
  );
}
