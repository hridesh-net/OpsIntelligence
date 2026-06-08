import { useState } from "react";
import { useStore } from "../../store";
import type { Card as CardT, LogLine } from "../../types";
import Avatar from "../Avatar";
import { Warn } from "../Icons";
import { STATUS_LABEL, TYPE_LABEL } from "../../lib/helpers";
import c from "./Board.module.css";

const PRE: Record<string, string> = { ok: "✓", wr: "!", ac: "›", "": "·" };

function MiniTerminal({ logs }: { logs: LogLine[] }) {
  const last = logs.slice(-3);
  return (
    <div className={c.terminal}>
      {last.map((l, i) => (
        <div className={c.tline} key={i}>
          <span className={c.ts}>{l.t}</span>{" "}
          <span className={c[l.k] ?? ""}>
            {PRE[l.k]} {l.x}
          </span>
        </div>
      ))}
      <div className={c.tline}>
        <span className={c.ts}>{last.length ? "" : "awaiting stream"}</span>
        <span className={c.cursor} />
      </div>
    </div>
  );
}

export default function Card({ k }: { k: CardT }) {
  const agentsMap = useStore((s) => s.agents);
  const selectedId = useStore((s) => s.selectedId);
  const flashId = useStore((s) => s.flashId);
  const openPanel = useStore((s) => s.openPanel);
  const resolveCardHitl = useStore((s) => s.resolveCardHitl);
  const setDragging = useStore((s) => s.setDragging);
  const [drag, setDrag] = useState(false);

  const a0 = agentsMap[k.agents[0]];
  const cls = [
    c.card,
    c.lvl,
    c["lvl" + k.prio],
    selectedId === k.id ? c.sel : "",
    flashId === k.id ? c["just-moved"] : "",
    drag ? c.dragging : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={cls}
      draggable
      onClick={() => openPanel(k.id)}
      onDragStart={(e) => {
        e.dataTransfer.setData("id", k.id);
        setDrag(true);
        setDragging(true);
      }}
      onDragEnd={() => {
        setDrag(false);
        setDragging(false);
      }}
    >
      <div className={c.crow}>
        <span className={"ttag " + k.type}>{TYPE_LABEL[k.type] || k.type}</span>
        <span className="cid">#{k.id}</span>
        <span className={"prio " + k.prio}>{k.prio}</span>
      </div>
      <div className={c.ctitle2}>{k.title}</div>
      <div className={c.agents}>
        {k.agents.length === 1 ? (
          <>
            <Avatar color={a0.color} ini={a0.ini} name={a0.name} />
            <span className="aname">{a0.name}</span>
          </>
        ) : (
          <>
            {k.agents.map((ak, i) => {
              const a = agentsMap[ak];
              return <Avatar key={ak} color={a.color} ini={a.ini} name={a.name} stack={i > 0} />;
            })}
            <span className="aname">{k.agents.length} agents</span>
          </>
        )}
      </div>
      <div className={c.statusrow}>
        <span className={"pill " + k.status}>
          <span className="pd" />
          {STATUS_LABEL[k.status]}
        </span>
        <span className="model">{a0.model}</span>
      </div>

      {k.status === "running" && k.progress != null && (
        <div className={c.bar}>
          <i style={{ width: k.progress + "%" }} />
        </div>
      )}
      {k.status === "running" && k.logs && k.logs.length > 0 && <MiniTerminal logs={k.logs} />}
      {k.tests && (
        <div className={c.codeline}>
          <span className={c.act + " " + c.test}>TESTS</span>
          <span className={c.path}>{k.tests}</span>
        </div>
      )}
      {k.hitl && (
        <div className={c.hitl}>
          <div className={c.q}>
            <Warn />
            {k.hitl.q}
          </div>
          {k.hitl.opts.map((o, i) => (
            <div
              key={i}
              className={c.opt + (i === 0 ? " " + c.pri : "")}
              onClick={(e) => {
                e.stopPropagation();
                resolveCardHitl(k.id, i);
              }}
            >
              <span className={c.n}>{i + 1}</span>
              {o}
            </div>
          ))}
        </div>
      )}

      {k.branch ? (
        <div className={c.cfoot}>
          <span className={c.branch}>⎇ {k.branch}</span>
          {k.add != null && (
            <>
              <span className={c.add}>+{k.add}</span>
              <span className={c.del}>-{k.del}</span>
            </>
          )}
          {k.cost != null && <span className={c.cost}>${k.cost.toFixed(2)}</span>}
          <span className={c.when}>{k.when}</span>
        </div>
      ) : (
        <div className={c.cfoot}>
          <span className={c.branch}>no branch</span>
          <span className={c.when}>{k.when}</span>
        </div>
      )}
    </div>
  );
}
