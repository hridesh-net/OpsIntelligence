import { useState } from "react";
import { useStore } from "../../store";
import { cardVisible } from "../../store/logic";
import type { AgentKey } from "../../types";
import Avatar from "../Avatar";
import Card from "./Card";
import c from "./Board.module.css";

function LaneCol({ agentKey, colId, dot, name }: { agentKey: AgentKey; colId: string; dot: string; name: string }) {
  const st = useStore();
  const [over, setOver] = useState(false);
  const cards = st.cards.filter((k) => k.agents.includes(agentKey) && cardVisible(st, k) && k.col === colId);
  return (
    <div
      className={c["lane-col"] + (over ? " " + c["drag-over"] : "")}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        const id = e.dataTransfer.getData("id");
        if (id) st.dropCard(id, colId);
      }}
    >
      <div className={c["lc-head"]}>
        <span className={c.cdot} style={{ background: dot }} />
        {name}
      </div>
      <div className={c["lc-body"]}>
        {cards.map((k) => (
          <Card key={k.id} k={k} />
        ))}
      </div>
    </div>
  );
}

export default function LanesLayout() {
  const st = useStore();
  const activeAgents = Object.keys(st.agents).filter((ak) =>
    st.cards.some((k) => k.agents.includes(ak) && cardVisible(st, k)),
  );

  return (
    <div className={c.lanes}>
      {activeAgents.map((ak) => {
        const a = st.agents[ak];
        const mine = st.cards.filter((k) => k.agents.includes(ak) && cardVisible(st, k));
        const running = mine.filter((k) => k.status === "running").length;
        const spend = mine.reduce((sum, k) => sum + (k.cost || 0), 0);
        return (
          <div className={c.lane} key={ak}>
            <div className={c["lane-head"]}>
              <Avatar color={a.color} ini={a.ini} name={a.name} style={{ width: 24, height: 24 }} />
              <span className={c.lname}>{a.name}</span>
              <span className={c.lmeta}>{a.model}</span>
              <span className={c.lspark}>
                <span>
                  <b>{mine.length}</b> tasks
                </span>
                <span>
                  <b>{running}</b> running
                </span>
                <span>
                  <b>${spend.toFixed(2)}</b> today
                </span>
              </span>
            </div>
            <div className={c["lane-cols"]}>
              {st.workflow.map((stage) => (
                <LaneCol key={stage.id} agentKey={ak} colId={stage.id} dot={stage.dot} name={stage.name} />
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}
