import { useStore } from "../../store";
import { totalSpend } from "../../store/logic";
import Avatar from "../Avatar";
import RichText from "../RichText";
import s from "./Screens.module.css";

export default function Activity() {
  const st = useStore();
  const items = st.activity.slice(0, 40);
  const running = st.cards.filter((k) => k.status === "running");
  const activeAgents = new Set(st.cards.flatMap((k) => (k.status === "running" ? k.agents : [])));

  return (
    <div className={s["activity-host"]}>
      <div className={s["feed-main"]}>
        <div className={s["feed-day"]}>Today · live</div>
        <div className={s["feed-list"]}>
          {items.length === 0 && <div className="p-desc" style={{ color: "var(--text-faint)" }}>No activity yet — the agents are warming up.</div>}
          {items.map((e, i) => {
            const a = st.agents[e.agent];
            if (!a) return null;
            return (
              <div className={s.fitem} key={e.time + e.id + i}>
                <span className={s.fdot} style={{ background: a.color }}>{a.ini}</span>
                <div className={s.ftop}>
                  <span className={s.fname}>{a.name}</span>
                  <span className={s.ftag + " " + s[e.tag]}>{e.tag}</span>
                  <span className={s.ftime}>{e.time}</span>
                </div>
                <div className={s.fbody}>
                  <span className="cid" onClick={() => st.openPanel(e.id)}>#{e.id}</span> <RichText text={e.text} />
                </div>
                {e.meta && <div className={s.fmeta}>{e.meta}</div>}
              </div>
            );
          })}
        </div>
      </div>
      <aside className={s["feed-side"]}>
        <h3>Live summary</h3>
        <div className={s["sb-stat"]}><span className={s.l}>Agents active</span><span className={s.v}>{activeAgents.size}</span></div>
        <div className={s["sb-stat"]}><span className={s.l}>Tasks running</span><span className={s.v}>{running.length}</span></div>
        <div className={s["sb-stat"]}><span className={s.l}>Awaiting human</span><span className={s.v} style={{ color: "var(--warning)" }}>{st.cards.filter((k) => k.status === "awaiting").length}</span></div>
        <div className={s["sb-stat"]}><span className={s.l}>Spend today</span><span className={s.v}>${totalSpend(st).toFixed(2)}</span></div>
        <h3 style={{ marginTop: 22 }}>In flight</h3>
        {running.length === 0 && <div className="p-desc" style={{ color: "var(--text-faint)", fontSize: 12 }}>Nothing running</div>}
        {running.map((k) => {
          const a = st.agents[k.agents[0]];
          if (!a) return null;
          return (
            <div className={s["mini-agent"]} key={k.id}>
              <Avatar color={a.color} ini={a.ini} name={a.name} />
              <span className={s.mname}>#{k.id}</span>
              <span className={s.mbar}><i style={{ width: (k.progress || 0) + "%" }} /></span>
              <span className={s.mpct}>{k.progress || 0}%</span>
            </div>
          );
        })}
      </aside>
    </div>
  );
}
