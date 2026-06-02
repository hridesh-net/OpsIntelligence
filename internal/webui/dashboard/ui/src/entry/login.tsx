import React, { useEffect, useState } from "react";
import ReactDOM from "react-dom/client";
import { bootstrap, getAuthStatus, login } from "@/api/auth";

import "@/theme/tokens.css";

function LoginPage() {
  const [needsBootstrap, setNeedsBootstrap] = useState<boolean | null>(null);
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getAuthStatus()
      .then((s) => setNeedsBootstrap(!!s.bootstrap_needed))
      .catch(() => setNeedsBootstrap(false));
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (needsBootstrap) {
        await bootstrap(username, email, password);
      } else {
        await login(username, password);
      }
      window.location.href = "/dashboard/app";
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  if (needsBootstrap === null) return <div style={{ padding: 40, textAlign: "center" }}>…</div>;

  return (
    <div style={{
      minHeight: "100vh",
      display: "grid",
      placeItems: "center",
      padding: 24,
    }}>
      <form onSubmit={submit} style={{
        width: 360,
        background: "var(--bg-elev)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-lg)",
        padding: 32,
        boxShadow: "var(--shadow-overlay)",
        display: "flex",
        flexDirection: "column",
        gap: 14,
      }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
          <div style={{
            width: 28, height: 28, borderRadius: 8,
            background: "linear-gradient(135deg, var(--accent), var(--accent-bright))",
            color: "#fff", display: "grid", placeItems: "center", fontWeight: 700,
          }}>O</div>
          <span style={{ fontWeight: 600, fontSize: 16 }}>OpsIntelligence</span>
        </div>
        <h1 style={{ fontSize: 18 }}>{needsBootstrap ? "Create owner account" : "Sign in"}</h1>

        <label style={labelStyle}>Username
          <input value={username} onChange={(e) => setUsername(e.target.value)} style={inputStyle} autoFocus required />
        </label>
        {needsBootstrap && (
          <label style={labelStyle}>Email
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} style={inputStyle} required />
          </label>
        )}
        <label style={labelStyle}>Password
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} style={inputStyle} required minLength={8} />
        </label>

        {error && <div style={{ color: "var(--danger)", fontSize: 12 }}>{error}</div>}

        <button className="btn primary" type="submit" disabled={busy} style={{ marginTop: 8, padding: "10px 14px", justifyContent: "center" }}>
          {busy ? "…" : needsBootstrap ? "Create account" : "Sign in"}
        </button>
      </form>
    </div>
  );
}

const labelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  fontSize: 12,
  color: "var(--fg-dim)",
};
const inputStyle: React.CSSProperties = {
  padding: "8px 12px",
  border: "1px solid var(--border)",
  borderRadius: "var(--radius-sm)",
  background: "var(--bg)",
  color: "var(--fg)",
  fontSize: 14,
  outline: "none",
};

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <LoginPage />
  </React.StrictMode>,
);
