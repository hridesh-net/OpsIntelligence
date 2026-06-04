import { useStore } from "../../store";
import s from "./Screens.module.css";

const AUTONOMY_LABEL: Record<string, string> = { supervised: "supervised", auto: "auto-validate", full: "full auto" };

export default function Agents() {
  const st = useStore();

  return (
    <div className={s.sbody}>
      <div className={s.shead}>
        <div>
          <h1>Agent Manager</h1>
          <p>Connect, configure and monitor the autonomous agents in your workforce. Each agent carries its own model, capabilities, budget and guardrails.</p>
        </div>
        <div className={s.right}>
          <button className="btn">Model registry</button>
          <button className="btn btn-primary">Connect agent</button>
        </div>
      </div>

      <div className={s.agrid}>
        {Object.entries(st.agents).map(([ak, a]) => {
          const cur = st.cards.find((k) => k.agents.includes(ak) && (k.status === "running" || k.status === "awaiting"));
          const stat = st.agentStats[ak];
          const state = cur ? "working" : stat.tasks % 7 === 0 ? "offline" : "idle";
          const busy = !!cur;
          return (
            <div className={s.acard + (busy ? " " + s.busy : "")} key={ak}>
              <div className={s["acard-top"]}>
                <span className={s.aav} style={{ background: a.color }}>{a.ini}</span>
                <div className={s.ainfo}>
                  <b>{a.name}</b>
                  <span className={s.amodel}>{a.role}</span>
                </div>
                <span className={s.astate + " " + s[state]}>
                  <span className={s.d} />
                  {state === "working" ? "Working" : state === "idle" ? "Idle" : "Offline"}
                </span>
              </div>
              <div className={s.acur + (cur ? "" : " " + s["idle-task"])}>
                {cur ? (<><span className="cid">#{cur.id}</span> {cur.title}</>) : "Idle — waiting for the orchestrator to route work"}
              </div>
              <div className={s.acap}>
                {a.caps.map((c) => <span className={s.cap} key={c}>{c}</span>)}
              </div>
              <div className={s.acap} style={{ marginTop: -4 }}>
                <span className={s.cap} style={{ color: "var(--text-dim)" }}>{a.model}</span>
                <span className={s.cap} style={{ color: "var(--text-dim)" }} title="Memory">⌬ {a.memory.mode} · {a.memory.contextK}K</span>
                <span className={s.cap} style={{ color: "var(--text-dim)" }} title="Autonomy">{AUTONOMY_LABEL[a.autonomy]}</span>
              </div>
              <div className={s.astats}>
                <div className={s.as}><div className={s.v}>{stat.tasks}</div><div className={s.l}>Tasks</div></div>
                <div className={s.as}><div className={s.v} style={{ color: "var(--success)" }}>{stat.success}%</div><div className={s.l}>Success</div></div>
                <div className={s.as}><div className={s.v}>${stat.spend.toFixed(0)}</div><div className={s.l}>Spend</div></div>
              </div>
              <div className={s["acard-foot"]}>
                <button className="btn" onClick={() => st.openAgentConfig(ak)}>Configure</button>
                <button className={"btn" + (state === "offline" ? " btn-primary" : "")} onClick={() => st.showToast((state === "offline" ? "Connect" : "Pause") + "d " + a.name)}>
                  {state === "offline" ? "Connect" : "Pause"}
                </button>
              </div>
            </div>
          );
        })}
        <div className={s.acard + " " + s["add-agent"]} onClick={() => st.showToast("Connect a new agent endpoint")}>
          <div>
            <div className={s.plus}>+</div>
            <b style={{ color: "var(--text)" }}>Connect agent</b>
            <small style={{ display: "block", color: "var(--text-faint)", fontSize: 11, marginTop: 4, fontFamily: "var(--font-mono)" }}>OpenAI · Anthropic · Meta · custom</small>
          </div>
        </div>
      </div>
    </div>
  );
}
