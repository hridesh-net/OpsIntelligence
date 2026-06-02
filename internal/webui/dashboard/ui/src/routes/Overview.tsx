import { Topbar } from "@/chrome/Topbar";

export function Overview() {
  return (
    <>
      <Topbar title="Overview" sub="Operational summary" />
      <div className="view">
        <div className="kpi-grid">
          <Kpi label="Active runs" value="—" />
          <Kpi label="Boards" value="—" />
          <Kpi label="Repos indexed" value="—" />
          <Kpi label="Agents" value="—" />
        </div>
        <p style={{ color: "var(--fg-muted)", marginTop: 24 }}>
          Overview widgets will land in a follow-up. The shell and Boards screen are live.
        </p>
      </div>
    </>
  );
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div style={{
      background: "var(--bg-elev)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius)",
      padding: 16,
      boxShadow: "var(--shadow-card)",
    }}>
      <div style={{ fontSize: 11, color: "var(--fg-muted)", textTransform: "uppercase", letterSpacing: "0.06em" }}>{label}</div>
      <div style={{ fontSize: 28, fontWeight: 600, marginTop: 6, fontFamily: "var(--mono)" }}>{value}</div>
    </div>
  );
}
