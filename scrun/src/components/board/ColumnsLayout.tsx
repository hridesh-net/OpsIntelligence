import { useState } from "react";
import { useStore } from "../../store";
import { cardVisible } from "../../store/logic";
import type { Stage } from "../../types";
import { Gate, Cog } from "../Icons";
import Card from "./Card";
import c from "./Board.module.css";

function Column({ stage, idx }: { stage: Stage; idx: number }) {
  const st = useStore();
  const cards = st.cards.filter((k) => k.col === stage.id && cardVisible(st, k));
  const [over, setOver] = useState(false);
  const wipOver = !!stage.wip && cards.length >= stage.wip;

  return (
    <div
      className={c.col + (over ? " " + c["drag-over"] : "") + (wipOver ? " " + c["wip-over"] : "")}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        const id = e.dataTransfer.getData("id");
        if (id) st.dropCard(id, stage.id);
      }}
    >
      <div className={c["col-head"]}>
        <span className={c.idx}>0{idx + 1}</span>
        <span className={c.cdot} style={{ background: stage.dot }} />
        <span className={c.ctitle}>{stage.name}</span>
        <span className={c.count}>{cards.length}</span>
        {stage.gate && (
          <span className={c.gate} title={stage.gate === "human" ? "Human approval gate" : "Auto-validate gate"}>
            <Gate />
          </span>
        )}
        <span className={c.wip}>{stage.wip ? `WIP ${cards.length}/${stage.wip}` : ""}</span>
        <span className={c.cog} title="Configure stage" onClick={() => st.go("workflows")}>
          <Cog />
        </span>
      </div>
      <div className={c["col-body"]}>
        {cards.map((k) => (
          <Card key={k.id} k={k} />
        ))}
      </div>
      <div className={c["col-add"]} onClick={() => st.openTaskForm(null, stage.id)}>
        + Add task
      </div>
    </div>
  );
}

export default function ColumnsLayout() {
  const workflow = useStore((s) => s.workflow);
  const density = useStore((s) => s.density);
  const go = useStore((s) => s.go);

  return (
    <div className={c.board} data-density={density}>
      {workflow.map((stage, i) => (
        <Column key={stage.id} stage={stage} idx={i} />
      ))}
      <div className={c["col-ghost"]} onClick={() => go("workflows")}>
        ＋ Add stage
      </div>
    </div>
  );
}
