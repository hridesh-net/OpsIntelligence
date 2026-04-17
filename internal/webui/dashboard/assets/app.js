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
        subEl.textContent = "Background runs supervised by the master agent.";
        break;
      case "users":
        titleEl.textContent = "Users & Roles";
        subEl.textContent = "Identity, roles and permissions.";
        break;
      case "apikeys":
        titleEl.textContent = "API keys";
        subEl.textContent = "Long-lived bearer credentials for automation.";
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
        <p>Pick a section on the left to view or edit it.</p>
        <p class="note">
          Every form here calls the same <code>configsvc</code> the CLI does,
          so changes show up in <code>opsintelligence.yaml</code> immediately.
        </p>
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
      fields: [
        { key: "host", label: "Host", type: "text", help: "Listen address (e.g. 127.0.0.1, 0.0.0.0)." },
        { key: "port", label: "Port", type: "number", min: 1, max: 65535 },
        {
          key: "bind",
          label: "Bind mode",
          type: "select",
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
      ],
    },

    webhooks: {
      summary: "Inbound webhook endpoints. Adapters (typed) take precedence over generic mappings.",
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
