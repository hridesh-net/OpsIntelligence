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

  async function bootAppPage() {
    const me = await getJSON(`${API}/whoami`).catch(() => null);
    if (!me || me.type !== "user") {
      window.location.href = `${DASH}/login`;
      return;
    }
    ME = me;
    renderWhoami(me);
    wireLogout();
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
    const { view, sub } = parseHash();
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
    { key: "vertex", label: "Google Vertex", kind: "vertex" },
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
          { key: "default_model", label: "Default model", type: "text" },
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
    let graphNetwork = null;
    function showTab(name) {
      if (graphNetwork) {
        try {
          graphNetwork.destroy();
        } catch (_) {
          /* ignore */
        }
        graphNetwork = null;
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
        if (typeof vis === "undefined" || !vis.DataSet || !vis.Network) {
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
          const arch =
            memory && memory.architecture
              ? escapeHTML(String(memory.architecture).slice(0, 1400))
              : "";
          const hints =
            memory && memory.review_hints
              ? escapeHTML(String(memory.review_hints).slice(0, 900))
              : "";
          const kf = memory && Array.isArray(memory.key_files) ? memory.key_files : [];
          const kfList = kf
            .map((f) => {
              const p = typeof f === "string" ? f : f.path || JSON.stringify(f);
              return `<li class="mono-cell">${escapeHTML(p)}</li>`;
            })
            .join("");
          const rawNodes = cg.nodes;
          const edgesArr = Array.isArray(cg.edges) ? cg.edges : [];
          const importEdgeCount = edgesArr.filter((e) => e.kind === "import").length;
          const callEdgeCount = edgesArr.length - importEdgeCount;
          const libPkgAllowed = !!fullRepo.show_callgraph_library_packages;
          const showImportsInitially = libPkgAllowed && importEdgeCount <= 22;
          const orientHint = libPkgAllowed
            ? `Solid: function calls. Dashed: imports / packages. Busy graphs default to <strong>calls only</strong> — use the toolbar checkbox to show import edges. Click a node for file and line.`
            : `Solid: <strong>function calls only</strong>. External library / package nodes and import edges stay hidden until an operator sets <code>repo_intel.show_callgraph_library_packages: true</code> (Settings → Repo Intelligence → <strong>Show external library / package nodes in call graph</strong>) and <strong>restarts the gateway</strong> so the process picks up config. Then reload this page. Click a node for file and line.`;
          const toolbarImports = libPkgAllowed
            ? `<label class="repo-graph-toolbar-label">
                    <input type="checkbox" id="repo-graph-show-imports" ${showImportsInitially ? "checked" : ""} />
                    Show package / import edges
                  </label>
                  <span class="repo-graph-toolbar-meta muted">${callEdgeCount} call · ${importEdgeCount} import</span>`
            : `<p class="repo-graph-toolbar-note muted">Import / package edges hidden by policy (${importEdgeCount} not shown). Enable <strong>Show external library / package nodes in call graph</strong> under Settings → Repo Intelligence, save, restart the gateway, and reload this page.</p>
                  <span class="repo-graph-toolbar-meta muted">${callEdgeCount} call edges</span>`;
          tabContent.innerHTML = `
            <div class="repo-graph-layout">
              <aside class="repo-graph-sidebar">
                <h3 class="repo-graph-sidebar-title">Orient</h3>
                <p class="repo-graph-sidebar-hint muted">${orientHint}</p>
                ${arch ? `<section class="repo-graph-section"><h4>Architecture</h4><div class="repo-graph-prose">${arch}</div></section>` : ""}
                ${kfList ? `<section class="repo-graph-section"><h4>Key files</h4><ul class="repo-graph-file-list">${kfList}</ul></section>` : ""}
                ${hints ? `<section class="repo-graph-section"><h4>Review focus</h4><div class="repo-graph-prose">${hints}</div></section>` : ""}
                <div id="repo-graph-node-detail" class="repo-graph-node-detail muted">Click a node…</div>
              </aside>
              <div class="repo-graph-canvas-wrap">
                <div class="repo-graph-toolbar">
                  ${toolbarImports}
                  <button type="button" class="ghost" id="repo-graph-fit">Fit view</button>
                  <button type="button" class="ghost" id="repo-graph-physics">Re-layout</button>
                </div>
                <div id="repo-vis-network" class="repo-vis-network" role="img" aria-label="Call graph"></div>
              </div>
            </div>`;
          const detailEl = tabContent.querySelector("#repo-graph-node-detail");
          const toVisNodes = (showImports) =>
            rawNodes
              .filter((n) => showImports || n.kind !== "module")
              .map((n) => ({
                id: n.id,
                label: n.name || n.id,
                title:
                  n.kind === "module" || n.kind === "file"
                    ? `${n.name}\n${n.file}`
                    : `${n.name}\n${n.file}:${n.line}`,
                group:
                  n.kind === "module" ? "module" : n.kind === "file" ? "file" : n.file || "unknown",
              }));
          const toVisEdges = (showImports) =>
            edgesArr
              .filter((e) => showImports || e.kind !== "import")
              .map((e) => ({
                id: `${e.from}→${e.to}→${e.kind}`,
                from: e.from,
                to: e.to,
                arrows: "to",
                dashes: e.kind === "import",
                color:
                  e.kind === "import"
                    ? { color: "#5c6570", highlight: "#8b98a8" }
                    : { color: "#5b8def", highlight: "#9dc0ff" },
              }));
          const el = tabContent.querySelector("#repo-vis-network");
          if (!el) {
            return;
          }
          let showImports = libPkgAllowed && showImportsInitially;
          const data = {
            nodes: new vis.DataSet(toVisNodes(showImports)),
            edges: new vis.DataSet(toVisEdges(showImports)),
          };
          const opts = {
            physics: {
              enabled: true,
              stabilization: { iterations: 260, updateInterval: 30, fit: true },
              barnesHut: {
                gravitationalConstant: -10000,
                centralGravity: 0.1,
                springLength: 220,
                springConstant: 0.032,
                damping: 0.58,
              },
            },
            edges: {
              smooth: { type: "continuous", roundness: 0.22 },
              selectionWidth: 2,
            },
            groups: {
              module: {
                color: { background: "#6e7681", border: "#484f58" },
                font: { color: "#f0f0f0", size: 13 },
              },
              file: {
                color: { background: "#1f6feb55", border: "#388bfd" },
                font: { color: "#f0f6fc", size: 13 },
              },
            },
            nodes: {
              font: { size: 14, face: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" },
              margin: 12,
              borderWidth: 1,
            },
            interaction: { hover: true, tooltipDelay: 80, zoomView: true, dragView: true },
          };
          graphNetwork = new vis.Network(el, data, opts);
          graphNetwork.once("stabilizationIterationsDone", () => {
            try {
              graphNetwork.setOptions({ physics: false });
              graphNetwork.fit({ animation: { duration: 280, easingFunction: "easeInOutQuad" } });
            } catch (_) {
              /* ignore */
            }
          });
          graphNetwork.on("click", (params) => {
            if (!detailEl || !params.nodes.length) {
              return;
            }
            const nid = params.nodes[0];
            const node = rawNodes.find((x) => x.id === nid);
            if (!node) {
              return;
            }
            const kind = node.kind === "module" || node.kind === "file" ? node.kind : node.kind || "function";
            const loc =
              node.kind === "module" || node.kind === "file" ? node.file : `${node.file}:${node.line}`;
            detailEl.innerHTML = `<strong>${escapeHTML(node.name || nid)}</strong> <span class="pill">${escapeHTML(kind)}</span><br/><span class="mono-cell">${escapeHTML(loc)}</span>`;
          });
          const chk = libPkgAllowed ? tabContent.querySelector("#repo-graph-show-imports") : null;
          if (chk) {
            chk.addEventListener("change", () => {
              showImports = chk.checked;
              data.nodes.clear();
              data.edges.clear();
              data.nodes.add(toVisNodes(showImports));
              data.edges.add(toVisEdges(showImports));
              graphNetwork.setData(data);
              graphNetwork.setOptions({ physics: true });
              graphNetwork.startSimulation();
              graphNetwork.once("stabilizationIterationsDone", () => {
                try {
                  graphNetwork.setOptions({ physics: false });
                  graphNetwork.fit({ animation: { duration: 220, easingFunction: "easeInOutQuad" } });
                } catch (_) {
                  /* ignore */
                }
              });
            });
          }
          const fitBtn = tabContent.querySelector("#repo-graph-fit");
          if (fitBtn) {
            fitBtn.addEventListener("click", () => {
              try {
                graphNetwork.fit({ animation: { duration: 220, easingFunction: "easeInOutQuad" } });
              } catch (_) {
                /* ignore */
              }
            });
          }
          const physBtn = tabContent.querySelector("#repo-graph-physics");
          if (physBtn) {
            physBtn.addEventListener("click", () => {
              try {
                graphNetwork.setOptions({ physics: true });
                graphNetwork.startSimulation();
                graphNetwork.once("stabilizationIterationsDone", () => {
                  graphNetwork.setOptions({ physics: false });
                });
              } catch (_) {
                /* ignore */
              }
            });
          }
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
    t.className = `toast toast-${kind || "info"}`;
    setTimeout(() => {
      t.className = "toast hidden";
    }, 4500);
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
})();
