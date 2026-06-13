import { Fragment } from "react";
import { useStore } from "../../store";
import { cardVisible } from "../../store/logic";
import Avatar from "../Avatar";
import { STATUS_LABEL, TYPE_LABEL } from "../../lib/helpers";
import c from "./Board.module.css";

export default function CompactLayout() {
  const st = useStore();

  return (
    <div className={c.compact}>
      <table className={c.ctbl}>
        <thead>
          <tr>
            <th>Task</th>
            <th>Agents</th>
            <th>Status</th>
            <th>Progress</th>
            <th>Pri</th>
            <th>Cost</th>
          </tr>
        </thead>
        <tbody>
          {st.workflow.map((stage) => {
            const cards = st.cards.filter((k) => k.col === stage.id && cardVisible(st, k));
            if (!cards.length) return null;
            return (
              <Fragment key={stage.id}>
                <tr className={c.grp}>
                  <td colSpan={6}>
                    <span className={c.cdot} style={{ background: stage.dot }} />
                    {stage.name} · {cards.length}
                  </td>
                </tr>
                {cards.map((k) => (
                  <tr
                    key={k.id}
                    className={c.row + (st.selectedId === k.id ? " " + c.sel : "")}
                    onClick={() => st.openPanel(k.id)}
                  >
                    <td>
                      <div className={c["c-task"]}>
                        <span className={"ttag " + k.type}>{TYPE_LABEL[k.type] || k.type}</span>
                        <span className="cid">#{k.id}</span>
                        <span>{k.title}</span>
                      </div>
                    </td>
                    <td>
                      <div className={c.agents} style={{ margin: 0 }}>
                        {k.agents.map((ak, i) => {
                          const a = st.agents[ak];
                          if (!a) return null;
                          return <Avatar key={ak} color={a.color} ini={a.ini} name={a.name} stack={i > 0} />;
                        })}
                      </div>
                    </td>
                    <td>
                      <span className={"pill " + k.status}>
                        <span className="pd" />
                        {STATUS_LABEL[k.status]}
                      </span>
                    </td>
                    <td>
                      <span className={c.minibar}>
                        <i style={{ width: (k.progress || 0) + "%" }} />
                      </span>
                    </td>
                    <td>
                      <span className={"prio " + k.prio} style={{ display: "inline-grid" }}>
                        {k.prio}
                      </span>
                    </td>
                    <td className={c.cost}>{k.cost != null ? "$" + k.cost.toFixed(2) : "—"}</td>
                  </tr>
                ))}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
