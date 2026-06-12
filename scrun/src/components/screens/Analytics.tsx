import { useEffect, useMemo, useState } from "react";
import { useStore } from "../../store";
import { getBoardAnalytics, type BoardAnalytics } from "../../api/kanban";
import type { Card } from "../../types";
import s from "./Screens.module.css";
import a from "./Analytics.module.css";

const demoDays = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const demoThroughput = [[6, 2], [9, 3], [7, 4], [11, 3], [13, 4], [5, 2], [8, 3]];

function fmtHours(h: number): string {
  if (h <= 0) return "—";
  if (h < 1) return Math.round(h * 60) + "m";
  return h.toFixed(1) + "h";
}

export default function Analytics() {
  const st = useStore();
  const cards = st.cards;
  const done = cards.filter((k) => k.status === "done");
  const running = cards.filter((k) => k.status === "running");
  const awaiting = cards.filter((k) => k.status === "awaiting");
  const queued = cards.filter((k) => k.status === "queued");

  /* live analytics — fetched from /api/v1/boards/{id}/analytics; demo mode
     keeps the synthetic series below so the screen still demos offline */
  const [live, setLive] = useState<BoardAnalytics | null>(null);
  useEffect(() => {
    if (st.apiMode !== "live" || !st.boardId) return;
    let on = true;
    const load = () =>
      getBoardAnalytics(st.boardId!).then(
        (resp) => { if (on) setLive(resp); },
        () => { /* keep demo fallback on fetch failure */ },
      );
    load();
    const t = window.setInterval(load, 30_000);
    return () => { on = false; window.clearInterval(t); };
  }, [st.apiMode, st.boardId]);

  /* synthetic series — generated once so charts don't jitter on each sim tick */
  const demoSpendPts = useMemo(() => {
    const pts: number[] = [];
    let v = 2.2;
    for (let i = 0; i < 14; i++) {
      v += (Math.random() - 0.35) * 0.6;
      v = Math.max(0.8, v);
      pts.push(v);
    }
    return pts;
  }, []);
  const demoCycleRows = useMemo(
    () =>
      st.workflow.map((stg, i) => ({
        nm: stg.name,
        dot: stg.dot,
        h: [0.4, 1.1, 3.8, 1.6, 2.2, 0][i % 6] + Math.random() * 0.4,
      })),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [st.workflow.length],
  );

  const dotFor = (stageName: string, idx: number) =>
    st.workflow.find((w) => w.name === stageName)?.dot ??
    st.workflow[idx]?.dot ??
    "var(--accent)";

  /* unified view-model: live numbers when present, demo otherwise */
  const throughput = live
    ? live.throughput.map((t) => ({ label: t.day, done: t.shipped, wip: t.started }))
    : demoThroughput.map((d, i) => ({ label: demoDays[i], done: d[0], wip: d[1] }));
  const spendPts = live ? live.spend_trend.map((p) => p.usd) : demoSpendPts;
  const cycleRows = live
    ? live.stage_hours.map((r, i) => ({ nm: r.name, dot: dotFor(r.name, i), h: r.avg_hours }))
    : demoCycleRows;
  const demoSpend = cards.reduce((sum, k) => sum + (k.cost || 0), 0) + 8.4;
  const spendToday = live ? live.kpis.spend_today_usd : demoSpend;

  /* KPIs */
  const kpis = live
    ? [
        { l: "Tasks shipped", v: live.kpis.tasks_shipped, d: `+${live.kpis.shipped_last7} this week`, dir: "up", ic: <path d="m5 12 5 5L20 6" /> },
        { l: "Avg cycle time", v: fmtHours(live.kpis.avg_cycle_hours), d: "completed cards", dir: "flat", ic: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></> },
        { l: "Success rate", v: live.kpis.success_rate > 0 ? Math.round(live.kpis.success_rate) + "%" : "—", d: "of finished runs", dir: "up", ic: <><path d="M12 2v4M12 18v4M2 12h4M18 12h4" /><circle cx="12" cy="12" r="3" /></> },
        { l: "Spend today", v: "$" + spendToday.toFixed(2), d: `$${live.kpis.spend_total_usd.toFixed(2)} all-time`, dir: "flat", ic: <path d="M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" /> },
      ]
    : [
        { l: "Tasks shipped", v: 128 + done.length, d: "+18%", dir: "up", ic: <path d="m5 12 5 5L20 6" /> },
        { l: "Avg cycle time", v: "4.2h", d: "−12%", dir: "up", ic: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></> },
        { l: "Autonomy rate", v: "86%", d: "+5%", dir: "up", ic: <><path d="M12 2v4M12 18v4M2 12h4M18 12h4" /><circle cx="12" cy="12" r="3" /></> },
        { l: "Spend today", v: "$" + spendToday.toFixed(2), d: "on budget", dir: "flat", ic: <path d="M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" /> },
      ];

  /* throughput */
  const tMax = Math.max(...throughput.map((d) => d.done + d.wip), 1);

  /* donut */
  const segs = [
    { nm: "Done", v: done.length, c: "var(--success)" },
    { nm: "Running", v: running.length, c: "var(--accent)" },
    { nm: "Awaiting human", v: awaiting.length, c: "var(--warning)" },
    { nm: "Queued", v: queued.length, c: "var(--text-faint)" },
  ].filter((x) => x.v > 0);
  const total = segs.reduce((sum, x) => sum + x.v, 0) || 1;
  const R = 58;
  const CIRC = 2 * Math.PI * R;
  let off = 0;
  const rings = segs.map((seg, i) => {
    const len = (seg.v / total) * CIRC;
    const node = (
      <circle key={i} cx="70" cy="70" r={R} fill="none" stroke={seg.c} strokeWidth="16" strokeDasharray={`${len.toFixed(2)} ${(CIRC - len).toFixed(2)}`} strokeDashoffset={(-off).toFixed(2)} transform="rotate(-90 70 70)" strokeLinecap="butt" />
    );
    off += len;
    return node;
  });

  /* spend area path */
  const W = 560, H = 150, P = 6;
  const sMax = Math.max(...spendPts, 0.01);
  const x = (i: number) => P + (i * (W - 2 * P)) / Math.max(spendPts.length - 1, 1);
  const y = (v: number) => H - P - (v / sMax) * (H - 2 * P);
  const line = spendPts.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(" ");
  const area = `${line} L${x(spendPts.length - 1).toFixed(1)} ${H - P} L${x(0).toFixed(1)} ${H - P} Z`;

  /* cycle */
  const cMax = Math.max(...cycleRows.map((r) => r.h)) || 1;

  /* leaderboard */
  const agentByName = (nm: string) =>
    Object.values(st.agents).find((ag) => ag.name === nm);
  const lb = live
    ? live.leaderboard.map((r) => {
        const ag = agentByName(r.name);
        return {
          k: r.agent_id,
          ag: {
            name: r.name,
            model: r.model || ag?.model || r.agent_type,
            color: ag?.color || "var(--accent)",
            ini: ag?.ini || r.name.slice(0, 2).toUpperCase(),
          },
          stat: { tasks: r.tasks, success: Math.round(r.success_pct), spend: r.spend_usd },
          util: Math.min(100, Math.round((r.active / 3) * 100)),
        };
      })
    : Object.entries(st.agents)
        .map(([k, ag]) => {
          const stat = st.agentStats[k];
          const active = cards.filter((c: Card) => c.agents.includes(k) && (c.status === "running" || c.status === "awaiting")).length;
          const util = Math.min(100, Math.round((active / (ag.maxParallel || 3)) * 100) + (stat.tasks % 30));
          return { k, ag, stat, util };
        })
        .sort((p, q) => q.stat.tasks - p.stat.tasks);

  return (
    <div className={s.sbody}>
      <div className={s.shead}>
        <div>
          <h1>Analytics</h1>
          <p>How your autonomous workforce is performing — throughput, cycle time, spend and per-agent productivity across {st.boardName}.</p>
        </div>
        <div className={s.right}>
          <button className="btn">Last 7 days ▾</button>
          <button className="btn">Export</button>
        </div>
      </div>

      <div className={a["an-kpis"]}>
        {kpis.map((k) => (
          <div className={a.kpi} key={k.l}>
            <div className={a.kl}>
              <span className={a.ki}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>{k.ic}</svg></span>
              {k.l}
            </div>
            <div className={a.kv}>{k.v}</div>
            <div className={a.kd + " " + a[k.dir]}>
              {k.dir === "up" ? "▲" : k.dir === "down" ? "▼" : "●"} {k.d}
              {!live && <span style={{ color: "var(--text-faint)" }}>vs last week</span>}
            </div>
          </div>
        ))}
      </div>

      <div className={a["an-grid"]}>
        {/* throughput */}
        <div className={a["panel-card"]}>
          <div className={a["pc-h"]}>
            <b>Throughput</b><span className={a.sub}>tasks / day</span>
            <span className={a.leg}><span><i style={{ background: "var(--accent)" }} />Shipped</span><span><i style={{ background: "var(--accent-soft)" }} />In progress</span></span>
          </div>
          <div className={a.barchart}>
            {throughput.map((d, i) => {
              const dn = (d.done / tMax) * 100;
              const wp = (d.wip / tMax) * 100;
              return (
                <div className={a.barcol} key={i}>
                  <div className={a.bv}>{d.done + d.wip}</div>
                  <div className={a.stack} style={{ height: dn + wp + "%" }}>
                    <i className={a.done} style={{ height: dn + wp > 0 ? (dn / (dn + wp)) * 100 + "%" : "0%" }} />
                    <i className={a.wip} style={{ height: dn + wp > 0 ? (wp / (dn + wp)) * 100 + "%" : "0%" }} />
                  </div>
                  <div className={a.bx}>{d.label}</div>
                </div>
              );
            })}
          </div>
        </div>

        {/* donut */}
        <div className={a["panel-card"]}>
          <div className={a["pc-h"]}><b>Work distribution</b><span className={a.sub}>{total} active</span></div>
          <div className={a["donut-wrap"]}>
            <div className={a.donut}>
              <svg width="140" height="140" viewBox="0 0 140 140">
                <circle cx="70" cy="70" r="58" fill="none" stroke="var(--surface-2)" strokeWidth="16" />
                {rings}
              </svg>
              <div className={a.dc}><b>{running.length + awaiting.length}</b><small>in flight</small></div>
            </div>
            <div className={a["donut-legend"]}>
              {segs.map((seg) => (
                <div className={a.dl} key={seg.nm}><span className={a.sw} style={{ background: seg.c }} /><span className={a.nm}>{seg.nm}</span><span className={a.vv}>{seg.v}</span></div>
              ))}
            </div>
          </div>
        </div>
      </div>

      <div className={a["an-grid"]}>
        {/* spend area */}
        <div className={a["panel-card"]}>
          <div className={a["pc-h"]}><b>Token spend trend</b><span className={a.sub}>14-day · ${spendToday.toFixed(2)} today</span></div>
          <svg className={a["area-chart"]} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
            <defs>
              <linearGradient id="spendg" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0" stopColor="var(--accent)" stopOpacity=".34" />
                <stop offset="1" stopColor="var(--accent)" stopOpacity="0" />
              </linearGradient>
            </defs>
            <path d={area} fill="url(#spendg)" />
            <path d={line} fill="none" stroke="var(--accent)" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
            <circle cx={x(spendPts.length - 1).toFixed(1)} cy={y(spendPts[spendPts.length - 1] || 0).toFixed(1)} r="4" fill="var(--accent-bright)" stroke="var(--surface)" strokeWidth="2" />
          </svg>
          <div className={a["spend-foot"]}><span>14 days ago</span><span>today</span></div>
        </div>

        {/* cycle */}
        <div className={a["panel-card"]}>
          <div className={a["pc-h"]}><b>Avg time in stage</b><span className={a.sub}>hours</span></div>
          <div className={a.cyclelist}>
            {cycleRows.map((r, i) => (
              <div className={a.cyclerow} key={i}>
                <div className={a.cn}><span className={a.cd} style={{ background: r.dot }} />{r.nm}</div>
                <div className={a.ctrack}><i style={{ width: ((r.h / cMax) * 100).toFixed(0) + "%", background: r.dot }} /></div>
                <div className={a.cval}>{fmtHours(r.h)}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* leaderboard */}
      <div className={a["panel-card"]}>
        <div className={a["pc-h"]}><b>Agent leaderboard</b><span className={a.sub}>all-time</span></div>
        <table className={a.lb}>
          <thead>
            <tr><th>Agent</th><th className={a.r}>Tasks</th><th className={a.r}>Success</th><th className={a.r}>Spend</th><th className={a.r}>Utilisation</th></tr>
          </thead>
          <tbody>
            {lb.map((r) => (
              <tr key={r.k}>
                <td>
                  <div className={a.ag}>
                    <span className="av" style={{ background: r.ag.color, width: 24, height: 24, borderRadius: 7 }}>{r.ag.ini}</span>
                    <div><b>{r.ag.name}</b><small>{r.ag.model}</small></div>
                  </div>
                </td>
                <td className={a.r + " " + a.mono}>{r.stat.tasks}</td>
                <td className={a.r + " " + a.succ}>{r.stat.success}%</td>
                <td className={a.r + " " + a.mono}>${r.stat.spend.toFixed(0)}</td>
                <td className={a.r}>
                  <div className={a.util}><div className={a.ut}><i style={{ width: r.util + "%" }} /></div><span className={a.mono} style={{ width: 34 }}>{r.util}%</span></div>
                </td>
              </tr>
            ))}
            {lb.length === 0 && (
              <tr><td colSpan={5} style={{ color: "var(--text-faint)", padding: "12px 0" }}>No agent runs recorded yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
