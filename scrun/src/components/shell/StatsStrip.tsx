import { useStore } from "../../store";
import { cardVisible, totalSpend, anyFilter } from "../../store/logic";
import s from "./StatsStrip.module.css";

export default function StatsStrip() {
  const st = useStore();
  const vis = st.cards.filter((k) => cardVisible(st, k));
  const agents = new Set<string>();
  let run = 0;
  let aw = 0;
  vis.forEach((k) => {
    if (k.status === "running") {
      k.agents.forEach((a) => agents.add(a));
      run++;
    }
    if (k.status === "awaiting") aw++;
  });
  const filtered = anyFilter(st);

  return (
    <div className={s.strip}>
      <span className={s.crumb}>
        {st.boardName} / <b>Board</b>
      </span>
      <span className={s.stat}>
        <span className="pulse" />
        <b>{agents.size}</b> agents working
      </span>
      <span className={s.stat}>
        <b>{run}</b> running
      </span>
      <span className={s.stat}>
        <b>{aw}</b> awaiting human
      </span>
      <span className={s.stat}>
        spend today <b>${totalSpend(st).toFixed(2)}</b>
      </span>
      <div className={s.right}>
        {filtered && (
          <span className={s.stat}>
            <span className={s.filtActive} onClick={st.clearFilters}>
              Clear filters ✕
            </span>
          </span>
        )}
        <span
          className={s.simToggle + (st.simRunning ? "" : " " + s.paused)}
          title="Pause / resume live simulation"
          onClick={st.toggleSim}
        >
          <span className={s.sdot} />
          {st.simRunning ? "Live" : "Paused"}
        </span>
        <span className={s.stat + " " + s.faint}>.opsintel/board.db · on-prem · 0 telemetry</span>
      </div>
    </div>
  );
}
