import { NavLink } from "react-router-dom";
import type { Principal } from "@/api/types";
import { logout } from "@/api/auth";

interface SidebarProps {
  me: Principal | null;
}

interface NavEntry {
  to: string;
  label: string;
  icon: string;
}

const SECTIONS: Array<{ title?: string; entries: NavEntry[] }> = [
  {
    entries: [
      { to: "/overview", label: "Overview", icon: "◆" },
      { to: "/boards", label: "Boards", icon: "▣" },
      { to: "/tasks", label: "Tasks", icon: "≡" },
      { to: "/repos", label: "Repos", icon: "{}" },
    ],
  },
  {
    title: "Operate",
    entries: [
      { to: "/runtrace", label: "Run Trace", icon: "↯" },
      { to: "/chat", label: "Chat", icon: "✦" },
      { to: "/analytics", label: "Analytics", icon: "∿" },
    ],
  },
  {
    title: "Admin",
    entries: [
      { to: "/users", label: "Users", icon: "◉" },
      { to: "/apikeys", label: "API Keys", icon: "⌽" },
      { to: "/settings", label: "Settings", icon: "⚙" },
    ],
  },
];

export function Sidebar({ me }: SidebarProps) {
  const initials = (me?.display_name || me?.username || "?")
    .split(/\s+/)
    .map((p) => p[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();

  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-mark">O</div>
        <div className="brand-name">OpsIntelligence</div>
      </div>

      <nav className="nav">
        {SECTIONS.map((section, i) => (
          <div key={i}>
            {section.title && <div className="nav-section">{section.title}</div>}
            {section.entries.map((e) => (
              <NavLink
                key={e.to}
                to={e.to}
                className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
              >
                <span className="mono" style={{ width: 14, color: "inherit", opacity: 0.7 }}>{e.icon}</span>
                <span>{e.label}</span>
              </NavLink>
            ))}
          </div>
        ))}
      </nav>

      <div className="sidebar-footer">
        {me && (
          <div className="whoami">
            <div className="avatar">{initials}</div>
            <div style={{ display: "flex", flexDirection: "column", lineHeight: 1.2 }}>
              <span>{me.display_name || me.username}</span>
              <span style={{ fontSize: 10, color: "var(--fg-muted)" }}>{me.type}</span>
            </div>
          </div>
        )}
        <button onClick={async () => { await logout(); window.location.href = "/dashboard/login"; }}>
          Sign out
        </button>
      </div>
    </aside>
  );
}
