import { useStore } from "../../store";
import Avatar from "../Avatar";
import Dropdown from "../Dropdown";
import { Cog, Plus, Search as SearchIco } from "../Icons";
import type { Screen } from "../../types";
import s from "./TopBar.module.css";

const SCREEN_TITLES: Record<Screen, string> = {
  board: "AI Workforce Board",
  workflows: "Workflow Builder",
  agents: "Agent Manager",
  activity: "Agent Activity",
  analytics: "Analytics",
};

const PRIOS: [string, string, string][] = [
  ["H", "High", "var(--danger)"],
  ["M", "Medium", "var(--warning)"],
  ["L", "Low", "var(--accent)"],
];
const TYPES: [string, string][] = [
  ["feat", "Feature"],
  ["fix", "Fix"],
  ["infra", "Infra"],
  ["sec", "Security"],
  ["research", "Research"],
  ["chore", "Chore"],
];

const sunIco = (
  <>
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M19 5l-1.5 1.5M6.5 17.5 5 19" />
  </>
);
const moonIco = <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" />;

export default function TopBar() {
  const st = useStore();
  const onBoard = st.screen === "board";
  const f = st.filters;
  const agents = Object.entries(st.agents);

  return (
    <header className={s.topbar}>
      <Dropdown
        align="left"
        menuStyle={{ minWidth: 210 }}
        trigger={(_open, toggle) => (
          <div className={s.boardPick} onClick={toggle}>
            <span className={s.star}>★</span> <span>{st.boardName}</span>{" "}
            <span className={s.chev}>▾</span>
          </div>
        )}
      >
        {(close) => (
          <>
            <div className="mlabel">Board</div>
            <div
              className="mi"
              onClick={() => {
                close();
                st.reconfigureBoard();
              }}
            >
              <Cog size={16} style={{ color: "var(--text-dim)" }} />
              Board setup
            </div>
            <div
              className="mi"
              onClick={() => {
                close();
                st.go("workflows");
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} style={{ color: "var(--text-dim)" }}>
                <rect x="3" y="4" width="6" height="6" rx="1.5" />
                <rect x="15" y="14" width="6" height="6" rx="1.5" />
                <path d="M9 7h4a2 2 0 0 1 2 2v8" />
              </svg>
              Edit workflow
            </div>
          </>
        )}
      </Dropdown>

      {!onBoard && (
        <div className={s.titleWrap}>
          <span className={s.screenTitle}>{SCREEN_TITLES[st.screen]}</span>
        </div>
      )}

      {onBoard && (
        <div className={s.toolbar}>
          <div className={s.search}>
            <SearchIco />
            <input
              id="scrun-search"
              placeholder="Search tasks, agents, branches…"
              value={f.q}
              onChange={(e) => st.setSearch(e.target.value)}
            />
            <span className={s.kbd}>⌘K</span>
          </div>

          {/* agent filter */}
          <Dropdown
            menuStyle={{ minWidth: 210 }}
            trigger={(_o, toggle) => (
              <button className={"btn" + (f.agent !== "all" ? " on" : "")} onClick={toggle}>
                <svg className="ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
                  <rect x="3" y="11" width="18" height="10" rx="2" />
                  <circle cx="12" cy="5" r="2" />
                  <path d="M12 7v4M8 16h.01M16 16h.01" />
                </svg>
                Agent<span className="chev">▾</span>
              </button>
            )}
          >
            {(close) => (
              <>
                <div className="mlabel">Filter by agent</div>
                <div className={"mi" + (f.agent === "all" ? " sel" : "")} onClick={() => { st.setFilter("agent", "all"); close(); }}>
                  All agents<span className="ck">✓</span>
                </div>
                {agents.map(([k, a]) => (
                  <div key={k} className={"mi" + (f.agent === k ? " sel" : "")} onClick={() => { st.setFilter("agent", k); close(); }}>
                    <Avatar color={a.color} ini={a.ini} />
                    {a.name}
                    <span className="ck">✓</span>
                  </div>
                ))}
              </>
            )}
          </Dropdown>

          {/* priority filter */}
          <Dropdown
            trigger={(_o, toggle) => (
              <button className={"btn" + (f.prio !== "all" ? " on" : "")} onClick={toggle}>
                Priority<span className="chev">▾</span>
              </button>
            )}
          >
            {(close) => (
              <>
                <div className="mlabel">Priority</div>
                <div className={"mi" + (f.prio === "all" ? " sel" : "")} onClick={() => { st.setFilter("prio", "all"); close(); }}>
                  All<span className="ck">✓</span>
                </div>
                {PRIOS.map(([k, label, color]) => (
                  <div key={k} className={"mi" + (f.prio === k ? " sel" : "")} onClick={() => { st.setFilter("prio", k); close(); }}>
                    <span className="av" style={{ background: color }}>{k}</span>
                    {label}
                    <span className="ck">✓</span>
                  </div>
                ))}
              </>
            )}
          </Dropdown>

          {/* type filter */}
          <Dropdown
            trigger={(_o, toggle) => (
              <button className={"btn" + (f.type !== "all" ? " on" : "")} onClick={toggle}>
                Type<span className="chev">▾</span>
              </button>
            )}
          >
            {(close) => (
              <>
                <div className="mlabel">Work type</div>
                <div className={"mi" + (f.type === "all" ? " sel" : "")} onClick={() => { st.setFilter("type", "all"); close(); }}>
                  All<span className="ck">✓</span>
                </div>
                {TYPES.map(([k, label]) => (
                  <div key={k} className={"mi" + (f.type === k ? " sel" : "")} onClick={() => { st.setFilter("type", k); close(); }}>
                    {label}
                    <span className="ck">✓</span>
                  </div>
                ))}
              </>
            )}
          </Dropdown>

          <div className="seg">
            <button className={st.layout === "columns" ? "on" : ""} title="Board" onClick={() => st.setLayout("columns")}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
                <rect x="3" y="4" width="5" height="16" rx="1.2" /><rect x="9.5" y="4" width="5" height="16" rx="1.2" /><rect x="16" y="4" width="5" height="16" rx="1.2" />
              </svg>
            </button>
            <button className={st.layout === "compact" ? "on" : ""} title="List" onClick={() => st.setLayout("compact")}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
                <path d="M4 6h16M4 12h16M4 18h16" />
              </svg>
            </button>
            <button className={st.layout === "lanes" ? "on" : ""} title="Swimlanes by agent" onClick={() => st.setLayout("lanes")}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
                <rect x="3" y="4" width="18" height="6" rx="1.4" /><rect x="3" y="14" width="18" height="6" rx="1.4" />
              </svg>
            </button>
          </div>

          <span
            className={"icon-btn" + (st.showRail ? " on" : "")}
            title="Live activity rail"
            onClick={st.toggleRail}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
              <path d="M3 12h4l2 6 4-12 2 6h6" />
            </svg>
          </span>
        </div>
      )}

      <div className={s.topActions}>
        <span className={s.dividerV} />
        <div className={s.themeSw} title="Toggle theme" onClick={st.toggleTheme}>
          <span className={s.knob + (st.theme === "light" ? " " + s.knobLight : "")}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
              {st.theme === "dark" ? sunIco : moonIco}
            </svg>
          </span>
        </div>
        <span className="icon-btn" title="Settings">
          <Cog size={16} />
        </span>
        <button
          className="btn btn-primary"
          onClick={() => {
            if (st.screen !== "board") st.go("board");
            st.openTaskForm(null, st.layout === "columns" ? "backlog" : "todo");
          }}
        >
          <Plus className="ico" />
          New Task
        </button>
      </div>
    </header>
  );
}
