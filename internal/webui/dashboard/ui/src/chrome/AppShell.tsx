import { Outlet } from "react-router-dom";
import { useEffect, useState } from "react";
import { Sidebar } from "./Sidebar";
import { whoami } from "@/api/auth";
import type { Principal } from "@/api/types";

export function AppShell() {
  const [me, setMe] = useState<Principal | null>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let alive = true;
    whoami().then((p) => {
      if (!alive) return;
      if (!p) {
        window.location.href = "/dashboard/login";
        return;
      }
      setMe(p);
      setLoaded(true);
    });
    return () => { alive = false; };
  }, []);

  if (!loaded) return <div className="empty">Loading…</div>;

  return (
    <div className="shell">
      <Sidebar me={me} />
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
