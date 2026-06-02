import { Topbar } from "@/chrome/Topbar";

export function Stub({ title, sub, note }: { title: string; sub?: string; note?: string }) {
  return (
    <>
      <Topbar title={title} sub={sub} />
      <div className="view">
        <div style={{
          background: "var(--bg-elev)",
          border: "1px solid var(--border)",
          borderRadius: "var(--radius)",
          padding: 32,
          color: "var(--fg-muted)",
        }}>
          <h2 style={{ color: "var(--fg)", marginBottom: 8 }}>Coming up</h2>
          <p>{note ?? "This screen is being ported from the legacy bundle. The data already flows through the unified API client — UI follows."}</p>
        </div>
      </div>
    </>
  );
}
