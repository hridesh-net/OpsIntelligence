import { useEffect } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "@/chrome/AppShell";
import { Overview } from "./Overview";
import { Chat } from "./Chat";
import { Repos } from "./Repos";
import { Stub } from "./Stub";

// /boards is served by the Go binary directly out of /dashboard/kanban
// (the Scrun React shell). If someone lands on the hash route via an
// old bookmark or in-app link, full-page-navigate to the Scrun bundle.
function BoardsRedirect() {
  useEffect(() => {
    window.location.replace("/dashboard/kanban");
  }, []);
  return (
    <div style={{ padding: 24, color: "var(--fg-muted)" }}>Opening Scrun…</div>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<Overview />} />
        <Route path="/boards" element={<BoardsRedirect />} />
        <Route path="/chat" element={<Chat />} />
        <Route path="/tasks" element={<Stub title="Tasks" sub="Async task manager" />} />
        <Route path="/repos" element={<Repos />} />
        <Route path="/runtrace" element={<Stub title="Run Trace" sub="Live run telemetry" />} />
        <Route path="/analytics" element={<Stub title="Analytics" sub="Throughput, cost, quality" />} />
        <Route path="/users" element={<Stub title="Users" sub="Members & roles" />} />
        <Route path="/apikeys" element={<Stub title="API Keys" sub="Personal & service keys" />} />
        <Route path="/settings/*" element={<Stub title="Settings" sub="Gateway, providers, integrations" />} />
        <Route path="/settings" element={<Stub title="Settings" sub="Gateway, providers, integrations" />} />
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Route>
    </Routes>
  );
}
