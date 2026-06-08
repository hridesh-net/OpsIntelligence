import { useStore } from "../../store";
import ColumnsLayout from "./ColumnsLayout";
import CompactLayout from "./CompactLayout";
import LanesLayout from "./LanesLayout";
import LiveRail from "./LiveRail";
import c from "./Board.module.css";

export default function Board() {
  const layout = useStore((s) => s.layout);
  const showRail = useStore((s) => s.showRail);

  return (
    <div className={c["board-host"]}>
      <div style={{ flex: 1, display: "flex", overflow: "hidden" }}>
        {layout === "columns" && <ColumnsLayout />}
        {layout === "compact" && <CompactLayout />}
        {layout === "lanes" && <LanesLayout />}
      </div>
      {showRail && <LiveRail />}
    </div>
  );
}
