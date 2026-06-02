import type { ReactNode } from "react";

interface TopbarProps {
  title: string;
  sub?: string;
  actions?: ReactNode;
}

export function Topbar({ title, sub, actions }: TopbarProps) {
  return (
    <header className="topbar">
      <h1>{title}</h1>
      {sub && <span className="sub">{sub}</span>}
      <div className="spacer" />
      {actions && <div className="actions">{actions}</div>}
    </header>
  );
}
