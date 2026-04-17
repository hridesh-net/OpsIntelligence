// OpsIntelligence dashboard shell (phase 2c).
//
// Responsibilities are deliberately minimal:
//   1. On the login page: decide whether to show "sign in" or
//      "bootstrap owner" based on /api/v1/auth/status.
//   2. Submit the appropriate form, then redirect to /dashboard/app.
//   3. On the app page: render whoami + navigate between placeholder
//      panels. Everything else lands in phase 3c.
//
// The gateway mounts the dashboard under /dashboard/, so every URL
// in here is written relative to that prefix.

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

  async function bootAppPage() {
    const me = await getJSON(`${API}/whoami`).catch(() => null);
    if (!me || me.type !== "user") {
      window.location.href = `${DASH}/login`;
      return;
    }
    renderWhoami(me);
    wireSidebar();
    wireLogout();
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

  function wireSidebar() {
    const items = document.querySelectorAll(".nav-item");
    const panels = document.querySelectorAll(".panel");
    const title = document.getElementById("section-title");
    items.forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.section;
        items.forEach((x) => x.classList.toggle("active", x === btn));
        panels.forEach((p) => p.classList.toggle("hidden", p.dataset.section !== id));
        title.textContent = btn.textContent.trim();
      });
    });
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

  // ─────────────────────── utilities ───────────────────────

  async function getJSON(url) {
    const res = await fetch(url, { credentials: "same-origin" });
    if (!res.ok) throw new Error(`${url} returned ${res.status}`);
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
})();
