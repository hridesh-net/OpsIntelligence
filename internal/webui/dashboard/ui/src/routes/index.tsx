import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "@/chrome/AppShell";
import { Overview } from "./Overview";
import { Board } from "@/kanban/Board";
import { Chat } from "./Chat";
import { Stub } from "./Stub";

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<Overview />} />
        <Route path="/boards" element={<Board />} />
        <Route path="/chat" element={<Chat />} />
        <Route path="/tasks" element={<Stub title="Tasks" sub="Async task manager" />} />
        <Route path="/repos" element={<Stub title="Repos" sub="Indexed repositories" />} />
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
