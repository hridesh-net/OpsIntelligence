// OpsIntelligence dashboard SPA (phase 3c).
//
// Responsibilities:
//   1. Auth page  → sign-in / first-run owner bootstrap.
//   2. App shell  → hash-based routing across overview / tasks /
//                   users / apikeys / settings/<section>.
//   3. Settings   → per-section forms backed by /api/v1/config/<section>
//                   with optimistic concurrency (If-Match) and CSRF.
//
// Settings pages are deliberately schema-driven: each section has a
// declarative schema describing its fields. The same renderer turns a
// schema + the JSON returned by the API into a form, and the same
// serializer turns the form back into a typed payload before PUT.
//
// Adding a new section in the future:
//   - add a CONFIG_SCHEMA[<name>] entry (or a custom render() fn for
//     anything fancier than the generic field renderer can express)
//   - add a sidebar entry to <nav id="settings-nav"> in app.html
// No backend change is needed as long as the section is wired in
// internal/gateway/config_api.go's putConfigSection switch.

(() => {
  "use strict";

  const DASH = "/dashboard";
  const API = "/api/v1";

  document.addEventListener("DOMContentLoaded", () => {
    if (document.body.classList.contains("auth-page")) {
      bootAuthPage();
    } else if (document.body.classList.contains("app-page")) {
      bootAppPage();
    }
  });

  // ─────────────────────── auth page ───────────────────────

  async function bootAuthPage() {
    const loginForm = document.getElementById("login-form");
    const bootForm = document.getElementById("bootstrap-form");
    const heading = document.getElementById("auth-heading");
    const sub = document.getElementById("auth-subheading");

    const status = await getJSON(`${API}/auth/status`).catch(() => null);
    const who = await getJSON(`${API}/whoami`).catch(() => null);

    if (who && who.type === "user") {
      window.location.href = `${DASH}/app`;
      return;
    }

    if (status && status.bootstrap_needed) {
      heading.textContent = "First-run setup";
      sub.textContent = "Create the initial owner account.";
      bootForm.hidden = false;
      wireBootstrapForm(bootForm, status);
      return;
    }

    loginForm.hidden = false;
    wireLoginForm(loginForm);
  }

  function wireLoginForm(form) {
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      clearError();
      const fd = new FormData(form);
      const body = {
        username: String(fd.get("username") || "").trim(),
        password: String(fd.get("password") || ""),
      };
      if (!body.username || !body.password) {
        showError("username and password required");
        return;
      }
      setBusy(form, true);
      try {
        const res = await fetch(`${API}/auth/login`, {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          throw new Error(body.error || `login failed (${res.status})`);
        }
        window.location.href = `${DASH}/app`;
      } catch (err) {
        showError(err.message || "login failed");
      } finally {
        setBusy(form, false);
      }
    });
  }

  function wireBootstrapForm(form, status) {
    const min = status && status.min_password_length ? status.min_password_length : 12;
    form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      clearError();
      const fd = new FormData(form);
      const username = String(fd.get("username") || "").trim();
      const email = String(fd.get("email") || "").trim();
      const password = String(fd.get("password") || "");
      const confirm = String(fd.get("confirm") || "");
      if (!username || !password) {
        showError("username and password required");
        return;
      }
      if (password !== confirm) {
        showError("passwords do not match");
        return;
      }
      if (password.length < min) {
        showError(`password must be at least ${min} characters`);
        return;
      }
      setBusy(form, true);
      try {
        const res = await fetch(`${API}/auth/bootstrap`, {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ username, email, password }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => ({}));
          throw new Error(body.error || `bootstrap failed (${res.status})`);
        }
        window.location.href = `${DASH}/app`;
      } catch (err) {
        showError(err.message || "bootstrap failed");
      } finally {
        setBusy(form, false);
      }
    });
  }

  function showError(msg) {
    const el = document.getElementById("auth-error");
    if (!el) return;
    el.textContent = msg;
    el.hidden = false;
  }

  function clearError() {
    const el = document.getElementById("auth-error");
    if (!el) return;
    el.textContent = "";
    el.hidden = true;
  }

  function setBusy(form, busy) {
    const btn = form.querySelector("button[type=submit]");
    if (btn) btn.disabled = busy;
  }

  // ─────────────────────── app page ───────────────────────

  // Cached principal — set once at boot, used by panels to know
  // whether to attempt secrets.read / settings.write.
  let ME = null;
  let runTracePollId = null;
  let tasksPollId = null;
  let reposPollId = null;
  let overviewPollId = null;
  let boardsPollId = null;
  let cmdPaletteActive = false;
  let cmdPaletteIndex = -1;

  let runTraceLineCache = [];
  let runTraceWhichCache = "all";

  function clearRunTracePoll() {
    if (runTracePollId != null) {
      clearInterval(runTracePollId);
      runTracePollId = null;
    }
  }

  function clearTasksPoll() {
    if (tasksPollId != null) {
      clearInterval(tasksPollId);
      tasksPollId = null;
    }
  }

  function clearReposPoll() {
    if (reposPollId != null) {
      clearInterval(reposPollId);
      reposPollId = null;
    }
  }

  function clearOverviewPoll() {
    if (overviewPollId != null) {
      clearInterval(overviewPollId);
      overviewPollId = null;
    }
  }

  function clearBoardsPoll() {
    if (boardsPollId != null) {
      clearInterval(boardsPollId);
      boardsPollId = null;
    }
  }

  // Inject Scrun stylesheets on first /boards visit, scoping the
  // ":root" design-token blocks to body.scrun-active so they don't
  // bleed into the rest of the dashboard once loaded.
  let scrunStylesLoaded = false;
  async function ensureScrunStyles() {
    if (scrunStylesLoaded) return;
    scrunStylesLoaded = true;
    const sheets = [
      "scrun/scrun.css",
      "scrun/scrun-board.css",
      "scrun/scrun-screens.css",
      "scrun/scrun-analytics.css",
      "scrun/setup.css",
    ];
    for (const href of sheets) {
      try {
        const res = await fetch(href);
        let css = await res.text();
        // Scope :root token blocks so dashboard's own tokens win when
        // the user navigates away from /boards.
        css = css.replace(/:root\s*\{/g, "body.scrun-active {");
        const style = document.createElement("style");
        style.dataset.scrun = href;
        style.textContent = css;
        document.head.appendChild(style);
      } catch (e) {
        console.warn("scrun css load failed", href, e);
      }
    }
  }

  async function bootAppPage() {
    const me = await getJSON(`${API}/whoami`).catch(() => null);
    if (!me || me.type !== "user") {
      window.location.href = `${DASH}/login`;
      return;
    }
    ME = me;
    renderWhoami(me);
    wireLogout();
    wireSearchBar();
    wireKeyboardShortcuts();
    window.addEventListener("hashchange", () => route());
    if (!window.location.hash) window.location.hash = "#/overview";
    route();
  }

  function renderWhoami(p) {
    document.getElementById("whoami-name").textContent =
      p.display_name || p.username || p.user_id || "unknown";
    const roles = Array.isArray(p.roles) && p.roles.length ? p.roles.join(", ") : "no roles";
    document.getElementById("whoami-roles").textContent = roles;
    const card = document.getElementById("card-user");
    if (card) {
      card.textContent = `${p.username || "unknown"} (${roles})`;
    }
  }

  function wireLogout() {
    const btn = document.getElementById("logout");
    if (!btn) return;
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      try {
        await fetch(`${API}/auth/logout`, {
          method: "POST",
          credentials: "same-origin",
          headers: csrfHeaders(),
        });
      } catch (_) {
        // Even if logout failed we clear the local view.
      }
      window.location.href = `${DASH}/login`;
    });
  }

  // ─────────────────────── routing ───────────────────────

  // Hash format: #/<view>[/<sub>]   e.g. #/settings/gateway
  function parseHash() {
    const h = (window.location.hash || "#/overview").replace(/^#\/?/, "");
    const parts = h.split("/").filter(Boolean);
    return { view: parts[0] || "overview", sub: parts[1] || "" };
  }

  function route() {
    clearRunTracePoll();
    clearTasksPoll();
    clearReposPoll();
    clearOverviewPoll();
    clearBoardsPoll();
    const { view, sub } = parseHash();
    // Leaving the Scrun shell? Drop the body class so the dashboard's
    // header padding comes back.
    if (view !== "boards") document.body.classList.remove("scrun-active");
    document.querySelectorAll(".view").forEach((v) => v.classList.add("hidden"));
    const target = document.getElementById(`view-${view}`);
    if (target) {
      target.classList.remove("hidden");
    } else {
      document.getElementById("view-overview").classList.remove("hidden");
    }

    document.querySelectorAll("#primary-nav .nav-item").forEach((a) => {
      a.classList.toggle("active", a.dataset.route === view);
    });

    const titleEl = document.getElementById("section-title");
    const subEl = document.getElementById("section-sub");
    const actionsEl = document.getElementById("content-actions");
    actionsEl.innerHTML = "";

    switch (view) {
      case "overview":
        titleEl.textContent = "Overview";
        subEl.textContent = "A quick look at the ops plane.";
        refreshOverviewStatus();
        clearOverviewPoll();
        overviewPollId = setInterval(refreshOverviewStatus, 10000);
        break;
      case "boards":
        // Scrun shell takes over the section; the dashboard header
        // (title/sub/actions) is hidden by scrun-bridge.css when
        // body.scrun-active is set. CSS is injected on first mount
        // because Scrun's :root tokens would otherwise leak into the
        // rest of the dashboard.
        document.body.classList.add("scrun-active");
        ensureScrunStyles();
        if (typeof window.scrunMount === "function") {
          try { window.scrunMount(); } catch (e) { console.error("scrunMount failed", e); }
        }
        break;
      case "tasks":
        titleEl.textContent = "Tasks";
        subEl.textContent =
          "Live async sub-agent runs (TaskManager): status, last progress event, and errors.";
        renderTasksView(actionsEl);
        break;
      case "users":
        titleEl.textContent = "Users & Roles";
        subEl.textContent = "Identity, roles and permissions.";
        renderUsersView(actionsEl);
        break;
      case "apikeys":
        titleEl.textContent = "API keys";
        subEl.textContent = "Long-lived bearer credentials for automation.";
        renderAPIKeysView(actionsEl);
        break;
      case "runtrace":
        titleEl.textContent = "Run trace";
        subEl.textContent =
          "Cloud-style execution log: merge master + sub-agent NDJSON, filter by stream, expand for full JSON.";
        renderRunTraceView(actionsEl);
        break;
      case "repos":
        titleEl.textContent = "Repo Intelligence";
        subEl.textContent = "Configured repositories, live index/scan status, and quick sync actions.";
        renderReposView(actionsEl);
        break;
      case "settings":
        titleEl.textContent = "Settings";
        subEl.textContent =
          "Every CLI configuration surface — same writes, same file (opsintelligence.yaml).";
        renderSettingsSubnav(sub);
        if (sub) {
          loadSettingsSection(sub);
        } else {
          renderSettingsLanding();
        }
        break;
      default:
        titleEl.textContent = "Overview";
        subEl.textContent = "A quick look at the ops plane.";
    }
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function formatRunTraceTime(t) {
    if (!t) return "—";
    const d = new Date(t);
    if (Number.isNaN(d.getTime())) return escapeHtml(String(t));
    return escapeHtml(d.toLocaleString());
  }

  /** Human text field across zap / OTLP-ish / ad-hoc JSON lines. */
  function traceTextMessage(o) {
    if (!o || typeof o !== "object") return "";
    const cand = o.msg ?? o.message ?? o.Message ?? o.text ?? o.body;
    if (typeof cand === "string") return cand.replace(/\s+/g, " ").trim();
    return "";
  }

  function traceLogLevel(o) {
    if (!o || typeof o !== "object") return "";
    if (typeof o.level === "string" && o.level) return o.level;
    if (typeof o.severity === "string" && o.severity) return o.severity;
    return "";
  }

  /** Trace events use `kind`; zap lines often use `msg` + `level` but some stacks use `message` only. */
  function traceLooksLikeZapLog(o) {
    const msg = traceTextMessage(o);
    if (!msg) return false;
    const lvl = traceLogLevel(o);
    return !!(lvl || o.caller || o.logger || o.Logger);
  }

  function runTraceEventKind(o) {
    if (!o || typeof o !== "object") return "event";
    if (o.kind) return String(o.kind);
    if (traceLooksLikeZapLog(o)) return "log";
    return "event";
  }

  function appendRunTraceComponentHints(o, k, parts) {
    if (o.runner_role) parts.push(`role=${o.runner_role}`);
    if (o.iteration != null && o.iteration !== "") parts.push(`iter=${o.iteration}`);
    if (o.tool) parts.push(`tool=${o.tool}`);
    if (o.chain_id) parts.push(`chain=${o.chain_id}`);
    if (o.run_kind) parts.push(`run=${o.run_kind}`);
    if (o.parent_iteration != null && o.parent_iteration !== "") {
      parts.push(`parent_iter=${o.parent_iteration}`);
    }
    if (o.finish) parts.push(`finish=${o.finish}`);

    if (Array.isArray(o.tools_offered)) {
      parts.push(`tools_offered=${o.tools_offered.length}`);
    }
    if (o.skills_enabled_count != null && o.skills_enabled_count !== "") {
      parts.push(`skills_cnt=${o.skills_enabled_count}`);
    } else if (Array.isArray(o.skills_enabled) && o.skills_enabled.length) {
      parts.push(`skills_cnt=${o.skills_enabled.length}`);
      const preview = o.skills_enabled.slice(0, 4).join(",");
      if (preview.length > 90) parts.push(`skills=${preview.slice(0, 87)}…`);
      else parts.push(`skills=${preview}`);
    }
    if (o.skills_context_chars != null && o.skills_context_chars !== "") {
      parts.push(`skills_ctx=${o.skills_context_chars}c`);
    }
    if (o.local_intel_enabled === true) parts.push("local_intel=on");
    else if (o.local_intel_enabled === false) parts.push("local_intel=off");
    if (o.local_advisory_applied === true) parts.push("li_advisory=on");

    if (o.llm_backend) parts.push(`backend=${o.llm_backend}`);
    if (o.primary_model) parts.push(`primary=${o.primary_model}`);
    if ((k === "model_iteration" || k === "task_start") && o.model) parts.push(`model=${o.model}`);

    if (Array.isArray(o.routing_intents) && o.routing_intents.length) {
      const ri = o.routing_intents.join(",");
      parts.push(`route=${ri.length > 80 ? ri.slice(0, 77) + "…" : ri}`);
    }

    if (k === "chain_run_complete" && Array.isArray(o.step_prompts) && o.step_prompts.length) {
      const st = o.step_prompts.join(" → ");
      parts.push(`steps=${st.length > 100 ? st.slice(0, 100) + "…" : st}`);
    }
    if (k === "task_start" && o.query_preview) {
      const q = String(o.query_preview);
      parts.push(`query=${q.length > 100 ? q.slice(0, 100) + "…" : q}`);
    }
    if (k === "tool_done" && o.ok != null) parts.push(o.ok ? "ok" : "err");
    if (k === "tool_done" && o.ms != null) parts.push(`${o.ms}ms`);
    if (k === "tool_call" && o.input_bytes != null) parts.push(`in=${o.input_bytes}b`);
    if ((k === "task_done" || k === "chain_run_error") && o.error) {
      const e = String(o.error);
      parts.push(`error=${e.length > 120 ? e.slice(0, 120) + "…" : e}`);
    }
  }

  function summarizeRunTraceEvent(o) {
    if (!o || typeof o !== "object") return "";
    if (traceLooksLikeZapLog(o)) {
      const lvl = traceLogLevel(o);
      let msg = traceTextMessage(o);
      if (msg.length > 160) msg = msg.slice(0, 157) + "…";
      const caller = o.caller ? String(o.caller) : "";
      const tail = caller.includes("/") ? caller.split("/").pop() : caller;
      const parts = ["log"];
      if (lvl) parts.push(lvl);
      if (tail) parts.push(tail);
      parts.push(msg);
      return parts.join(" · ");
    }
    const k = o.kind || "event";
    const parts = [`${k}`];
    appendRunTraceComponentHints(o, k, parts);
    if (parts.length === 1) {
      const fb = traceTextMessage(o);
      if (fb) parts.push(fb.length > 140 ? fb.slice(0, 137) + "…" : fb);
      else if (o.caller) {
        const c = String(o.caller);
        parts.push(c.length > 100 ? c.slice(0, 97) + "…" : c);
      }
    }
    return parts.join(" · ");
  }

  function renderRunTraceEmpty(metaText) {
    const total = Array.isArray(runTraceLineCache) ? runTraceLineCache.length : 0;
    const paths = (window.__rtPaths || []).map((p) => `<code>${escapeHtml(p)}</code>`).join("<br>");
    if (total === 0) {
      return `
        <div class="log-empty runtrace-empty">
          <div class="runtrace-empty-title">No agent runs recorded yet</div>
          <p class="runtrace-empty-body">
            Run trace files appear here as soon as the agent executes a step. Trigger one of these
            and refresh to see live events.
          </p>
          <ul class="runtrace-empty-list">
            <li>Send a chat message or webhook to a wired channel.</li>
            <li>Run an async sub-agent task (<code>subagent_run_async</code>) or a chain.</li>
            <li>Invoke the agent from the CLI: <code>opsintelligence run "&lt;prompt&gt;"</code>.</li>
            <li>Index a repo via <strong>Repo Intel → Add</strong> to see indexer traces.</li>
          </ul>
          ${paths ? `<p class="runtrace-empty-paths">Trace files (created on first event):<br>${paths}</p>` : ""}
          <p class="runtrace-empty-meta">${escapeHtml(metaText)}</p>
        </div>
      `;
    }
    return `<div class="log-empty">${escapeHtml(metaText)} — filters hide every line. Clear filters above to see ${total} cached event${total === 1 ? "" : "s"}.</div>`;
  }

  function runTraceKindClass(kind) {
    const k = String(kind || "").toLowerCase();
    if (k === "log") return "log-kind log-kind-log";
    if (k.includes("error") || k === "chain_run_error") return "log-kind log-kind-error";
    if (k.includes("tool")) return "log-kind log-kind-tool";
    if (k.includes("task")) return "log-kind log-kind-task";
    if (k.includes("model") || k.includes("iteration")) return "log-kind log-kind-model";
    if (k.includes("chain")) return "log-kind log-kind-chain";
    return "log-kind";
  }

  function renderRunTraceLogTable(bodyEl, metaEl, lines, metaText) {
    if (metaEl) {
      metaEl.className = "log-meta";
      metaEl.textContent = metaText;
    }
    bodyEl.innerHTML = "";
    if (!lines.length) {
      bodyEl.innerHTML = renderRunTraceEmpty(metaText);
      return;
    }
    const frag = document.createDocumentFragment();
    for (let idx = lines.length - 1; idx >= 0; idx--) {
      const row = lines[idx];
      let obj = row;
      if (typeof row === "string") {
        try {
          obj = JSON.parse(row);
        } catch {
          obj = { kind: "raw", _raw: row };
        }
      }
      const kind = obj && typeof obj === "object" ? runTraceEventKind(obj) : "event";
      const ts = (obj && obj.t) || "";
      const stream = (obj && obj._stream) || (obj && obj.runner_role) || "—";
      const summary = obj && typeof obj === "object" ? summarizeRunTraceEvent(obj) : "";
      const det = document.createElement("details");
      det.className = "log-entry";
      det.open = idx >= lines.length - 3;
      const sum = document.createElement("summary");
      sum.className = "log-summary";
      sum.innerHTML = `<span class="log-ts">${formatRunTraceTime(ts)}</span><span class="log-stream" title="stream / role">${escapeHtml(String(stream))}</span><span class="${runTraceKindClass(kind)}">${escapeHtml(String(kind))}</span><span class="log-msg">${escapeHtml(summary || JSON.stringify(obj).slice(0, 200))}</span>`;
      const pre = document.createElement("pre");
      pre.className = "log-json";
      pre.textContent = JSON.stringify(obj, null, 2);
      det.appendChild(sum);
      det.appendChild(pre);
      frag.appendChild(det);
    }
    bodyEl.appendChild(frag);
  }

  function applyRunTraceFilters() {
    const body = document.getElementById("runtrace-body");
    const meta = document.getElementById("runtrace-meta");
    if (!body) return;
    const streamNeedle = (document.getElementById("runtrace-filter-stream")?.value || "")
      .trim()
      .toLowerCase();
    const kindNeedle = (document.getElementById("runtrace-filter-kind")?.value || "").trim().toLowerCase();
    const textNeedle = (document.getElementById("runtrace-filter-text")?.value || "").trim().toLowerCase();
    const filtered = runTraceLineCache.filter((row) => {
      let obj = row;
      if (typeof row === "string") {
        try {
          obj = JSON.parse(row);
        } catch {
          obj = {};
        }
      }
      if (!obj || typeof obj !== "object") return true;
      const stream = String(obj._stream || obj.runner_role || "").toLowerCase();
      if (streamNeedle) {
        const match =
          stream.includes(streamNeedle) ||
          ((streamNeedle === "subagent" || streamNeedle === "child") &&
            (stream.includes("specialist:") || stream.includes("subagent:") || stream.startsWith("task:")));
        if (!match) return false;
      }
      const kind = String(runTraceEventKind(obj)).toLowerCase();
      if (kindNeedle && !kind.includes(kindNeedle)) return false;
      if (textNeedle) {
        try {
          if (!JSON.stringify(obj).toLowerCase().includes(textNeedle)) return false;
        } catch {
          return false;
        }
      }
      return true;
    });
    const base = window.__rtPathNote || "";
    const pathNote = `${base} · showing ${filtered.length} of ${runTraceLineCache.length} (source=${runTraceWhichCache})`;
    renderRunTraceLogTable(body, meta, filtered, pathNote);
  }

  async function loadRunTraceFetch(which) {
    const body = document.getElementById("runtrace-body");
    const meta = document.getElementById("runtrace-meta");
    if (body) body.innerHTML = `<div class="log-loading">Loading…</div>`;
    try {
      const data = await fetchJSON(
        `${API}/runtrace?which=${encodeURIComponent(which || "all")}&max_lines=1200`,
      );
      const lines = Array.isArray(data.lines) ? data.lines : [];
      runTraceLineCache = lines;
      runTraceWhichCache = which || "all";
      const pathList =
        Array.isArray(data.paths) && data.paths.length
          ? data.paths
          : data.path
            ? [data.path]
            : [];
      window.__rtPaths = pathList;
      const paths = pathList.length ? pathList.join(" · ") : "—";
      window.__rtPathNote = `Sources: ${paths}${data.truncated ? " · tail truncated" : ""}`;
      applyRunTraceFilters();
    } catch (err) {
      const m = String(err.message || err);
      const msg =
        m.includes("403") || m.toLowerCase().includes("permission")
          ? "Permission denied (needs run_trace.read on your role — owner/admin/operator/developer/auditor include it by default)."
          : m;
      if (body) body.innerHTML = `<div class="log-error">${escapeHtml(msg)}</div>`;
      if (meta) meta.textContent = "";
    }
  }

  function renderRunTraceView(actionsEl) {
    const tb = document.getElementById("runtrace-toolbar");
    const body = document.getElementById("runtrace-body");
    const meta = document.getElementById("runtrace-meta");
    if (!tb || !body) return;
    actionsEl.innerHTML = "";
    tb.innerHTML = `
      <label class="inline">NDJSON source
        <select id="runtrace-which">
          <option value="all">All streams (merged)</option>
          <option value="master">Master file only</option>
          <option value="subagent">Sub-agent file only</option>
        </select>
      </label>
      <button type="button" class="primary" id="runtrace-refresh">Refresh</button>
      <label class="inline"><input type="checkbox" id="runtrace-live" checked /> Auto-refresh (10s)</label>
      <span class="runtrace-toolbar-spacer"></span>
      <label class="inline">Filter stream / role <input type="search" id="runtrace-filter-stream" placeholder="master, subagent (matches specialist:…), …" /></label>
      <label class="inline">Kind <input type="search" id="runtrace-filter-kind" placeholder="task_start, tool…" /></label>
      <label class="inline">Contains <input type="search" id="runtrace-filter-text" placeholder="substring in JSON" /></label>
    `;
    if (meta) {
      meta.className = "log-meta";
      meta.textContent = "";
    }

    const whichSel = document.getElementById("runtrace-which");
    const liveCb = document.getElementById("runtrace-live");
    const refresh = () => loadRunTraceFetch(whichSel.value);
    document.getElementById("runtrace-refresh").addEventListener("click", refresh);
    whichSel.addEventListener("change", () => {
      runTraceLineCache = [];
      refresh();
    });
    ["runtrace-filter-stream", "runtrace-filter-kind", "runtrace-filter-text"].forEach((id) => {
      const el = document.getElementById(id);
      if (el)
        el.addEventListener("input", () => {
          if (runTraceLineCache.length) applyRunTraceFilters();
        });
    });
    liveCb.addEventListener("change", (ev) => {
      clearRunTracePoll();
      if (ev.target.checked) {
        runTracePollId = setInterval(refresh, 10000);
      }
    });
    refresh();
    if (liveCb.checked) {
      runTracePollId = setInterval(refresh, 10000);
    }
  }

  function renderTasksView(actionsEl) {
    const tb = document.getElementById("tasks-toolbar");
    const body = document.getElementById("tasks-body");
    if (!tb || !body) return;
    actionsEl.innerHTML = "";
    tb.innerHTML = `
      <button type="button" class="primary" id="tasks-refresh">Refresh</button>
      <label class="inline"><input type="checkbox" id="tasks-live" checked /> Auto-refresh (5s)</label>
    `;
    body.innerHTML = `<div class="log-loading">Loading…</div>`;

    const liveCb = document.getElementById("tasks-live");
    const refresh = async () => {
      try {
        const data = await fetchJSON(`${API}/agent-tasks`);
        const tasks = Array.isArray(data.tasks) ? data.tasks : [];
        if (!tasks.length) {
          body.innerHTML =
            '<p class="note">No async tasks in memory yet. Sub-agent runs appear here after <code>subagent_run_async</code> (or orchestrator delegations).</p>';
          return;
        }
        const rows = tasks
          .map((t) => {
            const last = t.last_event
              ? `${t.last_event.kind || ""}: ${escapeHtml(String(t.last_event.message || "").slice(0, 120))}`
              : "—";
            return `<tr>
              <td><code>${escapeHtml(t.id || "")}</code></td>
              <td>${escapeHtml(t.sub_agent_name || t.sub_agent_id || "—")}</td>
              <td><span class="task-status task-status-${escapeHtml(String(t.status || ""))}">${escapeHtml(t.status || "")}</span></td>
              <td>${t.started_at ? escapeHtml(t.started_at) : "—"}</td>
              <td>${typeof t.elapsed_ms === "number" ? `${Math.round(t.elapsed_ms / 1000)}s` : "—"}</td>
              <td class="task-preview">${escapeHtml(String(t.task_preview || "").slice(0, 80))}</td>
              <td class="task-preview">${last}</td>
              <td>${t.event_count != null ? t.event_count : 0}</td>
            </tr>`;
          })
          .join("");
        body.innerHTML = `<div class="tasks-table-wrap"><table class="admin-table log-ish">
          <thead><tr><th>Task ID</th><th>Agent</th><th>Status</th><th>Started</th><th>Elapsed</th><th>Prompt</th><th>Last event</th><th>#Evts</th></tr></thead>
          <tbody>${rows}</tbody></table></div>`;
      } catch (err) {
        const m = String(err.message || err);
        body.innerHTML =
          m.includes("403") || m.toLowerCase().includes("permission")
            ? '<p class="note">Permission denied (needs run_trace.read).</p>'
            : `<p class="note">${escapeHtml(m)}</p>`;
      }
    };

    document.getElementById("tasks-refresh").addEventListener("click", refresh);
    liveCb.addEventListener("change", (ev) => {
      clearTasksPoll();
      if (ev.target.checked) tasksPollId = setInterval(refresh, 5000);
    });
    refresh();
    if (liveCb.checked) tasksPollId = setInterval(refresh, 5000);
  }

  function renderSettingsSubnav(active) {
    document.querySelectorAll("#settings-nav .settings-nav-item").forEach((a) => {
      a.classList.toggle("active", a.dataset.section === active);
    });
  }

  function renderSettingsLanding() {
    const body = document.getElementById("settings-body");
    body.innerHTML = `
      <div class="placeholder">
        <h2>Settings</h2>
        <p>Pick a section on the left. Each page includes a short <strong>setup guide</strong> where it helps (Gateway, DevOps, Webhooks).</p>
        <p class="note">
          Every form calls the same <code>configsvc</code> as the CLI — saves go to <code>opsintelligence.yaml</code> on disk.
        </p>
        <aside class="setup-guide setup-guide-landing" role="note">
          <h3 class="setup-guide-title">Quick orientation</h3>
          <ul>
            <li><strong>GitHub PAT</strong> → <em>DevOps → GitHub</em> (agent tools: PR, diff, Actions).</li>
            <li><strong>Webhook HMAC secret</strong> → <em>Webhooks → GitHub adapter</em> (not the same as the PAT).</li>
            <li><strong>CLI cheat sheet</strong> → run <code>opsintelligence guides github</code> on the server.</li>
            <li><strong>Full webhook doc</strong> → <code>doc/github-webhooks.md</code> in the repo.</li>
          </ul>
        </aside>
      </div>`;
  }

  // ─────────────────────── settings ───────────────────────

  // Section schemas. Each entry is either:
  //   { fields: [...] }                — generic renderer
  //   { custom: { render, serialize } } — bespoke component
  //
  // Field types: text, password, number, checkbox, textarea, select,
  // duration (string), tags (string[]), kv-pairs ({"k":"v"}), object
  // (recursive). Sensitive fields are flagged so the renderer can show
  // a "currently set, hidden" hint when the API redacts them.
  const CONFIG_SCHEMA = {
    gateway: {
      summary:
        "HTTP/WebSocket listener — host, port, bind mode and optional TLS. " +
        "These changes apply on the next gateway restart.",
      setupGuide: `
        <h3 class="setup-guide-title">Gateway & inbound traffic</h3>
        <ul>
          <li><strong>Dashboard & API</strong> live on <code>host:port</code> (e.g. <code>/dashboard/</code>, <code>/api/v1/</code>).</li>
          <li><strong>GitHub webhooks</strong> use a <em>different</em> path: <code>/api/webhook/github</code> — GitHub must reach this URL over HTTPS in production.</li>
          <li>After changing bind or TLS, <strong>restart the gateway</strong> (<code>opsintelligence restart</code> or your supervisor).</li>
        </ul>`,
      fields: [
        { key: "host", label: "Host", type: "text", help: "Listen address (e.g. 127.0.0.1, 0.0.0.0)." },
        { key: "port", label: "Port", type: "number", min: 1, max: 65535 },
        {
          key: "bind",
          label: "Bind mode",
          type: "select",
          help:
            "loopback = 127.0.0.1 only (same machine / SSH port-forward). " +
            "lan = 0.0.0.0 — reachable on your LAN or Tailscale IP (e.g. http://100.x.x.x:port/dashboard/). " +
            "tailnet = serve via Tailscale tsnet (see gateway.tailscale). Restart after change.",
          options: [
            { value: "", label: "(default)" },
            { value: "loopback", label: "loopback (127.0.0.1)" },
            { value: "lan", label: "lan (0.0.0.0)" },
            { value: "tailnet", label: "tailnet (Tailscale)" },
          ],
        },
        {
          key: "token",
          label: "Legacy bearer token",
          type: "password",
          sensitive: true,
          help: "Optional pre-RBAC shared token. Prefer issuing API keys instead.",
        },
        {
          key: "tls",
          label: "TLS",
          type: "object",
          fields: [
            { key: "cert", label: "Cert file", type: "text" },
            { key: "key", label: "Key file", type: "text" },
          ],
        },
        {
          key: "tailscale",
          label: "Tailscale",
          type: "object",
          fields: [
            {
              key: "mode",
              label: "Mode",
              type: "select",
              options: [
                { value: "", label: "(off)" },
                { value: "off", label: "off" },
                { value: "serve", label: "serve" },
                { value: "funnel", label: "funnel" },
              ],
            },
            { key: "reset_on_exit", label: "Reset on exit", type: "checkbox" },
          ],
        },
      ],
    },

    auth: {
      summary:
        "Identity, sessions and CSRF for the gateway + dashboard. " +
        "Local username/password defaults on; OIDC defaults off.",
      fields: [
        {
          key: "local",
          label: "Local password login",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox-tri" },
            { key: "min_password_length", label: "Min password length", type: "number", min: 8 },
            { key: "require_mixed_case", label: "Require mixed case", type: "checkbox" },
            { key: "require_digit", label: "Require digit", type: "checkbox" },
            { key: "require_symbol", label: "Require symbol", type: "checkbox" },
          ],
        },
        {
          key: "api_keys",
          label: "API keys",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox-tri" },
            { key: "default_expiry", label: "Default expiry", type: "duration", help: "e.g. 720h" },
            { key: "max_expiry", label: "Max expiry", type: "duration" },
          ],
        },
        {
          key: "sessions",
          label: "Sessions",
          type: "object",
          fields: [
            { key: "cookie_name", label: "Session cookie name", type: "text" },
            { key: "csrf_cookie_name", label: "CSRF cookie name", type: "text" },
            { key: "path", label: "Cookie path", type: "text" },
            { key: "domain", label: "Cookie domain", type: "text" },
            { key: "secure", label: "Secure flag", type: "checkbox-tri" },
            {
              key: "same_site",
              label: "SameSite",
              type: "select",
              options: [
                { value: "", label: "(default lax)" },
                { value: "lax", label: "lax" },
                { value: "strict", label: "strict" },
                { value: "none", label: "none" },
              ],
            },
            { key: "ttl", label: "TTL", type: "duration", help: "e.g. 168h (7 days)" },
          ],
        },
        {
          key: "csrf",
          label: "CSRF",
          type: "object",
          fields: [{ key: "enabled", label: "Enabled", type: "checkbox-tri" }],
        },
        {
          key: "oidc",
          label: "OIDC",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "issuer", label: "Issuer URL", type: "text" },
            { key: "client_id", label: "Client ID", type: "text" },
            { key: "client_secret", label: "Client secret", type: "password", sensitive: true },
            { key: "redirect_url", label: "Redirect URL", type: "text" },
            { key: "scopes", label: "Scopes", type: "tags" },
            { key: "username_claim", label: "Username claim", type: "text" },
            { key: "email_claim", label: "Email claim", type: "text" },
            { key: "allowed_domains", label: "Allowed email domains", type: "tags" },
            { key: "default_role", label: "Default role", type: "text" },
          ],
        },
        {
          key: "legacy_shared_token",
          label: "Legacy shared token",
          type: "password",
          sensitive: true,
          help: "Bootstrap-only Authorization: Bearer credential. Leave empty in new installs.",
        },
        {
          key: "allow_anonymous_bootstrap",
          label: "Allow anonymous /bootstrap",
          type: "checkbox-tri",
          help: "Auto-disabled once the first owner exists.",
        },
      ],
    },

    datastore: {
      summary:
        "Ops-plane persistence — users, roles, API keys, sessions, audit log " +
        "and task history. Strictly separate from agent memory.",
      fields: [
        {
          key: "driver",
          label: "Driver",
          type: "select",
          options: [
            { value: "sqlite", label: "sqlite" },
            { value: "postgres", label: "postgres" },
          ],
        },
        {
          key: "dsn",
          label: "DSN",
          type: "password",
          sensitive: true,
          help:
            "SQLite: file path or URI. Postgres: libpq URL. " +
            "Empty falls back to <state_dir>/ops.db for SQLite.",
        },
        { key: "max_open_conns", label: "Max open connections", type: "number", min: 0 },
        { key: "max_idle_conns", label: "Max idle connections", type: "number", min: 0 },
        { key: "conn_max_lifetime", label: "Conn max lifetime", type: "duration" },
        {
          key: "migrations",
          label: "Migrations",
          type: "select",
          options: [
            { value: "auto", label: "auto" },
            { value: "manual", label: "manual" },
          ],
        },
      ],
    },

    devops: {
      summary:
        "First-class DevOps platform integrations. Tokens are write-only — " +
        "leave a field blank to keep the existing value unchanged.",
      setupGuide: `
        <h3 class="setup-guide-title">GitHub — which credential?</h3>
        <ul>
          <li><strong>Token / token_env here</strong> = Personal Access Token for the <strong>GitHub REST API</strong> (<code>devops.github.*</code> tools: read PR, diff, checks). Typical scope: <code>repo</code> for private repositories.</li>
          <li><strong>Not</strong> the webhook signing secret — that lives under <strong>Settings → Webhooks</strong> (adapter HMAC).</li>
          <li><strong>Posting PR reviews</strong> back to GitHub uses the <code>gh</code> CLI + <code>GH_TOKEN</code> / PAT with review permission — see <code>doc/github-webhooks.md</code> and run <code>opsintelligence guides github</code> on the host.</li>
        </ul>`,
      fields: [
        {
          key: "github",
          label: "GitHub",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "base_url", label: "API base URL", type: "text" },
            { key: "token", label: "Token", type: "password", sensitive: true },
            { key: "token_env", label: "Token env var", type: "text" },
            { key: "default_org", label: "Default org", type: "text" },
          ],
        },
        {
          key: "gitlab",
          label: "GitLab",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "base_url", label: "Base URL", type: "text" },
            { key: "token", label: "Token", type: "password", sensitive: true },
            { key: "token_env", label: "Token env var", type: "text" },
          ],
        },
        {
          key: "jenkins",
          label: "Jenkins",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "base_url", label: "Base URL", type: "text" },
            { key: "user", label: "User", type: "text" },
            { key: "token", label: "API token", type: "password", sensitive: true },
            { key: "token_env", label: "Token env var", type: "text" },
          ],
        },
        {
          key: "sonar",
          label: "SonarQube",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "base_url", label: "Base URL", type: "text" },
            { key: "token", label: "Token", type: "password", sensitive: true },
            { key: "token_env", label: "Token env var", type: "text" },
            { key: "project_key_prefix", label: "Project key prefix", type: "text" },
          ],
        },
      ],
    },

    agent: {
      summary: "Agent runner behavior — iteration cap, planning, reflection, LocalIntel.",
      fields: [
        { key: "max_iterations", label: "Max iterations", type: "number", min: 1 },
        { key: "system_prompt_ext", label: "System prompt extension", type: "textarea" },
        { key: "tools_dir", label: "Tools directory", type: "text" },
        { key: "skills_dir", label: "Skills directory", type: "text" },
        { key: "enabled_skills", label: "Enabled skills", type: "tags" },
        { key: "planning", label: "Planning pass", type: "checkbox-tri" },
        { key: "reflection", label: "Reflection pass", type: "checkbox-tri" },
        {
          key: "heartbeat",
          label: "Heartbeat",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "interval", label: "Interval", type: "duration" },
            { key: "session_id", label: "Session ID", type: "text" },
            { key: "prompt", label: "Prompt", type: "textarea" },
          ],
        },
        {
          key: "local_intel",
          label: "Local intel (Gemma)",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "gguf_path", label: "GGUF path", type: "text" },
            { key: "max_tokens", label: "Max tokens", type: "number", min: 0 },
            { key: "system_prompt", label: "System prompt override", type: "textarea" },
            { key: "cache_dir", label: "Cache dir", type: "text" },
          ],
        },
        {
          key: "palace",
          label: "Palace (local retrieval shaping)",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "shadow_only", label: "Shadow only", type: "checkbox" },
            { key: "prompt_routing", label: "Prompt routing", type: "checkbox" },
            { key: "memory_search_routing", label: "Memory search routing", type: "checkbox" },
            { key: "tool_routing", label: "Tool routing", type: "checkbox" },
            { key: "fail_open", label: "Fail open", type: "checkbox" },
            { key: "log_decisions", label: "Log decisions", type: "checkbox" },
          ],
        },
      ],
    },

    repo_intel: {
      summary:
        "Repository intelligence registry, memory/index directories, scan policy and watcher interval.",
      fields: [
        { key: "enabled", label: "Enabled", type: "checkbox" },
        { key: "registry_file", label: "Registry file", type: "text" },
        { key: "memory_dir", label: "Memory dir", type: "text" },
        { key: "max_files_per_repo", label: "Max files fetched per repo (index + graph)", type: "number", min: 1 },
        {
          key: "show_callgraph_library_packages",
          label: "Show external library / package nodes in call graph",
          type: "checkbox",
        },
        { key: "full_index_disable", label: "Disable full-repository RAG index", type: "checkbox" },
        { key: "full_index_max_files", label: "Full index max files per repo", type: "number", min: 0 },
        { key: "full_index_max_file_kb", label: "Full index max file size (KB)", type: "number", min: 0 },
        { key: "full_index_chunk_runes", label: "Full index chunk size (runes)", type: "number", min: 0 },
        { key: "full_index_concurrency", label: "Full index download concurrency", type: "number", min: 0 },
        { key: "docs_dir", label: "Docs dir", type: "text" },
        { key: "check_interval", label: "Check interval", type: "duration" },
        {
          key: "scan",
          label: "Scan policy",
          type: "object",
          fields: [
            { key: "enabled", label: "Enabled", type: "checkbox" },
            { key: "max_findings", label: "Max findings", type: "number", min: 0 },
            {
              key: "severity_floor",
              label: "Severity floor",
              type: "select",
              options: [
                { value: "", label: "(default)" },
                { value: "low", label: "low" },
                { value: "medium", label: "medium" },
                { value: "high", label: "high" },
                { value: "critical", label: "critical" },
              ],
            },
          ],
        },
      ],
    },

    channels: {
      summary: "Messaging channel integrations and shared outbound reliability knobs.",
      fields: [
        {
          key: "outbound",
          label: "Outbound reliability",
          type: "object",
          fields: [
            { key: "max_attempts", label: "Max attempts", type: "number", min: 1 },
            { key: "base_delay_ms", label: "Base delay (ms)", type: "number", min: 1 },
            { key: "max_delay_ms", label: "Max delay (ms)", type: "number", min: 1 },
            { key: "jitter_percent", label: "Jitter (0..1)", type: "number", step: "0.05", min: 0, max: 1 },
            { key: "breaker_threshold", label: "Breaker threshold", type: "number", min: 1 },
            { key: "breaker_cooldown_s", label: "Breaker cooldown (s)", type: "number", min: 1 },
            { key: "dlq_path", label: "DLQ path", type: "text" },
          ],
        },
        {
          key: "slack",
          label: "Slack",
          type: "nullable-object",
          fields: [
            { key: "bot_token", label: "Bot token", type: "password", sensitive: true },
            { key: "app_token", label: "App token", type: "password", sensitive: true },
            {
              key: "dm_mode",
              label: "DM mode",
              type: "select",
              options: [
                { value: "", label: "(default)" },
                { value: "open", label: "open" },
                { value: "pairing", label: "pairing" },
                { value: "allowlist", label: "allowlist" },
                { value: "disabled", label: "disabled" },
              ],
            },
            { key: "allow_from", label: "Allow-from IDs", type: "tags" },
          ],
        },
        {
          key: "teams",
          label: "Microsoft Teams (Bot Framework)",
          type: "nullable-object",
          fields: [
            { key: "app_id", label: "Azure Bot App ID", type: "text" },
            { key: "app_password", label: "Azure Bot App password", type: "password", sensitive: true },
            { key: "listen_addr", label: "Webhook listen addr", type: "text" },
            {
              key: "dm_mode",
              label: "DM mode",
              type: "select",
              options: [
                { value: "", label: "(default)" },
                { value: "open", label: "open" },
                { value: "allowlist", label: "allowlist" },
                { value: "disabled", label: "disabled" },
              ],
            },
            {
              key: "allow_from",
              label: "Allow-from (Teams user / AAD object IDs)",
              type: "tags",
            },
          ],
        },
      ],
    },

    webhooks: {
      summary: "Inbound webhook endpoints. Adapters (typed) take precedence over generic mappings.",
      setupGuide: `
        <h3 class="setup-guide-title">Webhooks vs DevOps tokens</h3>
        <ul>
          <li><strong>Generic webhook token</strong> below applies only to <em>legacy</em> generic mappings (header check), not the GitHub adapter.</li>
          <li><strong>GitHub adapter → secret</strong> must match GitHub’s webhook <strong>Signing secret</strong> (HMAC). Set the same value in <code>OPSINTEL_GITHUB_WEBHOOK_SECRET</code>.</li>
          <li>In GitHub: Settings → Webhooks → Payload URL <code>https://&lt;your-host&gt;/api/webhook/github</code>, content type <strong>application/json</strong>.</li>
        </ul>`,
      fields: [
        { key: "enabled", label: "Enabled", type: "checkbox" },
        {
          key: "token",
          label: "Generic webhook token",
          type: "password",
          sensitive: true,
          help: "Shared secret checked for legacy generic mappings only.",
        },
        { key: "max_concurrent", label: "Max concurrent runs", type: "number", min: 0 },
        { key: "timeout", label: "Per-run timeout", type: "duration", help: "e.g. 10m" },
        {
          key: "adapters",
          label: "Adapters",
          type: "object",
          fields: [
            {
              key: "github",
              label: "GitHub adapter",
              type: "object",
              fields: [
                { key: "enabled", label: "Enabled", type: "checkbox" },
                { key: "secret", label: "HMAC secret", type: "password", sensitive: true },
                { key: "path", label: "URL suffix", type: "text", help: "default: github" },
                { key: "default_prompt", label: "Default prompt", type: "textarea" },
                { key: "events", label: "Event allowlist (event → actions)", type: "kv-tags" },
                { key: "prompts", label: "Per-event prompt templates", type: "kv-textarea" },
                { key: "max_concurrent", label: "Max concurrent runs", type: "number", min: 0 },
                { key: "timeout", label: "Per-run timeout", type: "duration" },
                {
                  key: "allow_unverified",
                  label: "Allow unverified (testing only!)",
                  type: "checkbox",
                  help: "Bypasses HMAC. Never enable in production.",
                },
              ],
            },
          ],
        },
      ],
    },

    mcp: {
      summary:
        "Built-in MCP server and external MCP client connections. " +
        "Add/remove clients to attach external tool servers to the agent.",
      custom: {
        render: renderMCPSection,
        serialize: serializeMCPSection,
      },
    },

    providers: {
      summary:
        "LLM provider credentials. API keys are write-only — leaving a field " +
        "blank keeps the existing value untouched.",
      custom: {
        render: renderProvidersSection,
        serialize: serializeProvidersSection,
      },
    },
  };

  // sectionState holds the most recent revision/data per section, used
  // by the save flow's If-Match header and dirty-check.
  const sectionState = {};

  async function loadSettingsSection(section) {
    const body = document.getElementById("settings-body");
    body.innerHTML = `<div class="loading">Loading ${escapeHTML(section)}…</div>`;

    let data;
    try {
      data = await fetchJSON(`${API}/config/${encodeURIComponent(section)}`);
    } catch (err) {
      body.innerHTML = errorBlock(`Failed to load ${section}: ${err.message}`);
      return;
    }

    sectionState[section] = { revision: data.revision || "", original: data.config };
    renderSettingsForm(section, data.config, body);
  }

  function renderSettingsForm(section, value, container) {
    const schema = CONFIG_SCHEMA[section];
    if (!schema) {
      container.innerHTML = errorBlock(`No schema for section "${section}".`);
      return;
    }
    const root = document.createElement("div");
    root.className = "section-form";

    const header = document.createElement("div");
    header.className = "section-header";
    header.innerHTML = `
      <h2>${escapeHTML(prettySection(section))}</h2>
      <p class="section-summary">${escapeHTML(schema.summary || "")}</p>`;
    root.appendChild(header);

    if (schema.setupGuide) {
      const guide = document.createElement("aside");
      guide.className = "setup-guide";
      guide.setAttribute("role", "note");
      guide.innerHTML = schema.setupGuide;
      root.appendChild(guide);
    }

    const form = document.createElement("form");
    form.className = "config-form";
    form.dataset.section = section;
    form.addEventListener("submit", (ev) => {
      ev.preventDefault();
      saveSettingsForm(section, form, root);
    });

    if (schema.custom) {
      schema.custom.render(form, value, schema);
    } else {
      schema.fields.forEach((field) => {
        form.appendChild(renderField(field, value || {}, []));
      });
    }

    const footer = document.createElement("div");
    footer.className = "form-footer";
    footer.innerHTML = `
      <span class="rev-token" title="Optimistic concurrency token">rev: <code>${escapeHTML(
        sectionState[section].revision || "(none)"
      )}</code></span>
      <div class="form-buttons">
        <button type="button" class="ghost" data-action="reload">Reload</button>
        <button type="submit" class="primary">Save changes</button>
      </div>`;
    form.appendChild(footer);
    footer.querySelector("[data-action=reload]").addEventListener("click", () => {
      loadSettingsSection(section);
    });

    root.appendChild(form);
    container.innerHTML = "";
    container.appendChild(root);
  }

  async function saveSettingsForm(section, form, root) {
    const schema = CONFIG_SCHEMA[section];
    let payload;
    try {
      if (schema.custom) {
        payload = schema.custom.serialize(form, sectionState[section]?.original);
      } else {
        payload = serializeFields(schema.fields, form, sectionState[section]?.original || {});
      }
    } catch (err) {
      showToast(`Invalid input: ${err.message}`, "error");
      return;
    }

    const submitBtn = form.querySelector("button[type=submit]");
    if (submitBtn) submitBtn.disabled = true;
    try {
      const res = await fetch(`${API}/config/${encodeURIComponent(section)}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/json",
          "If-Match": sectionState[section]?.revision || "",
          ...csrfHeaders(),
        },
        body: JSON.stringify(payload),
      });
      const body = await res.json().catch(() => ({}));
      if (res.status === 409) {
        showToast(
          "Saved by someone else since you loaded this page. Reload to see the new values.",
          "warn",
        );
        return;
      }
      if (!res.ok) {
        throw new Error(body.error || `save failed (${res.status})`);
      }
      showToast(`Saved ${prettySection(section)}.`, "ok");
      sectionState[section].revision = body.revision || sectionState[section].revision;
      // Refresh from server so secret masks/defaults show through.
      loadSettingsSection(section);
    } catch (err) {
      showToast(err.message || "save failed", "error");
    } finally {
      if (submitBtn) submitBtn.disabled = false;
    }
  }

  // ─────────────────────── field renderer ───────────────────────

  function renderField(field, parent, path) {
    const fieldPath = [...path, field.key];
    const value = parent ? parent[field.key] : undefined;

    const wrap = document.createElement("div");
    wrap.className = `field field-${field.type}`;
    wrap.dataset.path = fieldPath.join(".");

    const labelText = field.label || field.key;

    switch (field.type) {
      case "object":
      case "nullable-object": {
        const fs = document.createElement("fieldset");
        fs.className = "field-group";
        const lg = document.createElement("legend");
        lg.textContent = labelText;
        fs.appendChild(lg);
        const inner = value || (field.type === "nullable-object" ? null : {});
        if (field.type === "nullable-object") {
          const enableLabel = document.createElement("label");
          enableLabel.className = "checkbox-row";
          const cb = document.createElement("input");
          cb.type = "checkbox";
          cb.name = `${fieldPath.join(".")}.__enabled`;
          cb.checked = !!inner;
          enableLabel.appendChild(cb);
          enableLabel.appendChild(document.createTextNode(` Enabled (${field.key})`));
          fs.appendChild(enableLabel);
          const inner2 = document.createElement("div");
          inner2.className = "nullable-object-body";
          inner2.style.display = inner ? "" : "none";
          field.fields.forEach((f) => inner2.appendChild(renderField(f, inner || {}, fieldPath)));
          cb.addEventListener("change", () => {
            inner2.style.display = cb.checked ? "" : "none";
          });
          fs.appendChild(inner2);
        } else {
          field.fields.forEach((f) => fs.appendChild(renderField(f, inner, fieldPath)));
        }
        wrap.appendChild(fs);
        return wrap;
      }
      case "checkbox": {
        const lab = document.createElement("label");
        lab.className = "checkbox-row";
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.name = fieldPath.join(".");
        cb.checked = value === true;
        lab.appendChild(cb);
        lab.appendChild(document.createTextNode(` ${labelText}`));
        wrap.appendChild(lab);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "checkbox-tri": {
        // Tri-state: "(default)" | true | false. Lets us preserve nil
        // in YAML and let backend defaults kick in.
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const sel = document.createElement("select");
        sel.name = fieldPath.join(".");
        ["", "true", "false"].forEach((v) => {
          const opt = document.createElement("option");
          opt.value = v;
          opt.textContent = v === "" ? "(default)" : v;
          sel.appendChild(opt);
        });
        sel.value = value === true ? "true" : value === false ? "false" : "";
        wrap.appendChild(sel);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "select": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const sel = document.createElement("select");
        sel.name = fieldPath.join(".");
        (field.options || []).forEach((o) => {
          const opt = document.createElement("option");
          opt.value = o.value;
          opt.textContent = o.label;
          sel.appendChild(opt);
        });
        sel.value = value == null ? "" : String(value);
        wrap.appendChild(sel);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "number": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const inp = document.createElement("input");
        inp.type = "number";
        inp.name = fieldPath.join(".");
        if (field.min != null) inp.min = String(field.min);
        if (field.max != null) inp.max = String(field.max);
        if (field.step != null) inp.step = String(field.step);
        inp.value = value == null ? "" : String(value);
        wrap.appendChild(inp);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "textarea": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const ta = document.createElement("textarea");
        ta.name = fieldPath.join(".");
        ta.rows = 4;
        ta.value = value == null ? "" : String(value);
        wrap.appendChild(ta);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "tags": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const inp = document.createElement("input");
        inp.type = "text";
        inp.name = fieldPath.join(".");
        inp.placeholder = "comma-separated";
        inp.value = Array.isArray(value) ? value.join(", ") : "";
        inp.dataset.kind = "tags";
        wrap.appendChild(inp);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "kv-tags": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const ta = document.createElement("textarea");
        ta.name = fieldPath.join(".");
        ta.dataset.kind = "kv-tags";
        ta.rows = 4;
        ta.placeholder = "key=value1,value2\nother_key=*";
        ta.value = kvTagsToText(value);
        wrap.appendChild(ta);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "kv-textarea": {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const ta = document.createElement("textarea");
        ta.name = fieldPath.join(".");
        ta.dataset.kind = "kv-textarea";
        ta.rows = 6;
        ta.placeholder = "===key1===\nfirst template body\n===key2===\nsecond template body";
        ta.value = kvTextAreaToText(value);
        wrap.appendChild(ta);
        if (field.help) wrap.appendChild(helpEl(field.help));
        return wrap;
      }
      case "duration":
      case "text":
      case "password":
      default: {
        const lab = document.createElement("label");
        lab.textContent = labelText;
        wrap.appendChild(lab);
        const inp = document.createElement("input");
        inp.type = field.type === "password" ? "password" : "text";
        inp.name = fieldPath.join(".");
        if (field.sensitive) {
          inp.placeholder = "(leave blank to keep current value)";
          inp.value = "";
          inp.dataset.sensitive = "1";
        } else {
          inp.value = value == null ? "" : String(value);
        }
        wrap.appendChild(inp);
        if (field.help) wrap.appendChild(helpEl(field.help));
        if (field.sensitive) {
          wrap.appendChild(helpEl("Stored value is hidden by the server. Leave blank to keep it."));
        }
        return wrap;
      }
    }
  }

  function helpEl(text) {
    const el = document.createElement("p");
    el.className = "field-help";
    el.textContent = text;
    return el;
  }

  // ─────────────────────── serializer ───────────────────────

  function serializeFields(fields, form, originalParent) {
    const out = {};
    for (const field of fields) {
      const original = originalParent ? originalParent[field.key] : undefined;
      const v = serializeField(field, form, [field.key], original);
      if (v !== OMIT) out[field.key] = v;
    }
    return out;
  }

  // OMIT signals "drop this key from the payload" (used when a
  // sensitive field was left blank — we resend the original instead).
  const OMIT = Symbol("omit");

  function serializeField(field, form, path, originalValue) {
    const name = path.join(".");
    switch (field.type) {
      case "object": {
        const out = {};
        for (const sub of field.fields) {
          const subOrig = originalValue ? originalValue[sub.key] : undefined;
          const v = serializeField(sub, form, [...path, sub.key], subOrig);
          if (v !== OMIT) out[sub.key] = v;
        }
        return out;
      }
      case "nullable-object": {
        const cb = form.querySelector(`[name="${cssEscape(name)}.__enabled"]`);
        if (cb && !cb.checked) return null;
        const out = {};
        for (const sub of field.fields) {
          const subOrig = originalValue ? originalValue[sub.key] : undefined;
          const v = serializeField(sub, form, [...path, sub.key], subOrig);
          if (v !== OMIT) out[sub.key] = v;
        }
        return out;
      }
      case "checkbox": {
        const cb = form.querySelector(`[name="${cssEscape(name)}"]`);
        return !!(cb && cb.checked);
      }
      case "checkbox-tri": {
        const sel = form.querySelector(`[name="${cssEscape(name)}"]`);
        if (!sel || sel.value === "") return null;
        return sel.value === "true";
      }
      case "select": {
        const sel = form.querySelector(`[name="${cssEscape(name)}"]`);
        return sel ? sel.value : "";
      }
      case "number": {
        const inp = form.querySelector(`[name="${cssEscape(name)}"]`);
        if (!inp || inp.value === "") return 0;
        const n = Number(inp.value);
        if (!Number.isFinite(n)) throw new Error(`${name} must be numeric`);
        return n;
      }
      case "tags": {
        const inp = form.querySelector(`[name="${cssEscape(name)}"]`);
        if (!inp) return [];
        const raw = inp.value || "";
        return raw.split(",").map((s) => s.trim()).filter(Boolean);
      }
      case "kv-tags": {
        const ta = form.querySelector(`[name="${cssEscape(name)}"]`);
        return parseKVTags(ta ? ta.value : "");
      }
      case "kv-textarea": {
        const ta = form.querySelector(`[name="${cssEscape(name)}"]`);
        return parseKVTextarea(ta ? ta.value : "");
      }
      case "textarea": {
        const ta = form.querySelector(`[name="${cssEscape(name)}"]`);
        return ta ? ta.value : "";
      }
      case "password":
      case "duration":
      case "text":
      default: {
        const inp = form.querySelector(`[name="${cssEscape(name)}"]`);
        const raw = inp ? inp.value : "";
        if (field.sensitive && raw === "") {
          // Blank sensitive field → re-send the original so we don't
          // accidentally clear a server-redacted secret on save.
          return originalValue == null ? "" : originalValue;
        }
        return raw;
      }
    }
  }

  // ─────────────────────── custom: providers ───────────────────────

  // The Providers section is a flat catalogue of cloud + local LLM
  // providers. Each provider is independently nullable. We render a
  // collapsible card per provider with its own "Enabled" toggle.
  const PROVIDER_DEFS = [
    { key: "openai", label: "OpenAI", kind: "cloud" },
    { key: "anthropic", label: "Anthropic", kind: "cloud" },
    { key: "groq", label: "Groq", kind: "cloud" },
    { key: "mistral", label: "Mistral", kind: "cloud" },
    { key: "together", label: "Together", kind: "cloud" },
    { key: "nvidia", label: "NVIDIA", kind: "cloud" },
    { key: "cohere", label: "Cohere", kind: "cloud" },
    { key: "deepseek", label: "DeepSeek", kind: "cloud" },
    { key: "perplexity", label: "Perplexity", kind: "cloud" },
    { key: "xai", label: "xAI", kind: "cloud" },
    { key: "voyage", label: "Voyage", kind: "cloud" },
    { key: "azure_openai", label: "Azure OpenAI", kind: "azure" },
    { key: "openrouter", label: "OpenRouter", kind: "openrouter" },
    { key: "huggingface", label: "HuggingFace", kind: "huggingface" },
    { key: "bedrock", label: "AWS Bedrock", kind: "bedrock" },
    { key: "vertex", label: "Google Vertex / Gemini", kind: "vertex" },
    { key: "ollama", label: "Ollama (local)", kind: "local" },
    { key: "vllm", label: "vLLM (local)", kind: "local" },
    { key: "lm_studio", label: "LM Studio (local)", kind: "local" },
  ];

  function renderProvidersSection(form, value) {
    const value0 = value || {};
    PROVIDER_DEFS.forEach((p) => {
      const cur = value0[p.key];
      form.appendChild(renderProviderCard(p, cur));
    });
  }

  function renderProviderCard(def, cur) {
    const fs = document.createElement("fieldset");
    fs.className = "field-group provider-card";
    fs.dataset.provider = def.key;

    const lg = document.createElement("legend");
    lg.textContent = def.label;
    fs.appendChild(lg);

    const enableLabel = document.createElement("label");
    enableLabel.className = "checkbox-row";
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.name = `providers.${def.key}.__enabled`;
    cb.checked = cur != null;
    enableLabel.appendChild(cb);
    enableLabel.appendChild(document.createTextNode(" Configured"));
    fs.appendChild(enableLabel);

    const body = document.createElement("div");
    body.className = "nullable-object-body";
    body.style.display = cur ? "" : "none";

    providerFields(def).forEach((f) => {
      body.appendChild(renderField(f, cur || {}, ["providers", def.key]));
    });
    fs.appendChild(body);

    cb.addEventListener("change", () => {
      body.style.display = cb.checked ? "" : "none";
    });
    return fs;
  }

  function providerFields(def) {
    switch (def.kind) {
      case "cloud":
        return [
          { key: "api_key", label: "API key", type: "password", sensitive: true },
          { key: "base_url", label: "Base URL", type: "text" },
          { key: "default_model", label: "Default model", type: "text" },
        ];
      case "azure":
        return [
          { key: "api_key", label: "API key", type: "password", sensitive: true },
          { key: "base_url", label: "Endpoint", type: "text" },
          { key: "default_model", label: "Default model/deployment", type: "text" },
          { key: "api_version", label: "API version", type: "text" },
        ];
      case "openrouter":
        return [
          { key: "api_key", label: "API key", type: "password", sensitive: true },
          { key: "base_url", label: "Base URL", type: "text" },
          { key: "default_model", label: "Default model", type: "text" },
          { key: "site_name", label: "Site name", type: "text" },
          { key: "site_url", label: "Site URL", type: "text" },
        ];
      case "huggingface":
        return [
          { key: "api_key", label: "API key", type: "password", sensitive: true },
          { key: "base_url", label: "Base URL", type: "text" },
          { key: "default_model", label: "Default model", type: "text" },
          { key: "model", label: "Specific model endpoint", type: "text" },
        ];
      case "bedrock":
        return [
          { key: "region", label: "Region", type: "text" },
          { key: "profile", label: "AWS profile", type: "text" },
          { key: "access_key_id", label: "Access key ID", type: "text" },
          { key: "secret_access_key", label: "Secret access key", type: "password", sensitive: true },
          { key: "api_key", label: "API key (alt)", type: "password", sensitive: true },
          { key: "default_model", label: "Default model", type: "text" },
        ];
      case "vertex":
        return [
          { key: "project_id", label: "Project ID", type: "text" },
          { key: "location", label: "Location", type: "text" },
          { key: "credentials", label: "Service account JSON path", type: "text" },
          { key: "default_model", label: "Default model", type: "text", help: "e.g. gemini-2.5-flash, gemini-2.5-pro" },
        ];
      case "local":
      default:
        return [
          { key: "base_url", label: "Base URL", type: "text" },
          { key: "api_key", label: "API key (optional)", type: "password", sensitive: true },
          { key: "default_model", label: "Default model", type: "text" },
        ];
    }
  }

  function serializeProvidersSection(form, original) {
    const orig = original || {};
    const out = {};
    PROVIDER_DEFS.forEach((def) => {
      const cb = form.querySelector(`[name="providers.${def.key}.__enabled"]`);
      if (!cb || !cb.checked) {
        out[def.key] = null;
        return;
      }
      const inner = {};
      providerFields(def).forEach((f) => {
        const subOrig = orig[def.key] ? orig[def.key][f.key] : undefined;
        const v = serializeField(f, form, ["providers", def.key, f.key], subOrig);
        if (v !== OMIT) inner[f.key] = v;
      });
      out[def.key] = inner;
    });
    return out;
  }

  // ─────────────────────── custom: mcp ───────────────────────

  function renderMCPSection(form, value) {
    const v = value || {};

    const serverFs = document.createElement("fieldset");
    serverFs.className = "field-group";
    serverFs.innerHTML = `<legend>MCP server (built-in)</legend>`;
    const serverFields = [
      { key: "enabled", label: "Enabled", type: "checkbox" },
      {
        key: "transport",
        label: "Transport",
        type: "select",
        options: [
          { value: "", label: "(default stdio)" },
          { value: "stdio", label: "stdio" },
          { value: "http", label: "http" },
        ],
      },
      { key: "http_port", label: "HTTP port", type: "number", min: 0, max: 65535 },
      { key: "auth_token", label: "HTTP auth token", type: "password", sensitive: true },
    ];
    serverFields.forEach((f) =>
      serverFs.appendChild(renderField(f, v.server || {}, ["server"]))
    );
    form.appendChild(serverFs);

    const clientsFs = document.createElement("fieldset");
    clientsFs.className = "field-group mcp-clients";
    clientsFs.innerHTML = `<legend>MCP clients (external servers)</legend>`;
    const list = document.createElement("div");
    list.className = "mcp-client-list";
    clientsFs.appendChild(list);

    const addBtn = document.createElement("button");
    addBtn.type = "button";
    addBtn.className = "ghost";
    addBtn.textContent = "+ Add client";
    clientsFs.appendChild(addBtn);

    (v.clients || []).forEach((c, i) => list.appendChild(renderMCPClient(c, i)));
    addBtn.addEventListener("click", () => {
      const i = list.querySelectorAll(".mcp-client").length;
      const node = renderMCPClient({}, i);
      list.appendChild(node);
    });

    form.appendChild(clientsFs);
  }

  function renderMCPClient(client, idx) {
    const card = document.createElement("div");
    card.className = "mcp-client";
    card.dataset.idx = String(idx);
    const fields = [
      { key: "name", label: "Name", type: "text" },
      {
        key: "transport",
        label: "Transport",
        type: "select",
        options: [
          { value: "stdio", label: "stdio" },
          { value: "http", label: "http" },
        ],
      },
      { key: "command", label: "Command (stdio)", type: "text" },
      { key: "args", label: "Args (stdio)", type: "tags" },
      { key: "dir", label: "Working dir (stdio)", type: "text" },
      { key: "env", label: "Env vars (KEY=value)", type: "tags" },
      { key: "url", label: "URL (http)", type: "text" },
      { key: "auth_token", label: "Auth token", type: "password", sensitive: true },
    ];
    fields.forEach((f) =>
      card.appendChild(renderField(f, client || {}, ["clients", String(idx)]))
    );
    const removeBtn = document.createElement("button");
    removeBtn.type = "button";
    removeBtn.className = "ghost danger";
    removeBtn.textContent = "Remove client";
    removeBtn.addEventListener("click", () => card.remove());
    card.appendChild(removeBtn);
    return card;
  }

  function serializeMCPSection(form, original) {
    const orig = original || {};
    const server = {};
    [
      { key: "enabled", type: "checkbox" },
      { key: "transport", type: "select" },
      { key: "http_port", type: "number" },
      { key: "auth_token", type: "password", sensitive: true },
    ].forEach((f) => {
      const subOrig = orig.server ? orig.server[f.key] : undefined;
      const v = serializeField(f, form, ["server", f.key], subOrig);
      if (v !== OMIT) server[f.key] = v;
    });

    const clients = [];
    const cards = form.querySelectorAll(".mcp-client");
    const origClients = Array.isArray(orig.clients) ? orig.clients : [];
    cards.forEach((card) => {
      const idx = card.dataset.idx;
      const subOrig = origClients[Number(idx)] || {};
      const fields = [
        { key: "name", type: "text" },
        { key: "transport", type: "select" },
        { key: "command", type: "text" },
        { key: "args", type: "tags" },
        { key: "dir", type: "text" },
        { key: "env", type: "tags" },
        { key: "url", type: "text" },
        { key: "auth_token", type: "password", sensitive: true },
      ];
      const obj = {};
      fields.forEach((f) => {
        const fOrig = subOrig[f.key];
        const v = serializeField(f, form, ["clients", idx, f.key], fOrig);
        if (v !== OMIT) obj[f.key] = v;
      });
      // Drop entries with no name to avoid silently writing junk.
      if (obj.name && obj.name.trim()) clients.push(obj);
    });

    return { server, clients };
  }

  // ─────────────────────── users & roles view ───────────────────────
  //
  // Panel layout: a "Invite user" action in the header + a table of
  // users with inline actions (disable/enable, edit, grant/revoke
  // role, delete). Every action goes through the /api/v1/users/* and
  // /api/v1/users/<id>/roles/* endpoints (phase 3d backend) and is
  // permission-gated on the server, so the UI is allowed to be
  // optimistic; we surface failures via the toast.

  let ROLES_CACHE = null; // resolved once per dashboard load

  async function renderUsersView(actionsEl) {
    const body = document.getElementById("users-body");
    if (!body) return;
    body.innerHTML = `<div class="loading">Loading users…</div>`;

    const canManage = meHasPerm("users.manage");
    const canDelete = meHasPerm("users.delete");
    const canGrantRole = meHasPerm("roles.manage");

    if (canManage && actionsEl) {
      const btn = document.createElement("button");
      btn.className = "primary";
      btn.textContent = "Invite user";
      btn.addEventListener("click", () => openInviteUserModal());
      actionsEl.appendChild(btn);
    }

    try {
      const roles = await getRolesCached();
      const data = await fetchJSON(`${API}/users`);
      const users = Array.isArray(data.users) ? data.users : [];
      body.innerHTML = `
        <div class="admin-table-wrap">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Username</th>
                <th>Email</th>
                <th>Status</th>
                <th>Roles</th>
                <th>Last login</th>
                <th class="col-actions">Actions</th>
              </tr>
            </thead>
            <tbody id="users-tbody"></tbody>
          </table>
        </div>
      `;
      const tbody = document.getElementById("users-tbody");
      users.forEach((u) => {
        const tr = document.createElement("tr");
        tr.dataset.userId = u.id;
        tr.innerHTML = `
          <td>
            <div class="primary-cell">${escapeHTML(u.username)}</div>
            ${
              u.display_name
                ? `<div class="secondary-cell">${escapeHTML(u.display_name)}</div>`
                : ""
            }
          </td>
          <td>${escapeHTML(u.email || "—")}</td>
          <td><span class="pill pill-${escapeHTML(u.status)}">${escapeHTML(u.status)}</span></td>
          <td>${renderRoleChips(u.roles || [])}</td>
          <td class="mono-cell">${u.last_login_at ? formatDate(u.last_login_at) : "—"}</td>
          <td class="col-actions"></td>
        `;
        const actions = tr.querySelector(".col-actions");
        if (canManage) {
          actions.appendChild(makeButton("Edit", "ghost", () => openEditUserModal(u, roles)));
          actions.appendChild(
            makeButton(u.status === "disabled" ? "Enable" : "Disable", "ghost", async () => {
              const next = u.status === "disabled" ? "active" : "disabled";
              const ok = await patchUser(u.id, { status: next });
              if (ok) reroute();
            })
          );
        }
        if (canGrantRole) {
          actions.appendChild(
            makeButton("Roles", "ghost", () => openManageRolesModal(u, roles))
          );
        }
        if (canDelete && (ME && ME.user_id !== u.id)) {
          actions.appendChild(
            makeButton("Delete", "ghost danger", async () => {
              if (!confirm(`Delete user "${u.username}"? This cannot be undone.`)) return;
              try {
                await sendJSON(`${API}/users/${encodeURIComponent(u.id)}`, "DELETE");
                showToast(`Deleted ${u.username}`, "ok");
                reroute();
              } catch (err) {
                showToast(err.message || "delete failed", "error");
              }
            })
          );
        }
        tbody.appendChild(tr);
      });
      if (!users.length) {
        body.innerHTML += `<p class="note">No users yet.</p>`;
      }
    } catch (err) {
      body.innerHTML = errorBlock(err.message || "failed to load users");
    }
  }

  async function getRolesCached() {
    if (ROLES_CACHE) return ROLES_CACHE;
    try {
      const data = await fetchJSON(`${API}/roles`);
      ROLES_CACHE = Array.isArray(data.roles) ? data.roles : [];
    } catch (err) {
      ROLES_CACHE = [];
      // If the caller lacks roles.read we'll still render the users
      // table; only the role picker is degraded.
    }
    return ROLES_CACHE;
  }

  function renderRoleChips(roleNames) {
    if (!roleNames.length) return `<span class="chip chip-muted">none</span>`;
    return roleNames
      .map((n) => `<span class="chip chip-role">${escapeHTML(n)}</span>`)
      .join(" ");
  }

  async function patchUser(id, body) {
    try {
      await sendJSON(`${API}/users/${encodeURIComponent(id)}`, "PATCH", body);
      showToast("updated", "ok");
      return true;
    } catch (err) {
      showToast(err.message || "update failed", "error");
      return false;
    }
  }

  function openInviteUserModal() {
    const modal = openModal({ title: "Invite user" });
    modal.body.innerHTML = `
      <form id="invite-form" class="modal-form">
        <div class="field">
          <label>Username</label>
          <input name="username" type="text" autocomplete="off" required />
        </div>
        <div class="field">
          <label>Email</label>
          <input name="email" type="email" autocomplete="off" />
        </div>
        <div class="field">
          <label>Display name</label>
          <input name="display_name" type="text" autocomplete="off" />
        </div>
        <div class="field">
          <label>Initial password</label>
          <input name="password" type="password" autocomplete="new-password" required />
          <p class="field-help">Share over a secure channel; prompt the user to change on first login.</p>
        </div>
        <div class="field">
          <label>Role</label>
          <select name="role" id="invite-role"></select>
        </div>
      </form>
    `;
    const select = modal.body.querySelector("#invite-role");
    getRolesCached().then((roles) => {
      const opts = [`<option value="">(none)</option>`];
      roles.forEach((r) => {
        opts.push(
          `<option value="${escapeHTML(r.name)}">${escapeHTML(r.name)}${
            r.is_builtin ? "" : " (custom)"
          }</option>`
        );
      });
      select.innerHTML = opts.join("");
      select.value = "viewer";
    });
    setModalFooter(modal, [
      { label: "Cancel", kind: "ghost", onClick: () => closeModal(modal) },
      {
        label: "Create",
        kind: "primary",
        onClick: async () => {
          const form = modal.body.querySelector("#invite-form");
          const fd = new FormData(form);
          const role = String(fd.get("role") || "").trim();
          const payload = {
            username: String(fd.get("username") || "").trim(),
            email: String(fd.get("email") || "").trim(),
            display_name: String(fd.get("display_name") || "").trim(),
            password: String(fd.get("password") || ""),
          };
          if (role) payload.roles = [role];
          if (!payload.username || !payload.password) {
            showToast("username and password required", "warn");
            return;
          }
          try {
            await sendJSON(`${API}/users`, "POST", payload);
            closeModal(modal);
            showToast(`Invited ${payload.username}`, "ok");
            reroute();
          } catch (err) {
            showToast(err.message || "invite failed", "error");
          }
        },
      },
    ]);
  }

  function openEditUserModal(user, roles) {
    const modal = openModal({ title: `Edit ${user.username}` });
    modal.body.innerHTML = `
      <form id="edit-user-form" class="modal-form">
        <div class="field">
          <label>Display name</label>
          <input name="display_name" type="text" value="${escapeHTML(user.display_name || "")}" />
        </div>
        <div class="field">
          <label>Email</label>
          <input name="email" type="email" value="${escapeHTML(user.email || "")}" />
        </div>
        <div class="field">
          <label>Reset password</label>
          <input name="password" type="password" autocomplete="new-password" placeholder="Leave blank to keep current" />
          <p class="field-help">Only filled when you want to set a new password.</p>
        </div>
      </form>
    `;
    setModalFooter(modal, [
      { label: "Cancel", kind: "ghost", onClick: () => closeModal(modal) },
      {
        label: "Save",
        kind: "primary",
        onClick: async () => {
          const form = modal.body.querySelector("#edit-user-form");
          const fd = new FormData(form);
          const body = {
            email: String(fd.get("email") || "").trim(),
            display_name: String(fd.get("display_name") || "").trim(),
          };
          const pw = String(fd.get("password") || "");
          if (pw) body.password = pw;
          try {
            await sendJSON(`${API}/users/${encodeURIComponent(user.id)}`, "PATCH", body);
            closeModal(modal);
            showToast("saved", "ok");
            reroute();
          } catch (err) {
            showToast(err.message || "save failed", "error");
          }
        },
      },
    ]);
  }

  async function openManageRolesModal(user, roles) {
    const modal = openModal({ title: `Roles · ${user.username}` });
    modal.body.innerHTML = `<div class="loading">Loading roles…</div>`;
    try {
      const data = await fetchJSON(`${API}/users/${encodeURIComponent(user.id)}/roles`);
      const current = new Set((data.roles || []).map((r) => r.id));
      modal.body.innerHTML = `
        <p class="field-help">
          Grant or revoke a built-in role. Changes take effect at the next
          request the user makes — active sessions keep the roles they were
          issued until they log out.
        </p>
        <div id="role-matrix" class="role-matrix"></div>
      `;
      const matrix = modal.body.querySelector("#role-matrix");
      roles.forEach((r) => {
        const row = document.createElement("div");
        row.className = "role-row";
        const has = current.has(r.id);
        row.innerHTML = `
          <div>
            <div class="primary-cell">${escapeHTML(r.name)}${
              r.is_builtin ? "" : ` <span class="chip chip-muted">custom</span>`
            }</div>
            <div class="secondary-cell">${escapeHTML(r.description || "")}</div>
          </div>
        `;
        const toggle = makeButton(has ? "Revoke" : "Grant", has ? "ghost danger" : "primary", async () => {
          try {
            if (has) {
              await sendJSON(
                `${API}/users/${encodeURIComponent(user.id)}/roles/${encodeURIComponent(r.id)}`,
                "DELETE"
              );
            } else {
              await sendJSON(`${API}/users/${encodeURIComponent(user.id)}/roles`, "POST", {
                role: r.id,
              });
            }
            showToast(has ? `Revoked ${r.name}` : `Granted ${r.name}`, "ok");
            closeModal(modal);
            reroute();
          } catch (err) {
            showToast(err.message || "role change failed", "error");
          }
        });
        row.appendChild(toggle);
        matrix.appendChild(row);
      });
    } catch (err) {
      modal.body.innerHTML = errorBlock(err.message || "failed to load roles");
    }
    setModalFooter(modal, [{ label: "Close", kind: "ghost", onClick: () => closeModal(modal) }]);
  }

  // ─────────────────────── api keys view ───────────────────────

  async function renderAPIKeysView(actionsEl) {
    const body = document.getElementById("apikeys-body");
    if (!body) return;
    body.innerHTML = `<div class="loading">Loading API keys…</div>`;

    const canManageAny =
      meHasPerm("apikeys.manage.own") || meHasPerm("apikeys.manage.all");
    const canReadAll = meHasPerm("apikeys.read.all");
    const canManageAll = meHasPerm("apikeys.manage.all");

    if (canManageAny && actionsEl) {
      const btn = document.createElement("button");
      btn.className = "primary";
      btn.textContent = "Mint new key";
      btn.addEventListener("click", () => openMintKeyModal(canManageAll));
      actionsEl.appendChild(btn);
    }

    try {
      const data = await fetchJSON(`${API}/apikeys${canReadAll ? "" : "?mine=1"}`);
      const keys = Array.isArray(data.keys) ? data.keys : [];
      body.innerHTML = `
        <div class="admin-table-wrap">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Key ID</th>
                <th>Name</th>
                <th>Owner</th>
                <th>Status</th>
                <th>Created</th>
                <th>Expires</th>
                <th>Last used</th>
                <th class="col-actions">Actions</th>
              </tr>
            </thead>
            <tbody id="apikeys-tbody"></tbody>
          </table>
        </div>
      `;
      const tbody = document.getElementById("apikeys-tbody");
      keys.forEach((k) => {
        const isOwn = ME && ME.user_id === k.user_id;
        const canRevoke = (isOwn && meHasPerm("apikeys.manage.own")) || canManageAll;
        const tr = document.createElement("tr");
        tr.dataset.keyId = k.key_id;
        tr.innerHTML = `
          <td class="mono-cell">${escapeHTML(k.key_id)}</td>
          <td>${escapeHTML(k.name)}</td>
          <td>${escapeHTML(k.username || k.user_id)}</td>
          <td><span class="pill pill-${escapeHTML(k.status)}">${escapeHTML(k.status)}</span></td>
          <td class="mono-cell">${formatDate(k.created_at)}</td>
          <td class="mono-cell">${k.expires_at ? formatDate(k.expires_at) : "never"}</td>
          <td class="mono-cell">${k.last_used_at ? formatDate(k.last_used_at) : "never"}</td>
          <td class="col-actions"></td>
        `;
        const actions = tr.querySelector(".col-actions");
        if (canRevoke && k.status === "active") {
          actions.appendChild(
            makeButton("Revoke", "ghost danger", async () => {
              if (!confirm(`Revoke key ${k.key_id} (${k.name})?`)) return;
              try {
                await sendJSON(`${API}/apikeys/${encodeURIComponent(k.key_id)}`, "DELETE");
                showToast("revoked", "ok");
                reroute();
              } catch (err) {
                showToast(err.message || "revoke failed", "error");
              }
            })
          );
        }
        tbody.appendChild(tr);
      });
      if (!keys.length) {
        body.innerHTML += `<p class="note">No API keys${canReadAll ? "" : " (for you)"} yet.</p>`;
      }
    } catch (err) {
      body.innerHTML = errorBlock(err.message || "failed to load API keys");
    }
  }

  // ─────────────────────── repo intel view ───────────────────────

  async function renderReposView(actionsEl) {
    const body = document.getElementById("repos-body");
    if (!body) return;
    body.innerHTML = `<div class="loading">Loading repos…</div>`;
    actionsEl.innerHTML = "";
    const refreshBtn = document.createElement("button");
    refreshBtn.className = "primary";
    refreshBtn.textContent = "Refresh";
    refreshBtn.addEventListener("click", () => loadReposBody(body));
    actionsEl.appendChild(refreshBtn);

    await loadReposBody(body);
    reposPollId = setInterval(() => loadReposBody(body), 3000);
  }

  async function loadReposBody(body) {
    try {
      const data = await fetchJSON(`${API}/repos`);
      const repos = Array.isArray(data.repos) ? data.repos : [];
      if (!repos.length) {
        body.innerHTML = `<div class="placeholder"><h2>No repos configured</h2><p>Add one via CLI: <code>opsintelligence repos add owner/name --platform github</code></p></div>`;
        return;
      }
      body.innerHTML = `
        <div class="admin-table-wrap">
          <table class="admin-table">
            <thead>
              <tr>
                <th>Repo</th>
                <th>Platform</th>
                <th>Index</th>
                <th>Scan</th>
                <th>Risk</th>
                <th>Users</th>
                <th class="col-actions">Actions</th>
              </tr>
            </thead>
            <tbody id="repos-tbody"></tbody>
          </table>
        </div>
      `;
      const tbody = document.getElementById("repos-tbody");
      repos.forEach((r) => {
        const tr = document.createElement("tr");
        tr.style.cursor = "pointer";
        tr.innerHTML = `
          <td><div class="primary-cell">${escapeHTML(r.full_name || r.id || "—")}</div><div class="secondary-cell mono-cell">${escapeHTML(r.id || "—")}</div></td>
          <td>${escapeHTML(r.platform || "—")}</td>
          <td><span class="pill pill-${escapeHTML((r.index_status || "pending").toLowerCase())}">${escapeHTML(r.index_status || "pending")}</span></td>
          <td><span class="pill pill-${escapeHTML((r.scan_status || "pending").toLowerCase())}">${escapeHTML(r.scan_status || "pending")}</span></td>
          <td>${escapeHTML(r.risk_level || "—")}</td>
          <td>${Number(r.user_count || 0)}</td>
          <td class="col-actions"></td>
        `;
        tr.addEventListener("click", (ev) => {
          if (ev.target.closest("button")) return;
          clearReposPoll();
          renderRepoDetail(body, r);
        });
        const actions = tr.querySelector(".col-actions");
        actions.appendChild(
          makeButton("Sync", "ghost", async () => {
            try {
              await sendJSON(`${API}/repos/${encodeURIComponent(r.id)}/sync`, "POST");
              showToast(`Queued sync for ${r.full_name || r.id}`, "ok");
              loadReposBody(body);
            } catch (err) {
              showToast(err.message || "sync failed", "error");
            }
          })
        );
        tbody.appendChild(tr);
      });
    } catch (err) {
      body.innerHTML = errorBlock(err.message || "failed to load repos");
    }
  }

  async function renderRepoDetail(container, repo) {
    const enc = encodeURIComponent(repo.id);
    container.innerHTML = `<div class="loading">Loading repo details…</div>`;

    let scan = null;
    let memory = null;
    let userList = [];
    let fullRepo = repo;
    try {
      const [scanR, memR, usersR, repoR] = await Promise.allSettled([
        fetchJSON(`${API}/repos/${enc}/scan`),
        fetchJSON(`${API}/repos/${enc}/memory`),
        fetchJSON(`${API}/repos/${enc}/users`),
        fetchJSON(`${API}/repos/${enc}`),
      ]);
      scan = scanR.status === "fulfilled" ? scanR.value : null;
      memory = memR.status === "fulfilled" ? memR.value : null;
      if (usersR.status === "fulfilled" && usersR.value && Array.isArray(usersR.value.users)) {
        userList = usersR.value.users;
      }
      if (repoR.status === "fulfilled" && repoR.value) {
        fullRepo = repoR.value;
      }
    } catch (_) {}

    const riskClass = (fullRepo.risk_level || repo.risk_level || "").toLowerCase();
    const idxSt = fullRepo.index_status || repo.index_status || "pending";
    const scSt = fullRepo.scan_status || repo.scan_status || "pending";
    const fmtTime = (iso) => {
      if (!iso) return "";
      const d = new Date(iso);
      return Number.isNaN(d.getTime()) ? "" : d.toLocaleString();
    };
    const shortSha = (sha) => (sha && sha.length > 10 ? `${sha.slice(0, 7)}…` : sha || "—");
    const artifactLine = () => {
      const bits = [];
      if (fullRepo.memory_file) bits.push(`memory: <span class="mono-cell">${escapeHTML(fullRepo.memory_file)}</span>`);
      if (fullRepo.scan_file) bits.push(`scan: <span class="mono-cell">${escapeHTML(fullRepo.scan_file)}</span>`);
      if (fullRepo.ref_md_file) bits.push(`ref: <span class="mono-cell">${escapeHTML(fullRepo.ref_md_file)}</span>`);
      if (fullRepo.summary_md_file) bits.push(`summary: <span class="mono-cell">${escapeHTML(fullRepo.summary_md_file)}</span>`);
      if (fullRepo.call_graph_file) bits.push(`graph: <span class="mono-cell">${escapeHTML(fullRepo.call_graph_file)}</span>`);
      return bits.length ? `<div class="repo-detail-artifacts muted">${bits.join(" · ")}</div>` : "";
    };
    container.innerHTML = `
      <div class="repo-detail">
        <div class="repo-detail-header">
          <button id="repo-back-btn" class="ghost small">← Back</button>
          <h2>${escapeHTML(fullRepo.full_name || repo.full_name || repo.id)}</h2>
          <span class="secondary-cell mono-cell">${escapeHTML(fullRepo.id || repo.id)}</span>
        </div>
        <div class="repo-detail-meta">
          <span class="pill pill-${escapeHTML(String(idxSt).toLowerCase())}">Index: ${escapeHTML(String(idxSt))}</span>
          <span class="pill pill-${escapeHTML(String(scSt).toLowerCase())}">Scan: ${escapeHTML(String(scSt))}</span>
          ${fullRepo.risk_level || repo.risk_level ? `<span class="pill pill-${riskClass}">Risk: ${escapeHTML(fullRepo.risk_level || repo.risk_level)}</span>` : ""}
          <span class="pill">${escapeHTML(fullRepo.platform || repo.platform || "github")}</span>
        </div>
        <div class="repo-detail-facts muted">
          ${fullRepo.description ? `<p class="repo-detail-desc">${escapeHTML(fullRepo.description)}</p>` : ""}
          <p class="repo-detail-facts-row">
            ${fullRepo.language ? `<span>Detected language: <strong>${escapeHTML(fullRepo.language)}</strong></span> · ` : ""}
            ${fmtTime(fullRepo.indexed_at) ? `<span>Indexed: ${escapeHTML(fmtTime(fullRepo.indexed_at))}</span> · ` : ""}
            <span>HEAD <span class="mono-cell">${escapeHTML(shortSha(fullRepo.head_sha))}</span></span>
            ${fmtTime(fullRepo.scanned_at) ? ` · <span>Scanned: ${escapeHTML(fmtTime(fullRepo.scanned_at))}</span>` : ""}
          </p>
          ${fullRepo.index_error ? `<p class="repo-detail-err">Index error: ${escapeHTML(fullRepo.index_error)}</p>` : ""}
          ${fullRepo.scan_error ? `<p class="repo-detail-err">Scan error: ${escapeHTML(fullRepo.scan_error)}</p>` : ""}
          ${fullRepo.index_tree_truncated ? `<p class="repo-index-tree-note" role="status">GitHub returned a <strong>truncated</strong> directory tree on the last full-repo index. Search and RAG over the tree may be incomplete for very large repositories.</p>` : ""}
          ${artifactLine()}
        </div>
        <div class="repo-detail-actions" id="repo-detail-actions"></div>
        <div class="repo-tabs">
          <button class="tab-btn active" data-tab="scan">Scan Results</button>
          <button class="tab-btn" data-tab="memory">Index Memory</button>
          <button class="tab-btn" data-tab="graph">Call graph</button>
          <button class="tab-btn" data-tab="ask">Ask repo</button>
          <button class="tab-btn" data-tab="users">Users (${userList.length})</button>
        </div>
        <div id="repo-tab-content" class="repo-tab-content"></div>
      </div>
    `;

    document.getElementById("repo-back-btn").addEventListener("click", () => {
      reposPollId = setInterval(() => loadReposBody(container), 3000);
      loadReposBody(container);
    });

    const actionsDiv = document.getElementById("repo-detail-actions");
    actionsDiv.appendChild(makeButton("Sync now", "primary", async () => {
      try {
        await sendJSON(`${API}/repos/${enc}/sync`, "POST");
        showToast(`Queued sync for ${repo.full_name || repo.id}`, "ok");
      } catch (err) {
        showToast(err.message || "sync failed", "error");
      }
    }));

    const tabContent = document.getElementById("repo-tab-content");
    let graphView = null;
    function showTab(name) {
      if (graphView) {
        try {
          graphView.dispose();
        } catch (_) {
          /* ignore */
        }
        graphView = null;
      }
      container.querySelectorAll(".tab-btn").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
      if (name === "scan") {
        if (!scan) {
          tabContent.innerHTML = `<div class="placeholder"><p>No scan results yet. Trigger a sync to run a security scan.</p></div>`;
          return;
        }
        const cves = Array.isArray(scan.cves) ? scan.cves : [];
        const bneck = Array.isArray(scan.bottlenecks) ? scan.bottlenecks : [];
        const sugs = Array.isArray(scan.suggestions) ? scan.suggestions : [];
        const trunc = (s, n) => {
          const t = String(s || "");
          return t.length > n ? `${t.slice(0, n)}…` : t;
        };
        const cveRows = cves.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
            <thead><tr><th>Severity</th><th>Package</th><th>Version</th><th>CVEs</th><th>Description</th><th>Fix</th></tr></thead>
            <tbody>${cves
              .map(
                (c) => `<tr>
              <td><span class="pill pill-${escapeHTML((c.severity || "info").toLowerCase())}">${escapeHTML(c.severity || "—")}</span></td>
              <td class="mono-cell">${escapeHTML(c.package || "—")}</td>
              <td class="mono-cell">${escapeHTML(c.version || "—")}</td>
              <td class="mono-cell">${escapeHTML((c.cve_ids || []).join(", ") || "—")}</td>
              <td>${escapeHTML(trunc(c.description, 240))}</td>
              <td>${escapeHTML(trunc(c.fix, 120))}</td>
            </tr>`,
              )
              .join("")}</tbody></table></div>`
          : `<p class="muted">No CVE findings in this scan.</p>`;
        const bnRows = bneck.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
            <thead><tr><th>Severity</th><th>Location</th><th>Description</th><th>Fix</th></tr></thead>
            <tbody>${bneck
              .map(
                (b) => `<tr>
              <td><span class="pill pill-${escapeHTML((b.severity || "info").toLowerCase())}">${escapeHTML(b.severity || "—")}</span></td>
              <td class="mono-cell">${escapeHTML(b.location || "—")}</td>
              <td>${escapeHTML(trunc(b.description, 280))}</td>
              <td>${escapeHTML(trunc(b.fix, 120))}</td>
            </tr>`,
              )
              .join("")}</tbody></table></div>`
          : `<p class="muted">No bottleneck findings.</p>`;
        const sugRows = sugs.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
            <thead><tr><th>Priority</th><th>Area</th><th>Suggestion</th></tr></thead>
            <tbody>${sugs
              .map(
                (s) => `<tr>
              <td><span class="pill">${escapeHTML(s.priority || "—")}</span></td>
              <td>${escapeHTML(s.area || "—")}</td>
              <td>${escapeHTML(trunc(s.suggestion, 400))}</td>
            </tr>`,
              )
              .join("")}</tbody></table></div>`
          : `<p class="muted">No architecture suggestions.</p>`;
        tabContent.innerHTML = `
          <div class="scan-summary">
            <div class="scan-meta">
              <span>Risk: <strong>${escapeHTML(scan.risk_level || "—")}</strong></span>
              ${scan.scanned_at ? `<span>Scanned: ${escapeHTML(new Date(scan.scanned_at).toLocaleString())}</span>` : ""}
              <span>${cves.length} CVE row${cves.length !== 1 ? "s" : ""} · ${bneck.length} bottleneck${bneck.length !== 1 ? "s" : ""} · ${sugs.length} suggestion${sugs.length !== 1 ? "s" : ""}</span>
            </div>
          </div>
          ${scan.summary ? `<section class="memory-view"><h3>Scan summary</h3><p>${escapeHTML(scan.summary)}</p></section>` : ""}
          <section class="memory-view"><h3>CVEs</h3>${cveRows}</section>
          <section class="memory-view"><h3>Bottlenecks</h3>${bnRows}</section>
          <section class="memory-view"><h3>Architecture suggestions</h3>${sugRows}</section>
        `;
      } else if (name === "memory") {
        if (!memory) {
          tabContent.innerHTML = `<div class="placeholder"><p>No index memory yet. Trigger a sync to index this repository.</p></div>`;
          return;
        }
        const files = Array.isArray(memory.key_files) ? memory.key_files : [];
        const langs = Array.isArray(memory.languages) ? memory.languages : [];
        const convs = Array.isArray(memory.conventions) ? memory.conventions : [];
        const deps = Array.isArray(memory.dependencies) ? memory.dependencies : [];
        const issues = Array.isArray(memory.common_issues) ? memory.common_issues : [];
        const convRows = convs.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
            <thead><tr><th>Convention</th><th>Pattern</th></tr></thead>
            <tbody>${convs
              .map(
                (c) => `<tr><td>${escapeHTML(c.name || "—")}</td><td>${escapeHTML(c.pattern || "—")}</td></tr>`,
              )
              .join("")}</tbody></table></div>`
          : `<p class="muted">No conventions recorded.</p>`;
        const depRows = deps.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
            <thead><tr><th>Dependency</th><th>Version</th><th>Purpose</th></tr></thead>
            <tbody>${deps
              .map(
                (d) => `<tr>
              <td class="mono-cell">${escapeHTML(d.name || "—")}</td>
              <td class="mono-cell">${escapeHTML(d.version || "—")}</td>
              <td>${escapeHTML(d.purpose || "—")}</td>
            </tr>`,
              )
              .join("")}</tbody></table></div>`
          : `<p class="muted">No dependencies extracted.</p>`;
        const issueList = issues.length
          ? `<ul class="file-list">${issues.map((x) => `<li>${escapeHTML(x)}</li>`).join("")}</ul>`
          : `<p class="muted">No common issue patterns listed.</p>`;
        tabContent.innerHTML = `
          <div class="memory-view">
            ${memory.architecture ? `<section><h3>Architecture</h3><p class="memory-prose">${escapeHTML(memory.architecture)}</p></section>` : ""}
            <section><h3>Languages</h3>
              <p>${memory.primary_lang ? `<span class="pill">Primary: ${escapeHTML(memory.primary_lang)}</span> ` : ""}
              ${langs.map((l) => `<span class="pill">${escapeHTML(l)}</span>`).join(" ") || `<span class="muted">—</span>`}</p>
            </section>
            ${files.length ? `<section><h3>Key files</h3><ul class="file-list">${files.map((f) => `<li class="mono-cell">${escapeHTML(typeof f === "string" ? f : f.path || JSON.stringify(f))}</li>`).join("")}</ul></section>` : ""}
            <section><h3>Coding conventions</h3>${convRows}</section>
            <section><h3>Dependencies</h3>${depRows}</section>
            ${memory.test_patterns ? `<section><h3>Test patterns</h3><p class="memory-prose">${escapeHTML(memory.test_patterns)}</p></section>` : ""}
            ${memory.ci_summary ? `<section><h3>CI / CD</h3><p class="memory-prose">${escapeHTML(memory.ci_summary)}</p></section>` : ""}
            ${memory.review_hints ? `<section><h3>Review focus</h3><p class="memory-prose">${escapeHTML(memory.review_hints)}</p></section>` : ""}
            <section><h3>Common issues</h3>${issueList}</section>
            ${memory.user_context ? `<section><h3>Operator notes</h3><p class="memory-prose">${escapeHTML(memory.user_context)}</p></section>` : ""}
          </div>
        `;
      } else if (name === "graph") {
        if (typeof d3 === "undefined" || !d3.forceSimulation) {
          tabContent.innerHTML = `<div class="placeholder"><p>Graph library not loaded. Hard-refresh the page (cache).</p></div>`;
          return;
        }
        tabContent.innerHTML = `<div class="repo-graph-loading">Loading call graph…</div>`;
        (async () => {
          let cg = null;
          try {
            cg = await fetchJSON(`${API}/repos/${enc}/callgraph`);
          } catch (err) {
            tabContent.innerHTML = `<div class="placeholder"><p>${escapeHTML(
              err.message || "No call graph yet. Run sync after indexing completes.",
            )}</p></div>`;
            return;
          }
          const activeBtn = container.querySelector(".tab-btn.active");
          if (!activeBtn || activeBtn.dataset.tab !== "graph") {
            return;
          }
          if (!cg || !Array.isArray(cg.nodes) || !cg.nodes.length) {
            tabContent.innerHTML = `<div class="placeholder"><p>No graph nodes yet. Re-sync after indexing finishes.</p></div>`;
            return;
          }
          graphView = renderCallGraph(tabContent, container, cg, memory, fullRepo);
        })();
      } else if (name === "ask") {
        const treeNote = fullRepo.index_tree_truncated
          ? `<p class="repo-index-tree-note" role="status">This repo’s last index used a <strong>truncated</strong> GitHub tree response — results may miss some files.</p>`
          : "";
        tabContent.innerHTML = `
          <div class="repo-ask-panel">
            ${treeNote}
            <p class="muted repo-ask-intro">Search across the <strong>full indexed tree</strong> for this repository (keyword + semantic hybrid). Run <strong>Sync</strong> after enabling repo intel so indexing completes.</p>
            <form id="repo-ask-form" class="repo-ask-form">
              <label class="repo-ask-label" for="repo-ask-q">Question or keywords</label>
              <textarea id="repo-ask-q" class="repo-ask-textarea" rows="3" placeholder="e.g. How is authentication implemented?"></textarea>
              <div class="repo-ask-actions">
                <button type="submit" class="primary">Search</button>
                <span class="muted repo-ask-hint">Results are ranked passages from indexed files and repo memory.</span>
              </div>
            </form>
            <div id="repo-ask-results" class="repo-ask-results"></div>
          </div>`;
        const form = tabContent.querySelector("#repo-ask-form");
        const qEl = tabContent.querySelector("#repo-ask-q");
        const outEl = tabContent.querySelector("#repo-ask-results");
        form.addEventListener("submit", async (ev) => {
          ev.preventDefault();
          const q = String(qEl.value || "").trim();
          if (!q) {
            outEl.innerHTML = `<p class="muted">Enter a query.</p>`;
            return;
          }
          outEl.innerHTML = `<div class="loading">Searching…</div>`;
          try {
            const data = await sendJSON(`${API}/repos/${enc}/search`, "POST", { query: q, limit: 18 });
            const hits = Array.isArray(data.hits) ? data.hits : [];
            if (!hits.length) {
              outEl.innerHTML = `<p class="muted">No hits. Try different keywords or confirm sync finished (Settings → embeddings required for semantic ranking).</p>`;
              return;
            }
            const searchTrunc = data.index_tree_truncated
              ? `<p class="repo-index-tree-note repo-ask-post-note" role="status">GitHub tree was truncated when this repo was indexed — not every path is searchable.</p>`
              : "";
            outEl.innerHTML = `${searchTrunc}<div class="repo-ask-hit-list">${hits
              .map(
                (h) => `<article class="repo-ask-hit">
                <header class="repo-ask-hit-head">
                  <span class="mono-cell">${escapeHTML(h.file_path || h.heading || "—")}</span>
                  <span class="pill">${escapeHTML(h.kind || "chunk")}</span>
                  <span class="muted">score ${typeof h.score === "number" ? h.score.toFixed(3) : "—"}</span>
                </header>
                <pre class="repo-ask-snippet">${escapeHTML(String(h.content || "").slice(0, 4000))}</pre>
              </article>`,
              )
              .join("")}</div>`;
          } catch (err) {
            outEl.innerHTML = `<p class="placeholder">${escapeHTML(err.message || "search failed")}</p>`;
          }
        });
      } else if (name === "users") {
        const list = userList;
        tabContent.innerHTML = list.length
          ? `<div class="admin-table-wrap"><table class="admin-table">
              <thead><tr><th>Handle</th><th>Role</th><th>Email</th></tr></thead>
              <tbody>${list.map((u) => `<tr>
                <td class="mono-cell">${escapeHTML(u.handle || "—")}</td>
                <td>${escapeHTML(String(u.role || "member"))}</td>
                <td>${escapeHTML(u.email || "—")}</td>
              </tr>`).join("")}</tbody>
            </table></div>`
          : `<div class="placeholder"><p>No users linked to this repo yet.</p></div>`;
      }
    }

    container.querySelectorAll(".tab-btn").forEach((btn) => {
      btn.addEventListener("click", () => showTab(btn.dataset.tab));
    });
    showTab("scan");
  }

  function openMintKeyModal(canManageAll) {
    const modal = openModal({ title: "Mint new API key" });
    modal.body.innerHTML = `
      <form id="mint-form" class="modal-form">
        <div class="field">
          <label>Name</label>
          <input name="name" type="text" required />
          <p class="field-help">Shown in the key list. e.g. "CI · prod-deploy".</p>
        </div>
        ${
          canManageAll
            ? `<div class="field">
                 <label>Owner username</label>
                 <input name="username" type="text" placeholder="(defaults to you)" />
                 <p class="field-help">Leave empty to mint for yourself.</p>
               </div>`
            : ""
        }
        <div class="field">
          <label>Expires (duration)</label>
          <input name="expires" type="text" placeholder="e.g. 720h  (leave empty for no expiry)" />
          <p class="field-help">Go duration syntax: <code>h</code>, <code>m</code>, <code>s</code>.</p>
        </div>
        <div class="field">
          <label>Scopes (comma-separated, optional)</label>
          <input name="scopes" type="text" placeholder="tasks.read,tasks.cancel" />
          <p class="field-help">Intersected with the owner's permissions. Empty = full owner permissions.</p>
        </div>
      </form>
    `;
    setModalFooter(modal, [
      { label: "Cancel", kind: "ghost", onClick: () => closeModal(modal) },
      {
        label: "Mint",
        kind: "primary",
        onClick: async () => {
          const form = modal.body.querySelector("#mint-form");
          const fd = new FormData(form);
          const body = {
            name: String(fd.get("name") || "").trim(),
            expires: String(fd.get("expires") || "").trim(),
          };
          const scopesRaw = String(fd.get("scopes") || "").trim();
          if (scopesRaw) {
            body.scopes = scopesRaw.split(",").map((s) => s.trim()).filter(Boolean);
          }
          if (canManageAll) {
            const u = String(fd.get("username") || "").trim();
            if (u) body.username = u;
          }
          if (!body.name) {
            showToast("name is required", "warn");
            return;
          }
          try {
            const res = await sendJSON(`${API}/apikeys`, "POST", body);
            closeModal(modal);
            showMintedKey(res);
          } catch (err) {
            showToast(err.message || "mint failed", "error");
          }
        },
      },
    ]);
  }

  function showMintedKey(res) {
    const modal = openModal({ title: "Copy this token now" });
    modal.body.innerHTML = `
      <p class="warn-banner">
        This is the only time the full token is shown. Store it somewhere
        safe <strong>now</strong> — we only keep the hash.
      </p>
      <div class="field">
        <label>Token</label>
        <div class="token-row">
          <input id="minted-token" readonly value="${escapeHTML(res.plain_token || "")}" />
          <button type="button" class="ghost" id="copy-token-btn">Copy</button>
        </div>
      </div>
      <div class="field">
        <label>Key ID</label>
        <input readonly value="${escapeHTML(res.key && res.key.key_id || "")}" />
      </div>
      ${
        res.key && res.key.expires_at
          ? `<p class="field-help">Expires ${escapeHTML(formatDate(res.key.expires_at))}.</p>`
          : `<p class="field-help">No expiry set.</p>`
      }
    `;
    const copyBtn = modal.body.querySelector("#copy-token-btn");
    copyBtn.addEventListener("click", async () => {
      const input = modal.body.querySelector("#minted-token");
      try {
        await navigator.clipboard.writeText(input.value);
        showToast("copied", "ok");
      } catch (_) {
        input.select();
        document.execCommand("copy");
        showToast("copied (fallback)", "ok");
      }
    });
    setModalFooter(modal, [
      {
        label: "Done",
        kind: "primary",
        onClick: () => {
          closeModal(modal);
          reroute();
        },
      },
    ]);
  }

  // ─────────────────────── shared ui helpers ───────────────────────

  function meHasPerm(perm) {
    if (!ME || !Array.isArray(ME.roles)) return false;
    // The dashboard does not ship the full permission set; rely on
    // the server's 403s for the authoritative answer. Here we use a
    // conservative role → perm mapping so we hide buttons the caller
    // can't use. If a button IS rendered but the backend rejects the
    // call, we still surface a toast.
    const roleToPerms = {
      owner: ["*"],
      admin: [
        "users.read", "users.manage", "users.delete",
        "roles.read", "roles.manage",
        "apikeys.read.all", "apikeys.manage.all", "apikeys.read.own", "apikeys.manage.own",
        "settings.read", "settings.write", "secrets.read", "secrets.write",
      ],
      operator: [
        "users.read", "roles.read",
        "apikeys.read.own", "apikeys.manage.own",
      ],
      developer: ["apikeys.read.own", "apikeys.manage.own"],
      auditor: ["users.read", "roles.read", "apikeys.read.all"],
      viewer: [],
    };
    for (const role of ME.roles) {
      const perms = roleToPerms[role] || [];
      if (perms.includes("*")) return true;
      if (perms.includes(perm)) return true;
      // Namespace wildcards just in case a future role uses them.
      if (perms.some((p) => p.endsWith(".*") && perm.startsWith(p.slice(0, -1)))) {
        return true;
      }
    }
    return false;
  }

  async function sendJSON(url, method, body) {
    const opts = {
      method,
      credentials: "same-origin",
      headers: { ...csrfHeaders() },
    };
    if (body !== undefined && body !== null) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(url, opts);
    const text = await res.text();
    let parsed = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch (_) {}
    }
    if (!res.ok) {
      const msg = (parsed && parsed.error) || `${res.status}`;
      const err = new Error(msg);
      err.status = res.status;
      throw err;
    }
    return parsed;
  }

  function makeButton(label, kind, onClick) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = kind || "ghost";
    b.textContent = label;
    b.addEventListener("click", onClick);
    return b;
  }

  function reroute() {
    // Re-trigger the current route to refresh the view after a write.
    route();
  }

  function formatDate(s) {
    if (!s) return "";
    try {
      const d = new Date(s);
      return d.toISOString().replace("T", " ").replace(/\.\d+Z$/, "Z");
    } catch (_) {
      return String(s);
    }
  }

  // ─────────────────────── modal ───────────────────────

  function openModal({ title }) {
    const backdrop = document.getElementById("modal-backdrop");
    const titleEl = document.getElementById("modal-title");
    const body = document.getElementById("modal-body");
    const footer = document.getElementById("modal-footer");
    titleEl.textContent = title || "";
    body.innerHTML = "";
    footer.innerHTML = "";
    backdrop.classList.remove("hidden");
    document.getElementById("modal-close").onclick = () => closeModal({ backdrop });
    // esc-to-close
    const keyHandler = (ev) => {
      if (ev.key === "Escape") closeModal({ backdrop, keyHandler });
    };
    document.addEventListener("keydown", keyHandler);
    return { backdrop, body, footer, keyHandler };
  }

  function closeModal(modal) {
    if (!modal || !modal.backdrop) return;
    modal.backdrop.classList.add("hidden");
    if (modal.keyHandler) {
      document.removeEventListener("keydown", modal.keyHandler);
    }
  }

  function setModalFooter(modal, buttons) {
    if (!modal || !modal.footer) return;
    modal.footer.innerHTML = "";
    buttons.forEach((spec) => {
      modal.footer.appendChild(makeButton(spec.label, spec.kind, spec.onClick));
    });
  }

  // ─────────────────────── helpers ───────────────────────

  async function getJSON(url) {
    const res = await fetch(url, { credentials: "same-origin" });
    if (!res.ok) throw new Error(`${url} returned ${res.status}`);
    return res.json();
  }

  async function fetchJSON(url) {
    const res = await fetch(url, { credentials: "same-origin" });
    if (!res.ok) {
      let msg = `${res.status}`;
      try {
        const body = await res.json();
        if (body && body.error) msg = body.error;
      } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  function csrfHeaders() {
    const tok = readCookie("opi_csrf");
    return tok ? { "X-CSRF-Token": tok } : {};
  }

  function readCookie(name) {
    const match = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
    return match ? decodeURIComponent(match[1]) : "";
  }

  function escapeHTML(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  // ───────────────────────────────────────────────────────────────────
  // Call-graph renderer (D3, Obsidian-style force layout)
  // ───────────────────────────────────────────────────────────────────
  const GRAPH_TYPE_META = {
    function: { color: "#e4572e", radius: 8, label: "Function" },
    method:   { color: "#7c3aed", radius: 8, label: "Method" },
    class:    { color: "#0d9488", radius: 9, label: "Class" },
    file:     { color: "#2563eb", radius: 10, label: "File" },
    module:   { color: "#78716c", radius: 7, label: "Module" },
  };

  function renderCallGraph(tabContent, container, cg, memory, fullRepo) {
    const rawNodes = cg.nodes.slice();
    const rawEdges = (Array.isArray(cg.edges) ? cg.edges : []).slice();
    const importEdgeCount = rawEdges.filter((e) => e.kind === "import").length;
    const callEdgeCount = rawEdges.length - importEdgeCount;
    const libPkgAllowed = !!fullRepo.show_callgraph_library_packages;
    const showImportsInitially = libPkgAllowed && importEdgeCount <= 22;

    const arch = memory && memory.architecture
      ? escapeHTML(String(memory.architecture).slice(0, 1400)) : "";
    const hints = memory && memory.review_hints
      ? escapeHTML(String(memory.review_hints).slice(0, 900)) : "";
    const kf = memory && Array.isArray(memory.key_files) ? memory.key_files : [];
    const kfList = kf.map((f) => {
      const p = typeof f === "string" ? f : f.path || JSON.stringify(f);
      return `<li class="mono-cell">${escapeHTML(p)}</li>`;
    }).join("");

    const presentKinds = Array.from(new Set(rawNodes.map((n) => n.kind || "function")));
    const legendOrder = ["function", "method", "class", "file", "module"];
    const orderedKinds = legendOrder.filter((k) => presentKinds.includes(k));
    const legendHtml = orderedKinds.map((k) => {
      const meta = GRAPH_TYPE_META[k] || { color: "#78716c", label: k };
      const count = rawNodes.filter((n) => (n.kind || "function") === k).length;
      return `<li class="repo-graph-legend-chip" data-kind="${escapeHTML(k)}">
        <span class="repo-graph-legend-dot" style="background:${meta.color}"></span>
        <span class="repo-graph-legend-label">${escapeHTML(meta.label)}</span>
        <span class="repo-graph-legend-count">${count}</span>
      </li>`;
    }).join("");

    const toolbarImports = libPkgAllowed
      ? `<label class="repo-graph-toolbar-label">
            <input type="checkbox" id="repo-graph-show-imports" ${showImportsInitially ? "checked" : ""} />
            Show package / import edges
          </label>
          <span class="repo-graph-toolbar-meta muted">${callEdgeCount} call · ${importEdgeCount} import</span>`
      : `<span class="repo-graph-toolbar-meta muted">${callEdgeCount} call edges · ${importEdgeCount} import edges hidden by policy</span>`;

    tabContent.innerHTML = `
      <div class="repo-graph-layout repo-graph-layout-d3">
        <aside class="repo-graph-sidebar">
          <h3 class="repo-graph-sidebar-title">Orient</h3>
          <p class="repo-graph-sidebar-hint muted">Hover a node to focus its neighbors. Click to inspect. Drag to rearrange. Scroll to zoom.</p>
          ${arch ? `<section class="repo-graph-section"><h4>Architecture</h4><div class="repo-graph-prose">${arch}</div></section>` : ""}
          ${kfList ? `<section class="repo-graph-section"><h4>Key files</h4><ul class="repo-graph-file-list">${kfList}</ul></section>` : ""}
          ${hints ? `<section class="repo-graph-section"><h4>Review focus</h4><div class="repo-graph-prose">${hints}</div></section>` : ""}
          <section class="repo-graph-section">
            <h4>Node types</h4>
            <ul class="repo-graph-legend">${legendHtml}</ul>
          </section>
          <section class="repo-graph-section repo-graph-paths-section">
            <h4>Call-chain walkthroughs</h4>
            <p class="repo-graph-paths-hint muted">Auto-detected from entry points. Click to animate the trace.</p>
            <div class="repo-graph-paths" id="repo-graph-paths"></div>
          </section>
        </aside>
        <div class="repo-graph-canvas-wrap">
          <div class="repo-graph-toolbar">
            ${toolbarImports}
            <button type="button" class="ghost" id="repo-graph-fit">Fit view</button>
            <button type="button" class="ghost" id="repo-graph-relayout">Re-layout</button>
          </div>
          <div class="repo-graph-canvas-host">
            <svg id="repo-graph-svg" class="repo-graph-svg" role="img" aria-label="Call graph"></svg>
            <div class="repo-graph-flow-status" id="repo-graph-flow-status" aria-live="polite"></div>
          </div>
          <div class="repo-graph-detail" id="repo-graph-detail">
            <div class="repo-graph-detail-empty muted">Click a node for details — name, file, dependencies, and dependents.</div>
          </div>
        </div>
      </div>`;

    const hostEl = tabContent.querySelector(".repo-graph-canvas-host");
    const svgEl = tabContent.querySelector("#repo-graph-svg");
    const detailEl = tabContent.querySelector("#repo-graph-detail");
    const flowStatusEl = tabContent.querySelector("#repo-graph-flow-status");
    const pathsEl = tabContent.querySelector("#repo-graph-paths");

    const state = {
      showImports: libPkgAllowed && showImportsInitially,
      hiddenKinds: new Set(),
      selectedId: null,
      flowPlaying: false,
      flowAbort: 0,
    };

    const svg = d3.select(svgEl);
    const rootG = svg.append("g");
    const linkLayer = rootG.append("g").attr("class", "repo-graph-links");
    const nodeLayer = rootG.append("g").attr("class", "repo-graph-nodes");
    const pulseLayer = rootG.append("g").attr("class", "repo-graph-pulses");

    const zoom = d3.zoom().scaleExtent([0.2, 4]).on("zoom", (e) => {
      rootG.attr("transform", e.transform);
    });
    svg.call(zoom).on("dblclick.zoom", null);
    svg.on("click", (e) => {
      if (e.target === svgEl) clearSelection();
    });

    let simulation = null;
    let nodeSel = null;
    let linkSel = null;
    let visibleNodes = [];
    let visibleEdges = [];
    let depsMap = {}, dependentsMap = {};

    function resizeSvg() {
      const r = hostEl.getBoundingClientRect();
      if (r.width > 0 && r.height > 0) {
        svg.attr("viewBox", `0 0 ${r.width} ${r.height}`)
          .attr("preserveAspectRatio", "xMidYMid meet");
      }
    }
    resizeSvg();
    const ro = (typeof ResizeObserver !== "undefined") ? new ResizeObserver(() => {
      resizeSvg();
      if (simulation) {
        const r = hostEl.getBoundingClientRect();
        simulation.force("center", d3.forceCenter(r.width / 2, r.height / 2));
        simulation.alpha(0.3).restart();
      }
    }) : null;
    if (ro) ro.observe(hostEl);

    function rebuild() {
      const wantImports = state.showImports;
      visibleNodes = rawNodes
        .filter((n) => wantImports || n.kind !== "module")
        .filter((n) => !state.hiddenKinds.has(n.kind || "function"))
        .map((n) => Object.assign({}, n));
      const idSet = new Set(visibleNodes.map((n) => n.id));
      visibleEdges = rawEdges
        .filter((e) => wantImports || e.kind !== "import")
        .filter((e) => idSet.has(e.from) && idSet.has(e.to))
        .map((e) => ({ source: e.from, target: e.to, kind: e.kind }));

      depsMap = {}; dependentsMap = {};
      visibleNodes.forEach((n) => { depsMap[n.id] = []; dependentsMap[n.id] = []; });
      visibleEdges.forEach((e) => {
        depsMap[e.source].push(e.target);
        dependentsMap[e.target].push(e.source);
      });

      renderForce();
      renderPaths();
    }

    function renderForce() {
      const r = hostEl.getBoundingClientRect();
      if (simulation) simulation.stop();

      linkSel = linkLayer.selectAll("line").data(visibleEdges, (d) => `${d.source.id || d.source}-${d.target.id || d.target}-${d.kind}`);
      linkSel.exit().remove();
      const linkEnter = linkSel.enter().append("line")
        .attr("class", (d) => `repo-graph-link repo-graph-link-${d.kind}`);
      linkSel = linkEnter.merge(linkSel);

      nodeSel = nodeLayer.selectAll("g.repo-graph-node").data(visibleNodes, (d) => d.id);
      nodeSel.exit().remove();
      const nodeEnter = nodeSel.enter().append("g")
        .attr("class", "repo-graph-node")
        .on("click", (event, d) => { event.stopPropagation(); selectNode(d.id); })
        .on("mouseenter", (_, d) => hoverNode(d.id, true))
        .on("mouseleave", (_, d) => hoverNode(d.id, false))
        .call(d3.drag()
          .on("start", (e, d) => { if (!e.active) simulation.alphaTarget(0.25).restart(); d.fx = d.x; d.fy = d.y; })
          .on("drag",  (e, d) => { d.fx = e.x; d.fy = e.y; })
          .on("end",   (e, d) => { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; }));
      nodeEnter.append("circle")
        .attr("r", (d) => (GRAPH_TYPE_META[d.kind] || GRAPH_TYPE_META.function).radius)
        .attr("fill", (d) => (GRAPH_TYPE_META[d.kind] || GRAPH_TYPE_META.function).color)
        .attr("fill-opacity", 0.18)
        .attr("stroke", (d) => (GRAPH_TYPE_META[d.kind] || GRAPH_TYPE_META.function).color);
      nodeEnter.append("text")
        .attr("dy", (d) => (GRAPH_TYPE_META[d.kind] || GRAPH_TYPE_META.function).radius + 11)
        .text((d) => d.name || d.id);
      nodeSel = nodeEnter.merge(nodeSel);

      simulation = d3.forceSimulation(visibleNodes)
        .force("link", d3.forceLink(visibleEdges).id((d) => d.id).distance(80).strength(0.45))
        .force("charge", d3.forceManyBody().strength(-260))
        .force("center", d3.forceCenter(r.width / 2, r.height / 2))
        .force("collide", d3.forceCollide().radius((d) => ((GRAPH_TYPE_META[d.kind] || GRAPH_TYPE_META.function).radius) + 10))
        .on("tick", () => {
          linkSel
            .attr("x1", (d) => d.source.x).attr("y1", (d) => d.source.y)
            .attr("x2", (d) => d.target.x).attr("y2", (d) => d.target.y);
          nodeSel.attr("transform", (d) => `translate(${d.x},${d.y})`);
        });
      simulation.alpha(1).restart();
    }

    function hoverNode(id, on) {
      if (state.flowPlaying || state.selectedId) return;
      if (on) {
        const neighbors = new Set([id, ...(depsMap[id] || []), ...(dependentsMap[id] || [])]);
        nodeSel.classed("dimmed", (d) => !neighbors.has(d.id));
        linkSel.classed("dimmed", (l) => !(l.source.id === id || l.target.id === id))
               .classed("highlighted", (l) => (l.source.id === id || l.target.id === id));
      } else {
        nodeSel.classed("dimmed", false);
        linkSel.classed("dimmed", false).classed("highlighted", false);
      }
    }

    function selectNode(id) {
      state.selectedId = id;
      const n = visibleNodes.find((x) => x.id === id) || rawNodes.find((x) => x.id === id);
      if (!n) return;
      const neighbors = new Set([id, ...(depsMap[id] || []), ...(dependentsMap[id] || [])]);
      nodeSel
        .classed("selected", (d) => d.id === id)
        .classed("dimmed", (d) => !neighbors.has(d.id));
      linkSel.classed("dimmed", (l) => !(l.source.id === id || l.target.id === id))
             .classed("highlighted", (l) => (l.source.id === id || l.target.id === id));
      renderDetail(n);
    }

    function clearSelection() {
      state.selectedId = null;
      if (nodeSel) nodeSel.classed("selected", false).classed("dimmed", false);
      if (linkSel) linkSel.classed("dimmed", false).classed("highlighted", false);
      detailEl.innerHTML = `<div class="repo-graph-detail-empty muted">Click a node for details — name, file, dependencies, and dependents.</div>`;
    }

    function renderDetail(n) {
      const meta = GRAPH_TYPE_META[n.kind] || GRAPH_TYPE_META.function;
      const depItems = (depsMap[n.id] || []).map((targetId) => {
        const t = visibleNodes.find((x) => x.id === targetId);
        if (!t) return "";
        const tMeta = GRAPH_TYPE_META[t.kind] || GRAPH_TYPE_META.function;
        return `<li class="repo-graph-dep-item" data-id="${escapeHTML(t.id)}">
          <span class="repo-graph-dep-dot" style="background:${tMeta.color}"></span>
          <span class="repo-graph-dep-label">${escapeHTML(t.name || t.id)}</span>
          <span class="repo-graph-dep-kind muted">${escapeHTML(t.kind)}</span>
        </li>`;
      }).join("");
      const sourceItems = (dependentsMap[n.id] || []).map((sourceId) => {
        const s = visibleNodes.find((x) => x.id === sourceId);
        if (!s) return "";
        const sMeta = GRAPH_TYPE_META[s.kind] || GRAPH_TYPE_META.function;
        return `<li class="repo-graph-dep-item" data-id="${escapeHTML(s.id)}">
          <span class="repo-graph-dep-dot" style="background:${sMeta.color}"></span>
          <span class="repo-graph-dep-label">${escapeHTML(s.name || s.id)}</span>
          <span class="repo-graph-dep-kind muted">${escapeHTML(s.kind)}</span>
        </li>`;
      }).join("");
      const loc = (n.kind === "module" || n.kind === "file") ? (n.file || "—") : `${n.file || "—"}:${n.line || 0}`;
      const pkg = n.package ? `<span class="muted"> · pkg ${escapeHTML(n.package)}</span>` : "";
      detailEl.innerHTML = `
        <div class="repo-graph-detail-header">
          <span class="repo-graph-type-pill" style="background:${meta.color}1f;color:${meta.color};border-color:${meta.color}55">${escapeHTML(meta.label)}</span>
          <div class="repo-graph-detail-name">${escapeHTML(n.name || n.id)}</div>
          <div class="repo-graph-detail-loc mono-cell">${escapeHTML(loc)}${pkg}</div>
        </div>
        <div class="repo-graph-detail-cols">
          <section class="repo-graph-detail-col">
            <h5>Depends on <span class="muted">(${(depsMap[n.id] || []).length})</span></h5>
            <ul class="repo-graph-dep-list">${depItems || `<li class="repo-graph-dep-empty muted">None in view</li>`}</ul>
          </section>
          <section class="repo-graph-detail-col">
            <h5>Called by <span class="muted">(${(dependentsMap[n.id] || []).length})</span></h5>
            <ul class="repo-graph-dep-list">${sourceItems || `<li class="repo-graph-dep-empty muted">None in view</li>`}</ul>
          </section>
        </div>`;
      detailEl.querySelectorAll(".repo-graph-dep-item").forEach((el) => {
        el.addEventListener("click", () => selectNode(el.dataset.id));
      });
    }

    // ─── Call-chain walkthroughs (computed from graph data) ─────────
    function computeWalkthroughs() {
      // Entry points = nodes with zero incoming CALL edges, that have outgoing call edges
      const callIn = {}, callOut = {};
      rawNodes.forEach((n) => { callIn[n.id] = 0; callOut[n.id] = []; });
      rawEdges.forEach((e) => {
        if (e.kind !== "call") return;
        if (callIn[e.to] != null) callIn[e.to] += 1;
        if (callOut[e.from]) callOut[e.from].push(e.to);
      });
      const entries = rawNodes
        .filter((n) => (n.kind === "function" || n.kind === "method"))
        .filter((n) => callIn[n.id] === 0 && callOut[n.id].length > 0);
      // Rank by reachable-set size (depth 4)
      function reachCount(id) {
        const seen = new Set([id]);
        const stack = [{ id, d: 0 }];
        while (stack.length) {
          const { id: cur, d } = stack.pop();
          if (d >= 4) continue;
          (callOut[cur] || []).forEach((t) => {
            if (!seen.has(t)) { seen.add(t); stack.push({ id: t, d: d + 1 }); }
          });
        }
        return seen.size;
      }
      entries.forEach((e) => { e._reach = reachCount(e.id); });
      entries.sort((a, b) => b._reach - a._reach);
      const top = entries.slice(0, 6);
      // For each entry, build a representative DFS walk (max 14 hops, no cycles)
      return top.map((entry) => {
        const path = [entry.id];
        const visited = new Set([entry.id]);
        let cur = entry.id;
        while (path.length < 14) {
          const nexts = (callOut[cur] || []).filter((t) => !visited.has(t));
          if (!nexts.length) break;
          // pick the next node with highest reach (most interesting branch)
          nexts.sort((a, b) => (reachCount(b) - reachCount(a)));
          cur = nexts[0];
          visited.add(cur);
          path.push(cur);
        }
        return { entryId: entry.id, entry, path };
      }).filter((w) => w.path.length >= 2);
    }

    function renderPaths() {
      const walks = computeWalkthroughs();
      if (!walks.length) {
        pathsEl.innerHTML = `<div class="muted" style="font-size:12px">No clear entry points detected.</div>`;
        return;
      }
      pathsEl.innerHTML = walks.map((w, i) => {
        const node = rawNodes.find((n) => n.id === w.entryId);
        const name = node ? (node.name || node.id) : w.entryId;
        const loc = node ? `${node.file || ""}${node.line ? ":" + node.line : ""}` : "";
        return `<button type="button" class="repo-graph-path-btn" data-walk="${i}">
          <span class="repo-graph-path-name">${escapeHTML(name)}</span>
          <span class="repo-graph-path-meta muted">${w.path.length} hops · ${escapeHTML(loc)}</span>
        </button>`;
      }).join("");
      pathsEl.querySelectorAll(".repo-graph-path-btn").forEach((btn) => {
        btn.addEventListener("click", () => {
          const idx = parseInt(btn.dataset.walk, 10);
          playWalk(walks[idx], btn);
        });
      });
    }

    const sleep = (ms, token) => new Promise((resolve, reject) => {
      const t = setTimeout(() => {
        if (token.aborted) reject(new Error("aborted")); else resolve();
      }, ms);
      token.timers.push(t);
    });

    async function playWalk(walk, btn) {
      if (state.flowPlaying) return;
      state.flowPlaying = true;
      state.flowAbort += 1;
      const token = { aborted: false, timers: [] };
      const myAbort = state.flowAbort;
      pathsEl.querySelectorAll(".repo-graph-path-btn").forEach((b) => b.classList.remove("playing"));
      btn.classList.add("playing");
      clearSelection();
      nodeSel.classed("flow-active", false).classed("flow-current", false);
      linkSel.classed("flow-active", false);
      pulseLayer.selectAll("*").remove();
      flowStatusEl.classList.add("visible");

      try {
        for (let i = 0; i < walk.path.length; i++) {
          if (myAbort !== state.flowAbort) { token.aborted = true; break; }
          const id = walk.path[i];
          const cur = visibleNodes.find((n) => n.id === id);
          if (!cur) continue;
          flowStatusEl.innerHTML = `<span class="repo-graph-flow-dot"></span>
            <span class="repo-graph-flow-step">${escapeHTML(cur.name || id)}</span>
            <span class="repo-graph-flow-meta muted">step ${i + 1} of ${walk.path.length}</span>`;
          nodeSel.filter((d) => d.id === id).classed("flow-current", true).classed("flow-active", true);
          if (i > 0) {
            const prevId = walk.path[i - 1];
            const lnk = visibleEdges.find((e) => (e.source.id === prevId && e.target.id === id));
            if (lnk) {
              linkSel.filter((l) => l === lnk).classed("flow-active", true);
              const prev = visibleNodes.find((n) => n.id === prevId);
              if (prev && cur) animatePulse(prev, cur);
            }
          }
          await sleep(520, token);
          nodeSel.filter((d) => d.id === id).classed("flow-current", false);
        }
        await sleep(900, token);
      } catch (_) { /* aborted */ }
      finally {
        nodeSel.classed("flow-active", false).classed("flow-current", false);
        linkSel.classed("flow-active", false);
        pulseLayer.selectAll("*").remove();
        flowStatusEl.classList.remove("visible");
        flowStatusEl.innerHTML = "";
        btn.classList.remove("playing");
        if (myAbort === state.flowAbort) state.flowPlaying = false;
        token.timers.forEach((t) => clearTimeout(t));
      }
    }

    function animatePulse(source, target) {
      const pulse = pulseLayer.append("circle")
        .attr("class", "repo-graph-pulse")
        .attr("r", 4)
        .attr("cx", source.x).attr("cy", source.y);
      pulse.transition()
        .duration(460).ease(d3.easeQuadInOut)
        .attr("cx", target.x).attr("cy", target.y)
        .on("end", function () { d3.select(this).remove(); });
    }

    // ─── Toolbar / legend handlers ──────────────────────────────────
    const chk = libPkgAllowed ? tabContent.querySelector("#repo-graph-show-imports") : null;
    if (chk) {
      chk.addEventListener("change", () => {
        state.showImports = chk.checked;
        rebuild();
      });
    }
    tabContent.querySelectorAll(".repo-graph-legend-chip").forEach((chip) => {
      chip.addEventListener("click", () => {
        const k = chip.dataset.kind;
        if (state.hiddenKinds.has(k)) { state.hiddenKinds.delete(k); chip.classList.remove("dim"); }
        else { state.hiddenKinds.add(k); chip.classList.add("dim"); }
        rebuild();
      });
    });
    const fitBtn = tabContent.querySelector("#repo-graph-fit");
    if (fitBtn) fitBtn.addEventListener("click", () => {
      svg.transition().duration(360).call(zoom.transform, d3.zoomIdentity);
    });
    const relayoutBtn = tabContent.querySelector("#repo-graph-relayout");
    if (relayoutBtn) relayoutBtn.addEventListener("click", () => {
      visibleNodes.forEach((n) => { n.fx = null; n.fy = null; });
      if (simulation) simulation.alpha(1).restart();
    });

    rebuild();

    return {
      dispose() {
        state.flowAbort += 1;
        state.flowPlaying = false;
        if (simulation) { try { simulation.stop(); } catch (_) {} }
        if (ro) { try { ro.disconnect(); } catch (_) {} }
        svg.on(".zoom", null);
      },
    };
  }

  function cssEscape(s) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_\-]/g, "\\$&");
  }

  function prettySection(s) {
    return s.charAt(0).toUpperCase() + s.slice(1);
  }

  function errorBlock(msg) {
    return `<div class="placeholder error"><h2>Error</h2><p>${escapeHTML(msg)}</p></div>`;
  }

  function showToast(msg, kind) {
    const t = document.getElementById("toast");
    if (!t) return;
    t.textContent = msg;
    t.className = `toast toast-${kind || "info"} visible`;
    if (t._toastTimer) clearTimeout(t._toastTimer);
    t._toastTimer = setTimeout(() => {
      t.className = "toast hidden";
    }, 4000);
  }

  // ── command palette ──

  const CMD_ITEMS = [
    { label: "Overview", route: "#/overview", icon: "◈" },
    { label: "Tasks", route: "#/tasks", icon: "◈" },
    { label: "Users & Roles", route: "#/users", icon: "◈" },
    { label: "API keys", route: "#/apikeys", icon: "◈" },
    { label: "Repo Intel", route: "#/repos", icon: "◈" },
    { label: "Run trace", route: "#/runtrace", icon: "◈" },
    { label: "Settings → Gateway", route: "#/settings/gateway", icon: "⚙" },
    { label: "Settings → Auth", route: "#/settings/auth", icon: "⚙" },
    { label: "Settings → Datastore", route: "#/settings/datastore", icon: "⚙" },
    { label: "Settings → Providers", route: "#/settings/providers", icon: "⚙" },
    { label: "Settings → MCP", route: "#/settings/mcp", icon: "⚙" },
    { label: "Settings → Webhooks", route: "#/settings/webhooks", icon: "⚙" },
    { label: "Settings → Channels", route: "#/settings/channels", icon: "⚙" },
    { label: "Settings → DevOps", route: "#/settings/devops", icon: "⚙" },
    { label: "Settings → Agent", route: "#/settings/agent", icon: "⚙" },
    { label: "Settings → Repo Intel", route: "#/settings/repo_intel", icon: "⚙" },
  ];

  function wireSearchBar() {
    const bar = document.getElementById("global-search-input");
    if (!bar) return;
    bar.addEventListener("focus", () => openCmdPalette());
    bar.addEventListener("click", () => openCmdPalette());
  }

  function openCmdPalette() {
    const backdrop = document.getElementById("cmd-palette-backdrop");
    const input = document.getElementById("cmd-palette-input");
    const results = document.getElementById("cmd-palette-results");
    if (!backdrop || !input) return;
    backdrop.classList.remove("hidden");
    input.value = "";
    input.focus();
    cmdPaletteActive = true;
    cmdPaletteIndex = -1;
    renderCmdResults("");
    const clickOutside = (e) => {
      if (e.target === backdrop) { closeCmdPalette(); document.removeEventListener("click", clickOutside); }
    };
    setTimeout(() => document.addEventListener("click", clickOutside), 10);
  }

  function closeCmdPalette() {
    const backdrop = document.getElementById("cmd-palette-backdrop");
    if (backdrop) backdrop.classList.add("hidden");
    cmdPaletteActive = false;
    cmdPaletteIndex = -1;
  }

  function renderCmdResults(query) {
    const results = document.getElementById("cmd-palette-results");
    if (!results) return;
    const q = query.trim().toLowerCase();
    const hits = q
      ? CMD_ITEMS.filter((it) => it.label.toLowerCase().includes(q))
      : CMD_ITEMS.slice(0, 8);
    results.innerHTML = hits.map((it, i) => `
      <div class="cmd-res" data-idx="${i}" data-route="${it.route}">
        <span style="color:var(--accent);font-size:11px">${it.icon}</span>
        <span>${escapeHTML(it.label)}</span>
        <span class="shortcut">${it.route.replace("#/", "")}</span>
      </div>`).join("");
    results.querySelectorAll(".cmd-res").forEach((el) => {
      el.addEventListener("click", () => {
        window.location.hash = el.dataset.route;
        closeCmdPalette();
      });
      el.addEventListener("mouseenter", () => {
        cmdPaletteIndex = Number(el.dataset.idx);
        highlightCmdItem();
      });
    });
  }

  function highlightCmdItem() {
    const results = document.getElementById("cmd-palette-results");
    if (!results) return;
    results.querySelectorAll(".cmd-res").forEach((el, i) => {
      el.classList.toggle("active", i === cmdPaletteIndex);
    });
  }

  function wireKeyboardShortcuts() {
    document.addEventListener("keydown", (e) => {
      if (cmdPaletteActive) {
        const results = document.getElementById("cmd-palette-results");
        const items = results ? results.querySelectorAll(".cmd-res") : [];
        if (e.key === "Escape") { e.preventDefault(); closeCmdPalette(); return; }
        if (e.key === "ArrowDown") { e.preventDefault(); cmdPaletteIndex = Math.min(cmdPaletteIndex + 1, items.length - 1); highlightCmdItem(); return; }
        if (e.key === "ArrowUp") { e.preventDefault(); cmdPaletteIndex = Math.max(cmdPaletteIndex - 1, -1); highlightCmdItem(); return; }
        if (e.key === "Enter") {
          e.preventDefault();
          if (cmdPaletteIndex >= 0 && items[cmdPaletteIndex]) {
            window.location.hash = items[cmdPaletteIndex].dataset.route;
            closeCmdPalette();
          }
          return;
        }
        return;
      }
      if (e.target.tagName === "INPUT" || e.target.tagName === "TEXTAREA" || e.target.tagName === "SELECT") return;
      if (e.key === "?") { e.preventDefault(); openShortcutsModal(); return; }
      if (e.key === "/") { e.preventDefault(); openCmdPalette(); return; }
      if (e.key === "g" && !e.repeat) {
        // wait for next key
        const handler = (ev) => {
          document.removeEventListener("keydown", handler);
          if (ev.key === "o") window.location.hash = "#/overview";
          if (ev.key === "t") window.location.hash = "#/tasks";
          if (ev.key === "u") window.location.hash = "#/users";
          if (ev.key === "a") window.location.hash = "#/apikeys";
          if (ev.key === "r") window.location.hash = "#/repos";
          if (ev.key === "s") window.location.hash = "#/settings";
          if (ev.key === "l") window.location.hash = "#/runtrace";
        };
        document.addEventListener("keydown", handler, { once: true });
        setTimeout(() => document.removeEventListener("keydown", handler), 600);
      }
    });

    const kbdHelp = document.getElementById("kbd-help");
    if (kbdHelp) kbdHelp.addEventListener("click", openShortcutsModal);

    const paletteInput = document.getElementById("cmd-palette-input");
    if (paletteInput) {
      paletteInput.addEventListener("input", (e) => {
        renderCmdResults(e.target.value);
        cmdPaletteIndex = -1;
      });
    }
  }

  function openShortcutsModal() {
    const modal = openModal({ title: "Keyboard shortcuts" });
    modal.body.innerHTML = `
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:8px 24px;font-size:13px">
        <div style="color:var(--fg-muted)">/</div><div>Open command palette</div>
        <div style="color:var(--fg-muted)">?</div><div>Show this help</div>
        <div style="color:var(--fg-muted)">g o</div><div>Go to Overview</div>
        <div style="color:var(--fg-muted)">g t</div><div>Go to Tasks</div>
        <div style="color:var(--fg-muted)">g u</div><div>Go to Users</div>
        <div style="color:var(--fg-muted)">g a</div><div>Go to API keys</div>
        <div style="color:var(--fg-muted)">g r</div><div>Go to Repo Intel</div>
        <div style="color:var(--fg-muted)">g s</div><div>Go to Settings</div>
        <div style="color:var(--fg-muted)">g l</div><div>Go to Run trace</div>
        <div style="color:var(--fg-muted)">Esc</div><div>Close modal / palette</div>
      </div>
    `;
    setModalFooter(modal, [{ label: "Close", kind: "ghost", onClick: () => closeModal(modal) }]);
  }

  async function refreshOverviewStatus() {
    try {
      const [tasksData, configData] = await Promise.allSettled([
        fetchJSON(`${API}/agent-tasks`).catch(() => null),
        fetchJSON(`${API}/config/redis`).catch(() => null),
      ]);
      const tasks = tasksData.status === "fulfilled" && tasksData.value ? tasksData.value.tasks || [] : [];
      const running = tasks.filter((t) => t.status === "running").length;
      const pending = tasks.filter((t) => t.status === "pending").length;
      const taskEl = document.getElementById("status-tasks");
      const taskHint = document.getElementById("status-tasks-hint");
      if (taskEl) {
        if (running > 0) {
          taskEl.innerHTML = `<span class="pulse-dot"></span> ${running} running`;
          taskEl.className = "status-value";
        } else if (pending > 0) {
          taskEl.textContent = `${pending} pending`;
          taskEl.className = "status-value status-warn";
        } else {
          taskEl.textContent = `${tasks.length} total`;
          taskEl.className = "status-value status-ok";
        }
      }
      if (taskHint) taskHint.textContent = `${tasks.length} task${tasks.length !== 1 ? "s" : ""} in memory`;

      const redisCfg = configData.status === "fulfilled" && configData.value ? configData.value.config : null;
      const redisEl = document.getElementById("status-redis");
      const redisHint = document.getElementById("status-redis-hint");
      if (redisEl) {
        if (redisCfg && redisCfg.enabled) {
          redisEl.innerHTML = `<span class="pulse-dot ok"></span> Enabled`;
          redisEl.className = "status-value";
          if (redisHint) redisHint.textContent = `${redisCfg.addr || "default addr"}`;
        } else {
          redisEl.textContent = "Disabled";
          redisEl.className = "status-value status-ok";
          if (redisHint) redisHint.textContent = "Zero-overhead mode";
        }
      }
    } catch (_) {
      // silently fail on overview status refresh
    }
    const verEl = document.getElementById("status-version");
    if (verEl && verEl.textContent === "—") {
      verEl.textContent = "3c";
    }
  }

  function kvTagsToText(v) {
    if (!v || typeof v !== "object") return "";
    return Object.keys(v)
      .map((k) => `${k}=${(v[k] || []).join(",")}`)
      .join("\n");
  }

  function parseKVTags(text) {
    const out = {};
    String(text || "")
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter(Boolean)
      .forEach((line) => {
        const i = line.indexOf("=");
        if (i < 0) return;
        const k = line.slice(0, i).trim();
        const rest = line.slice(i + 1).trim();
        if (!k) return;
        out[k] = rest
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
      });
    return out;
  }

  function kvTextAreaToText(v) {
    if (!v || typeof v !== "object") return "";
    return Object.keys(v)
      .map((k) => `===${k}===\n${v[k] || ""}`)
      .join("\n");
  }

  function parseKVTextarea(text) {
    const out = {};
    const lines = String(text || "").split(/\r?\n/);
    let curKey = null;
    let buf = [];
    const flush = () => {
      if (curKey != null) out[curKey] = buf.join("\n").replace(/\s+$/, "");
    };
    for (const line of lines) {
      const m = line.match(/^===\s*([^=]+?)\s*===\s*$/);
      if (m) {
        flush();
        curKey = m[1].trim();
        buf = [];
      } else if (curKey != null) {
        buf.push(line);
      }
    }
    flush();
    return out;
  }

  // ─────────────────────── kanban boards ───────────────────────

  let currentBoardId = null;

  let boardsRefreshing = false;
  let boardViewRefreshing = false;

  function renderBoardsView(actionsEl) {
    const container = document.getElementById("boards-list");
    const detail = document.getElementById("board-detail");
    container.classList.remove("hidden");
    detail.classList.add("hidden");
    currentBoardId = null;

    // "New board" button
    const btn = document.createElement("button");
    btn.className = "primary";
    btn.textContent = "+ New board";
    btn.addEventListener("click", () => showCreateBoardModal());
    actionsEl.appendChild(btn);

    refreshBoardsList();
    clearBoardsPoll();
    boardsPollId = setInterval(() => {
      if (document.hidden || boardsRefreshing) return;
      refreshBoardsList();
    }, 5000);
  }

  async function refreshBoardsList() {
    const container = document.getElementById("boards-list");
    if (!container || container.classList.contains("hidden")) return;
    if (boardsRefreshing) return;
    boardsRefreshing = true;
    try {
      const data = await getJSON(`${API}/boards`);
      const boards = data.boards || [];
      if (boards.length === 0) {
        if (container.children.length !== 1 || !container.querySelector(".empty-state")) {
          container.innerHTML = `
            <div class="empty-state">
              <p>No boards yet. Create your first kanban board to get started.</p>
            </div>`;
        }
        return;
      }
      // Simple DOM diffing: reuse existing board cards, add/remove as needed
      let grid = container.querySelector(".boards-grid");
      if (!grid) {
        container.innerHTML = `<div class="boards-grid"></div>`;
        grid = container.querySelector(".boards-grid");
      }
      const existing = new Map();
      grid.querySelectorAll(".board-card").forEach((el) => {
        existing.set(el.dataset.id, el);
      });
      for (const b of boards) {
        if (existing.has(b.id)) {
          existing.delete(b.id);
          continue;
        }
        const card = document.createElement("div");
        card.className = "board-card";
        card.dataset.id = b.id;
        card.innerHTML = `
          <div class="board-card-name">${escapeHtml(b.name)}</div>
          <div class="board-card-meta">${escapeHtml(b.mode || "local")}${b.repo_url ? " · " + escapeHtml(b.repo_url) : ""}</div>`;
        card.addEventListener("click", () => openBoard(b.id));
        grid.appendChild(card);
      }
      // Remove stale boards
      for (const el of existing.values()) {
        el.remove();
      }
    } catch (err) {
      if (!container.querySelector(".error")) {
        container.innerHTML = `<div class="error">Failed to load boards: ${escapeHtml(err.message)}</div>`;
      }
    } finally {
      boardsRefreshing = false;
    }
  }

  async function openBoard(boardId) {
    currentBoardId = boardId;
    const container = document.getElementById("boards-list");
    const detail = document.getElementById("board-detail");
    container.classList.add("hidden");
    detail.classList.remove("hidden");

    document.getElementById("section-title").textContent = "Board";
    document.getElementById("section-sub").textContent = "Kanban view";

    await refreshBoardView();
    clearBoardsPoll();
    boardsPollId = setInterval(() => {
      if (document.hidden || boardViewRefreshing) return;
      refreshBoardView();
    }, 5000);
  }

  async function refreshBoardView() {
    const detail = document.getElementById("board-detail");
    if (!detail || detail.classList.contains("hidden")) return;
    if (!currentBoardId) return;
    if (boardViewRefreshing) return;
    boardViewRefreshing = true;
    try {
      const data = await getJSON(`${API}/boards/${currentBoardId}`);
      const columns = data.columns || [];
      const cards = data.cards || [];

      // Build or reuse DOM
      let toolbar = detail.querySelector(".kanban-toolbar");
      let boardEl = detail.querySelector(".kanban-board");
      if (!toolbar) {
        detail.innerHTML = `
          <div class="kanban-toolbar">
            <button class="ghost" id="board-back">← Back to boards</button>
            <button class="primary" id="board-new-card">+ New task</button>
            <span class="board-cost-summary" id="board-cost-summary"></span>
            <span style="margin-left:auto"></span>
            <button class="ghost" id="board-sync-gh" title="Pull issues from GitHub (mode=github boards only)">↻ GitHub</button>
            <button class="ghost" id="board-sync-sentry" title="Import Sentry issues">↻ Sentry</button>
            <button class="ghost" id="board-autopilots" title="List running autopilot sessions">Autopilots</button>
          </div>
          <div class="kanban-board"></div>`;
        toolbar = detail.querySelector(".kanban-toolbar");
        boardEl = detail.querySelector(".kanban-board");
        document.getElementById("board-back").addEventListener("click", () => {
          renderBoardsView(document.getElementById("content-actions"));
        });
        document.getElementById("board-new-card").addEventListener("click", () => {
          showCreateCardModal(currentBoardId);
        });
        document.getElementById("board-sync-gh").addEventListener("click", async () => {
          try {
            const res = await fetch(`${API}/boards/${currentBoardId}/github/sync`, {
              method: "POST", credentials: "same-origin", headers: csrfHeaders(),
            });
            if (!res.ok) throw new Error((await res.json()).error || "sync failed");
            const j = await res.json();
            showToast(`GitHub sync: +${j.added} added, ${j.updated} updated`);
            await refreshBoardView();
          } catch (err) { showToast("GitHub sync failed: " + err.message); }
        });
        document.getElementById("board-sync-sentry").addEventListener("click", () => showSentryImportModal());
        document.getElementById("board-autopilots").addEventListener("click", () => showAutopilotsListModal());
      }
      // Per-board cost summary.
      const totalCost = (cards || []).reduce((s, c) => s + (c.cost_usd || 0), 0);
      const runningCount = (cards || []).filter(c => c.status === "running").length;
      document.getElementById("board-cost-summary").textContent =
        `${cards.length} cards · ${runningCount} running · $${totalCost.toFixed(2)} spent`;

      // Update columns and cards
      const existingCols = new Map();
      boardEl.querySelectorAll(".kanban-column").forEach((el) => {
        existingCols.set(el.dataset.columnId, el);
      });

      for (const col of columns) {
        const colCards = cards.filter((c) => c.column_id === col.id);
        let colEl = existingCols.get(col.id);
        if (!colEl) {
          colEl = document.createElement("div");
          colEl.className = "kanban-column";
          colEl.dataset.columnId = col.id;
          colEl.innerHTML = `
            <div class="kanban-column-header">
              <span class="kanban-column-name"></span>
              <span class="kanban-column-count"></span>
            </div>
            <div class="kanban-column-cards"></div>`;
          boardEl.appendChild(colEl);
        }
        colEl.querySelector(".kanban-column-name").textContent = col.name;
        colEl.querySelector(".kanban-column-count").textContent = colCards.length;

        const cardsContainer = colEl.querySelector(".kanban-column-cards");
        const existingCards = new Map();
        cardsContainer.querySelectorAll(".kanban-card").forEach((el) => {
          existingCards.set(el.dataset.cardId, el);
        });

        for (const card of colCards) {
          let cardEl = existingCards.get(card.id);
          const priorityClass = `priority-${card.priority || "p2"}`;
          const typeLabel = card.card_type ? card.card_type.toUpperCase() : "TASK";
          const statusIcon = card.status === "running" ? "⚡" : card.status === "awaiting" ? "⏸" : "";

          if (!cardEl) {
            cardEl = document.createElement("div");
            cardEl.className = `kanban-card ${priorityClass}`;
            cardEl.draggable = true;
            cardEl.dataset.cardId = card.id;
            cardEl.innerHTML = `
              <div class="kanban-card-top">
                <span class="kanban-card-type"></span>
                <span class="kanban-card-priority"></span>
              </div>
              <div class="kanban-card-title"></div>
              <div class="kanban-card-bottom">
                <span class="kanban-card-status"></span>
                <span class="kanban-card-cost"></span>
              </div>`;
            cardEl.addEventListener("click", () => showCardDetailModal(card.id));
            cardsContainer.appendChild(cardEl);
          }
          cardEl.querySelector(".kanban-card-type").textContent = typeLabel;
          cardEl.querySelector(".kanban-card-priority").textContent = card.priority || "p2";
          cardEl.querySelector(".kanban-card-title").textContent = card.title;
          cardEl.querySelector(".kanban-card-status").textContent = (statusIcon + " " + (card.status || "queued")).trim();
          const costEl = cardEl.querySelector(".kanban-card-cost");
          if (card.cost_usd) {
            costEl.textContent = "$" + Number(card.cost_usd).toFixed(2);
          } else {
            costEl.textContent = "";
          }
          existingCards.delete(card.id);
        }
        // Remove stale cards
        for (const el of existingCards.values()) {
          el.remove();
        }
        existingCols.delete(col.id);
      }
      // Remove stale columns
      for (const el of existingCols.values()) {
        el.remove();
      }

      // Re-bind drag-and-drop (columns may have changed)
      setupKanbanDragDrop(boardEl);
    } catch (err) {
      if (!detail.querySelector(".error")) {
        detail.innerHTML = `<div class="error">Failed to load board: ${escapeHtml(err.message)}</div>`;
      }
    } finally {
      boardViewRefreshing = false;
    }
  }

  function setupKanbanDragDrop(boardEl) {
    let draggedId = null;
    boardEl.querySelectorAll(".kanban-card:not([data-dd-wired])").forEach((card) => {
      card.dataset.ddWired = "1";
      card.addEventListener("dragstart", (e) => {
        draggedId = card.dataset.cardId;
        e.dataTransfer.setData("text/plain", draggedId);
        card.classList.add("dragging");
      });
      card.addEventListener("dragend", () => {
        card.classList.remove("dragging");
        draggedId = null;
      });
    });
    boardEl.querySelectorAll(".kanban-column:not([data-dd-wired])").forEach((col) => {
      col.dataset.ddWired = "1";
      col.addEventListener("dragover", (e) => {
        e.preventDefault();
        col.classList.add("drag-over");
      });
      col.addEventListener("dragleave", () => {
        col.classList.remove("drag-over");
      });
      col.addEventListener("drop", async (e) => {
        e.preventDefault();
        col.classList.remove("drag-over");
        const cardId = e.dataTransfer.getData("text/plain");
        const columnId = col.dataset.columnId;
        if (!cardId || !columnId) return;
        try {
          await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}/move`, {
            method: "POST",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json", ...csrfHeaders() },
            body: JSON.stringify({ column_id: columnId }),
          });
          await refreshBoardView();
        } catch (err) {
          showToast("Move failed: " + err.message);
        }
      });
    });
  }

  async function showCreateBoardModal() {
    const name = prompt("Board name:");
    if (!name) return;
    try {
      const res = await fetch(`${API}/boards`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", ...csrfHeaders() },
        body: JSON.stringify({ name }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "create failed");
      await refreshBoardsList();
    } catch (err) {
      showToast("Create board failed: " + err.message);
    }
  }

  async function showCreateCardModal(boardId) {
    const title = prompt("Task title:");
    if (!title) return;
    try {
      const res = await fetch(`${API}/boards/${boardId}/cards`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", ...csrfHeaders() },
        body: JSON.stringify({ title }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "create failed");
      await refreshBoardView();
    } catch (err) {
      showToast("Create card failed: " + err.message);
    }
  }

  async function showCardDetailModal(cardId) {
    try {
      const data = await getJSON(`${API}/boards/${currentBoardId}/cards/${cardId}`);
      const card = data.card;
      const runs = data.runs || [];

      // Fetch personas, board agents, and attachments in parallel.
      const [personas, agents, attachments, preview] = await Promise.all([
        getJSON(`${API}/personas`).then(d => d.personas || []).catch(() => []),
        getJSON(`${API}/boards/${currentBoardId}/agents`).then(d => d.agents || []).catch(() => []),
        getJSON(`${API}/boards/${currentBoardId}/cards/${cardId}/attachments`).catch(() => []),
        getJSON(`${API}/boards/${currentBoardId}/cards/${cardId}/preview`).catch(() => null),
      ]);

      const modal = document.createElement("div");
      modal.className = "modal-overlay";
      modal.id = "card-detail-modal";

      let runsHtml = "";
      for (const run of runs) {
        const statusBadge = `<span class="run-status ${escapeHtml(run.status)}">${escapeHtml(run.status)}</span>`;
        runsHtml += `<div class="run-row" data-run-id="${escapeHtml(run.id)}">
          ${statusBadge}
          <span class="run-agent">${escapeHtml(run.agent_type || run.agent_id || "auto")}</span>
          ${run.cost_usd ? `<span class="run-cost">$${Number(run.cost_usd).toFixed(4)}</span>` : ""}
          ${run.elapsed_ms ? `<span class="run-elapsed muted">${Math.round(run.elapsed_ms/1000)}s</span>` : ""}
          <button class="ghost xs view-run-btn" data-run-id="${escapeHtml(run.id)}">events</button>
          ${run.status === "running" ? `<button class="danger xs stop-run-btn" data-run-id="${escapeHtml(run.id)}">stop</button>` : ""}
        </div>`;
      }

      const personaOptions = [`<option value="">No persona</option>`,
        ...personas.map(p => `<option value="${escapeHtml(p.id)}">${escapeHtml((p.icon || "") + " " + p.name).trim()}</option>`)].join("");
      const agentOptions = [`<option value="">Auto (card assignee)</option>`,
        ...agents.map(a => `<option value="${escapeHtml(a.id)}">${escapeHtml(a.name + " · " + a.agent_type)}</option>`)].join("");

      const attHtml = (attachments && attachments.length > 0)
        ? attachments.map(a => `
          <div class="att-row" data-att-id="${escapeHtml(a.id)}">
            <a href="${API}/attachments/${escapeHtml(a.id)}" target="_blank" class="att-name">${escapeHtml(a.filename)}</a>
            <span class="muted">${prettyBytes(a.size_bytes)}</span>
            <button class="ghost xs att-del" data-att-id="${escapeHtml(a.id)}">×</button>
          </div>`).join("")
        : `<p class="muted">No attachments.</p>`;

      const previewHtml = preview && preview.status === "running"
        ? `<div class="preview-status">
            <span class="run-status running">running</span>
            <a href="${escapeHtml(preview.public_url || preview.local_url)}" target="_blank">${escapeHtml(preview.public_url || preview.local_url)}</a>
            <button class="danger xs" id="preview-stop">stop</button>
          </div>`
        : `<div class="preview-form">
            <input id="preview-cmd" placeholder="dev-server cmd (e.g. npm run dev)" class="input" />
            <button class="primary xs" id="preview-start">Start preview</button>
          </div>`;

      // Look for a pending decision on any of the card's runs.
      let pendingDecisionRunId = null;
      let pendingDecision = null;
      for (const run of runs) {
        if (run.status === "awaiting") {
          try {
            const rd = await getJSON(`${API}/runs/${run.id}`);
            const pd = (rd.decisions || []).find(d => d.status === "pending");
            if (pd) { pendingDecisionRunId = run.id; pendingDecision = pd; break; }
          } catch (_) {}
        }
      }
      const decisionHtml = pendingDecision ? `
        <div class="decision-banner">
          <h4>⏸ Decision needed</h4>
          <p>${escapeHtml(pendingDecision.question)}</p>
          <div class="decision-options">
            ${(pendingDecision.options || []).map(o =>
              `<button class="primary xs decision-opt" data-decision="${escapeHtml(pendingDecision.id)}" data-answer="${escapeHtml(o)}">${escapeHtml(o)}</button>`
            ).join("")}
          </div>
          <input id="decision-custom" placeholder="…or type a custom answer" class="input" />
          <button class="ghost xs" id="decision-submit-custom" data-decision="${escapeHtml(pendingDecision.id)}">Send custom</button>
        </div>` : "";

      modal.innerHTML = `
        <div class="modal modal-wide">
          <div class="modal-header">
            <h3>#${escapeHtml(card.id.slice(0, 8))} · ${escapeHtml(card.title)}</h3>
            <button class="modal-close">&times;</button>
          </div>
          <div class="modal-body">
            <div class="card-meta">
              <span class="badge">${escapeHtml((card.card_type || "feature").toUpperCase())}</span>
              <span class="badge ${card.priority || "p2"}">${escapeHtml(card.priority || "p2")}</span>
              <span class="badge">${escapeHtml(card.status || "queued")}</span>
              ${card.cost_usd ? `<span class="badge cost">$${Number(card.cost_usd).toFixed(4)}</span>` : ""}
            </div>
            <p class="card-description">${escapeHtml(card.description || "No description.")}</p>
            ${decisionHtml}

            <h4>Runs</h4>
            <div class="runs-list" id="modal-runs-list">${runsHtml || "<p class='muted'>No runs yet.</p>"}</div>
            <div id="run-events-pane"></div>

            <h4>Dispatch</h4>
            <div class="dispatch-controls">
              <div class="grid-2">
                <div>
                  <label>Agent</label>
                  <select id="dispatch-agent" class="input">${agentOptions}</select>
                </div>
                <div>
                  <label>Persona</label>
                  <select id="dispatch-persona" class="input">${personaOptions}</select>
                </div>
                <div>
                  <label>Slash command</label>
                  <select id="dispatch-slash" class="input">
                    <option value="">— none —</option>
                    <option value="spec">/spec — write spec, stop before coding</option>
                    <option value="review">/review — review existing work, no edits</option>
                    <option value="split">/split — emit subtasks as JSON</option>
                  </select>
                </div>
                <div>
                  <label>Model (optional)</label>
                  <input id="dispatch-model" class="input" placeholder="e.g. claude-sonnet-4-5" />
                </div>
              </div>
            </div>
            <div class="modal-actions">
              <button class="primary" id="card-dispatch">Dispatch agent</button>
              <button class="ghost" id="card-autopilot">Autopilot…</button>
              <button class="danger" id="card-delete" style="margin-left:auto">Delete card</button>
            </div>

            <h4>Branch preview</h4>
            <div id="preview-pane">${previewHtml}</div>

            <h4>Attachments</h4>
            <div id="att-pane">${attHtml}</div>
            <div class="att-upload">
              <input type="file" id="att-file" class="input" />
              <button class="primary xs" id="att-upload-btn">Upload</button>
            </div>
          </div>
        </div>`;
      document.body.appendChild(modal);

      const cleanup = [];
      const close = () => { modal.remove(); cleanup.forEach(fn => fn()); };
      modal.querySelector(".modal-close").addEventListener("click", close);
      modal.addEventListener("click", (e) => { if (e.target === modal) close(); });

      // ── decision answers (need runID + decisionID for the path)
      modal.querySelectorAll(".decision-opt").forEach(btn => {
        btn.addEventListener("click", async () => {
          await answerDecision(pendingDecisionRunId, btn.dataset.decision, btn.dataset.answer, close);
        });
      });
      const customSubmit = modal.querySelector("#decision-submit-custom");
      if (customSubmit) {
        customSubmit.addEventListener("click", async () => {
          const ans = modal.querySelector("#decision-custom").value.trim();
          if (!ans) return;
          await answerDecision(pendingDecisionRunId, customSubmit.dataset.decision, ans, close);
        });
      }

      // ── dispatch
      document.getElementById("card-dispatch").addEventListener("click", async () => {
        const body = {
          persona_id: document.getElementById("dispatch-persona").value,
          agent_id: document.getElementById("dispatch-agent").value,
          model: document.getElementById("dispatch-model").value,
          slash_command: document.getElementById("dispatch-slash").value,
        };
        try {
          const res = await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}/dispatch`, {
            method: "POST",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json", ...csrfHeaders() },
            body: JSON.stringify(body),
          });
          if (!res.ok) throw new Error((await res.json()).error || "dispatch failed");
          showToast("Agent dispatched");
          close();
          await refreshBoardView();
        } catch (err) {
          showToast("Dispatch failed: " + err.message);
        }
      });

      // ── autopilot
      document.getElementById("card-autopilot").addEventListener("click", () => {
        close();
        showAutopilotModal(cardId, personas);
      });

      // ── delete
      document.getElementById("card-delete").addEventListener("click", async () => {
        if (!confirm("Delete this card?")) return;
        try {
          const res = await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}`, {
            method: "DELETE", credentials: "same-origin", headers: csrfHeaders(),
          });
          if (!res.ok) throw new Error("delete failed");
          close();
          await refreshBoardView();
        } catch (err) {
          showToast("Delete failed: " + err.message);
        }
      });

      // ── stop run
      modal.querySelectorAll(".stop-run-btn").forEach(btn => {
        btn.addEventListener("click", async (e) => {
          e.stopPropagation();
          try {
            await fetch(`${API}/runs/${btn.dataset.runId}/stop`, {
              method: "POST", credentials: "same-origin", headers: csrfHeaders(),
            });
            showToast("Run stop requested");
          } catch (err) { showToast("Stop failed: " + err.message); }
        });
      });

      // ── view events
      modal.querySelectorAll(".view-run-btn").forEach(btn => {
        btn.addEventListener("click", async () => {
          await renderRunEvents(btn.dataset.runId, modal.querySelector("#run-events-pane"));
        });
      });

      // ── preview
      const psBtn = document.getElementById("preview-start");
      if (psBtn) {
        psBtn.addEventListener("click", async () => {
          const cmd = document.getElementById("preview-cmd").value.trim();
          if (!cmd) { showToast("Preview command is required"); return; }
          try {
            const res = await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}/preview`, {
              method: "POST", credentials: "same-origin",
              headers: { "Content-Type": "application/json", ...csrfHeaders() },
              body: JSON.stringify({ cmd }),
            });
            if (!res.ok) throw new Error((await res.json()).error || "preview failed");
            showToast("Preview starting");
            close();
            showCardDetailModal(cardId);
          } catch (err) { showToast("Preview failed: " + err.message); }
        });
      }
      const ppBtn = document.getElementById("preview-stop");
      if (ppBtn) {
        ppBtn.addEventListener("click", async () => {
          try {
            await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}/preview`, {
              method: "DELETE", credentials: "same-origin", headers: csrfHeaders(),
            });
            showToast("Preview stopped");
            close();
            showCardDetailModal(cardId);
          } catch (err) { showToast("Stop preview failed: " + err.message); }
        });
      }

      // ── attachments: upload + delete
      document.getElementById("att-upload-btn").addEventListener("click", async () => {
        const input = document.getElementById("att-file");
        if (!input.files || input.files.length === 0) { showToast("Pick a file first"); return; }
        const fd = new FormData();
        fd.append("file", input.files[0]);
        try {
          const res = await fetch(`${API}/boards/${currentBoardId}/cards/${cardId}/attachments`, {
            method: "POST", credentials: "same-origin", headers: csrfHeaders(), body: fd,
          });
          if (!res.ok) throw new Error("upload failed");
          showToast("Uploaded");
          close();
          showCardDetailModal(cardId);
        } catch (err) { showToast("Upload failed: " + err.message); }
      });
      modal.querySelectorAll(".att-del").forEach(btn => {
        btn.addEventListener("click", async () => {
          if (!confirm("Delete attachment?")) return;
          try {
            await fetch(`${API}/attachments/${btn.dataset.attId}`, {
              method: "DELETE", credentials: "same-origin", headers: csrfHeaders(),
            });
            close();
            showCardDetailModal(cardId);
          } catch (err) { showToast("Delete failed: " + err.message); }
        });
      });
    } catch (err) {
      showToast("Load card failed: " + err.message);
    }
  }

  async function answerDecision(runId, decisionId, answer, closeFn) {
    try {
      const res = await fetch(`${API}/runs/${runId}/decisions/${decisionId}`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", ...csrfHeaders() },
        body: JSON.stringify({ answer }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "answer failed");
      showToast("Decision sent — agent resuming");
      if (closeFn) closeFn();
      await refreshBoardView();
    } catch (err) {
      showToast("Answer failed: " + err.message);
    }
  }

  async function renderRunEvents(runId, pane) {
    try {
      const data = await getJSON(`${API}/runs/${runId}`);
      const events = data.events || [];
      const rows = events.slice(-200).map(e => {
        const kind = e.kind || "text";
        const phase = e.phase ? ` <span class="muted xs">[${escapeHtml(e.phase)}]</span>` : "";
        return `<div class="evt evt-${escapeHtml(kind)}">
          <span class="muted xs">${escapeHtml(kind)}</span>${phase}
          <pre>${escapeHtml(e.message || "")}</pre>
        </div>`;
      }).join("");
      pane.innerHTML = `
        <div class="run-events">
          <div class="run-events-head">
            <strong>Events — ${escapeHtml(runId.slice(0,8))}</strong>
            <button class="ghost xs" id="run-events-refresh">refresh</button>
          </div>
          <div class="run-events-body">${rows || `<p class="muted">No events yet.</p>`}</div>
        </div>`;
      document.getElementById("run-events-refresh").addEventListener("click", () => renderRunEvents(runId, pane));
    } catch (err) { pane.innerHTML = `<p class="error">Failed to load events: ${escapeHtml(err.message)}</p>`; }
  }

  function prettyBytes(n) {
    if (!n) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return n.toFixed(i === 0 ? 0 : 1) + " " + units[i];
  }

  // ── sentry import modal ──────────────────────────────────────────────────
  function showSentryImportModal() {
    const modal = document.createElement("div");
    modal.className = "modal-overlay";
    modal.innerHTML = `
      <div class="modal">
        <div class="modal-header">
          <h3>Import Sentry issues</h3>
          <button class="modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <p class="muted">Fetches Sentry issues for the project and upserts them as cards in this board's first column.</p>
          <label>Sentry org slug</label>
          <input id="sentry-org" class="input" placeholder="acme-corp" />
          <label>Sentry project slug</label>
          <input id="sentry-project" class="input" placeholder="backend-api" />
          <label>Query (defaults to is:unresolved)</label>
          <input id="sentry-query" class="input" placeholder="is:unresolved level:error" />
          <div class="modal-actions">
            <button class="primary" id="sentry-go">Import</button>
          </div>
        </div>
      </div>`;
    document.body.appendChild(modal);
    const close = () => modal.remove();
    modal.querySelector(".modal-close").addEventListener("click", close);
    modal.addEventListener("click", e => { if (e.target === modal) close(); });
    document.getElementById("sentry-go").addEventListener("click", async () => {
      const body = {
        org: document.getElementById("sentry-org").value.trim(),
        project: document.getElementById("sentry-project").value.trim(),
        query: document.getElementById("sentry-query").value.trim(),
      };
      if (!body.org || !body.project) { showToast("org and project are required"); return; }
      try {
        const res = await fetch(`${API}/boards/${currentBoardId}/sentry/import`, {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/json", ...csrfHeaders() },
          body: JSON.stringify(body),
        });
        if (!res.ok) throw new Error((await res.json()).error || "import failed");
        const j = await res.json();
        showToast(`Sentry: +${j.added} added, ${j.updated} updated`);
        close();
        await refreshBoardView();
      } catch (err) { showToast("Sentry import failed: " + err.message); }
    });
  }

  // ── autopilot sessions list ──────────────────────────────────────────────
  async function showAutopilotsListModal() {
    let sessions = [];
    try { sessions = await getJSON(`${API}/autopilot`); } catch (_) {}
    const modal = document.createElement("div");
    modal.className = "modal-overlay";
    const rows = (sessions || []).map(s => `
      <div class="ap-row">
        <span class="run-status ${escapeHtml(s.status)}">${escapeHtml(s.status)}</span>
        <span>${escapeHtml(s.mode)}</span>
        <span class="muted">card ${escapeHtml((s.card_id || "").slice(0,8))}</span>
        <span class="muted">cycles ${s.cycles || 0}</span>
        <span class="muted">$${Number(s.total_cost_usd || 0).toFixed(4)}</span>
        ${s.status === "running" ? `<button class="danger xs" data-stop="${escapeHtml(s.id)}">stop</button>` : ""}
      </div>`).join("");
    modal.innerHTML = `
      <div class="modal modal-wide">
        <div class="modal-header">
          <h3>Autopilot sessions</h3>
          <button class="modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <div class="ap-list">${rows || "<p class='muted'>No autopilot sessions.</p>"}</div>
        </div>
      </div>`;
    document.body.appendChild(modal);
    const close = () => modal.remove();
    modal.querySelector(".modal-close").addEventListener("click", close);
    modal.addEventListener("click", e => { if (e.target === modal) close(); });
    modal.querySelectorAll("[data-stop]").forEach(b => {
      b.addEventListener("click", async () => {
        try {
          await fetch(`${API}/autopilot/${b.dataset.stop}/stop`, {
            method: "POST", credentials: "same-origin", headers: csrfHeaders(),
          });
          showToast("Stopped");
          close();
          showAutopilotsListModal();
        } catch (err) { showToast("Stop failed: " + err.message); }
      });
    });
  }

  // ── autopilot modal ──────────────────────────────────────────────────────
  async function showAutopilotModal(cardId, personas) {
    const modal = document.createElement("div");
    modal.className = "modal-overlay";
    modal.innerHTML = `
      <div class="modal">
        <div class="modal-header">
          <h3>Autopilot</h3>
          <button class="modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <div class="ap-mode-tabs">
            <button class="ap-tab active" data-mode="feature-dev">feature-dev</button>
            <button class="ap-tab" data-mode="qa">qa</button>
          </div>

          <div class="ap-panel" data-mode="feature-dev">
            <label>Personas (round-robin)</label>
            <select id="ap-personas" class="input" multiple style="height:80px">
              ${personas.map(p => `<option value="${escapeHtml(p.id)}">${escapeHtml(p.name)}</option>`).join("")}
            </select>
            <label>Parallelism</label>
            <input id="ap-parallelism" type="number" min="1" max="4" value="1" class="input" />
            <label>Max cycles (0 = unbounded)</label>
            <input id="ap-cycles" type="number" min="0" value="3" class="input" />
            <label>Session budget (USD)</label>
            <input id="ap-budget" type="number" step="0.01" min="0" value="5" class="input" />
          </div>

          <div class="ap-panel hidden" data-mode="qa">
            <label>Check commands (one per line)</label>
            <textarea id="ap-checks" class="input" rows="5" placeholder="go test ./...&#10;go vet ./...&#10;npm run lint"></textarea>
            <label>Max fix attempts per check</label>
            <input id="ap-fix-attempts" type="number" min="1" value="3" class="input" />
            <label>Session budget (USD)</label>
            <input id="ap-qa-budget" type="number" step="0.01" min="0" value="5" class="input" />
          </div>

          <div class="modal-actions">
            <button class="primary" id="ap-start">Start autopilot</button>
          </div>
        </div>
      </div>`;
    document.body.appendChild(modal);
    const close = () => modal.remove();
    modal.querySelector(".modal-close").addEventListener("click", close);
    modal.addEventListener("click", e => { if (e.target === modal) close(); });

    let currentMode = "feature-dev";
    modal.querySelectorAll(".ap-tab").forEach(t => {
      t.addEventListener("click", () => {
        modal.querySelectorAll(".ap-tab").forEach(x => x.classList.toggle("active", x === t));
        currentMode = t.dataset.mode;
        modal.querySelectorAll(".ap-panel").forEach(p => p.classList.toggle("hidden", p.dataset.mode !== currentMode));
      });
    });

    document.getElementById("ap-start").addEventListener("click", async () => {
      let body;
      if (currentMode === "feature-dev") {
        const personaIds = Array.from(document.getElementById("ap-personas").selectedOptions).map(o => o.value);
        body = {
          card_id: cardId, mode: "feature-dev",
          persona_ids: personaIds,
          parallelism: parseInt(document.getElementById("ap-parallelism").value || "1", 10),
          max_cycles: parseInt(document.getElementById("ap-cycles").value || "0", 10),
          budget_usd: parseFloat(document.getElementById("ap-budget").value || "0"),
        };
      } else {
        const lines = document.getElementById("ap-checks").value.split("\n").map(s => s.trim()).filter(Boolean);
        body = {
          card_id: cardId, mode: "qa",
          checks: lines.map((cmd, i) => ({ name: "check-" + (i+1), cmd })),
          max_fix_attempts: parseInt(document.getElementById("ap-fix-attempts").value || "3", 10),
          budget_usd: parseFloat(document.getElementById("ap-qa-budget").value || "0"),
        };
      }
      try {
        const res = await fetch(`${API}/autopilot`, {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/json", ...csrfHeaders() },
          body: JSON.stringify(body),
        });
        if (!res.ok) throw new Error((await res.json()).error || "start failed");
        showToast("Autopilot started");
        close();
        await refreshBoardView();
      } catch (err) { showToast("Autopilot failed: " + err.message); }
    });
  }
})();
