import { useStore } from "../../store";
import RichText from "../RichText";
import { Activity, Plus } from "../Icons";
import s from "./LiveRail.module.css";

export default function LiveRail() {
  const activity = useStore((st) => st.activity);
  const agents = useStore((st) => st.agents);
  const openPanel = useStore((st) => st.openPanel);
  const openTaskForm = useStore((st) => st.openTaskForm);
  const items = activity.slice(0, 14);

  return (
    <aside className={s["live-rail"]}>
      <div className={s["lr-head"]}>
        <Activity size={15} />
        <b>Agent Activity</b>
        <span className={s.live}>
          <span className="pulse" />
          Live
        </span>
      </div>
      <div className={s["lr-body"]}>
        {items.length === 0 && <div className={s.empty}>Waiting for agent activity…</div>}
        {items.map((e, i) => {
          const a = agents[e.agent];
          if (!a) return null;
          return (
            <div className={s["lr-item"]} key={e.time + e.id + i}>
              <span className={s.lav} style={{ background: a.color }}>
                {a.ini}
              </span>
              <div className={s.lc}>
                <div className={s.ln}>
                  {a.name}
                  <span className={s.ftime}>{e.time}</span>
                </div>
                <div className={s.lt}>
                  <span className="cid" onClick={() => openPanel(e.id)}>
                    #{e.id}
                  </span>{" "}
                  <RichText text={e.text} />
                </div>
              </div>
            </div>
          );
        })}
      </div>
      <div className={s["lr-foot"]}>
        <button className="btn" onClick={() => openTaskForm(null, "backlog")}>
          <Plus className="ico" />
          Queue a task
        </button>
      </div>
    </aside>
  );
}
