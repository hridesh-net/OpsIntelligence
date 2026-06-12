import { useStore } from "../../store";
import type { Screen } from "../../types";
import Logo from "../Logo";
import s from "./NavRail.module.css";

type NavDef = { go: Screen; label: string; badge?: string; dim?: boolean; icon: JSX.Element };

export default function NavRail() {
  const screen = useStore((st) => st.screen);
  const collapsed = useStore((st) => st.railCollapsed);
  const cardCount = useStore((st) => st.cards.length);
  const agentCount = useStore((st) => Object.keys(st.agents).length);
  const go = useStore((st) => st.go);
  const toggleCollapsed = useStore((st) => st.toggleRailCollapsed);

  const workspace: NavDef[] = [
    {
      go: "board",
      label: "Board",
      badge: String(cardCount),
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <rect x="3" y="3" width="6" height="18" rx="1.5" />
          <rect x="10.5" y="3" width="6" height="12" rx="1.5" />
          <rect x="18" y="3" width="3" height="9" rx="1.5" />
        </svg>
      ),
    },
    {
      go: "workflows",
      label: "Workflows",
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <rect x="3" y="4" width="6" height="6" rx="1.5" />
          <rect x="15" y="4" width="6" height="6" rx="1.5" />
          <rect x="9" y="14" width="6" height="6" rx="1.5" />
          <path d="M6 10v2a2 2 0 0 0 2 2h1M18 10v2a2 2 0 0 1-2 2h-1" />
        </svg>
      ),
    },
    {
      go: "agents",
      label: "Agents",
      badge: String(agentCount),
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <rect x="4" y="10" width="16" height="10" rx="2" />
          <circle cx="12" cy="5" r="2.2" />
          <path d="M12 7v3M9 15h.01M15 15h.01" />
        </svg>
      ),
    },
    {
      go: "activity",
      label: "Activity",
      badge: "live",
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <path d="M3 12h4l3 8 4-16 3 8h4" />
        </svg>
      ),
    },
  ];

  const insights: NavDef[] = [
    {
      go: "analytics",
      label: "Analytics",
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <path d="M3 3v18h18" />
          <path d="M7 14l4-4 3 3 5-6" />
        </svg>
      ),
    },
    {
      go: "board",
      label: "Knowledge",
      dim: true,
      icon: (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
          <path d="M4 19V5a2 2 0 0 1 2-2h9l5 5v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z" />
          <path d="M14 3v5h5" />
        </svg>
      ),
    },
  ];

  const item = (n: NavDef, i: number) => (
    <div
      key={n.label + i}
      className={s.navItem + (screen === n.go && !n.dim ? " " + s.on : "")}
      style={n.dim ? { opacity: 0.55 } : undefined}
      onClick={() => go(n.go)}
    >
      <span className={s.niIco}>{n.icon}</span>
      <span className={s.niText}>{n.label}</span>
      {n.badge && <span className={s.badge}>{n.badge}</span>}
    </div>
  );

  return (
    <aside className={s.rail + (collapsed ? " " + s.collapsed : "")}>
      <div className={s.railTop}>
        <div className={s.brand}>
          <Logo />
          <span className={s.name}>Scrun</span>
        </div>
        <span className={s.collapseBtn} title="Collapse" onClick={toggleCollapsed}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
            <path d="M15 6l-6 6 6 6" />
          </svg>
        </span>
      </div>
      <nav className={s.nav}>
        <div
          className={s.navItem}
          style={{ opacity: 0.8 }}
          onClick={() => useStore.getState().goToBoards()}
        >
          <span className={s.niIco}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
              <path d="M15 6l-6 6 6 6" />
            </svg>
          </span>
          <span className={s.niText}>All boards</span>
        </div>
        <div className={s.navLabel}>Workspace</div>
        {workspace.map(item)}
        <div className={s.navLabel}>Insights</div>
        {insights.map(item)}
      </nav>
      <div className={s.railUser}>
        <span className={s.uav}>LO</span>
        <span className={s.uinfo}>
          <b>Leodavinci1</b>
          <small>Admin · OpsIntelAI</small>
        </span>
        <span className={s.uchev}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
            <path d="m6 9 6 6 6-6" />
          </svg>
        </span>
      </div>
    </aside>
  );
}
