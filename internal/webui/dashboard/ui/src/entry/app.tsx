import React from "react";
import ReactDOM from "react-dom/client";
import { HashRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppRoutes } from "@/routes";

import "@/theme/tokens.css";
import "@/chrome/chrome.css";
import "@/kanban/kanban.css";

const qc = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 4_000, retry: 1, refetchOnWindowFocus: false },
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <HashRouter>
        <AppRoutes />
      </HashRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);
