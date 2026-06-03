/* scrun/api.js — thin client for /api/v1/boards.
 *
 * Maps backend kanban shapes onto the structure the rest of the Scrun
 * shell expects (DB.AGENTS / DB.WORKFLOW / DB.CARDS / DB.AGENT_STATS).
 *
 * Read-only for v1.0.51. Mutations (drag, create, edit) still update
 * DB in-place; write-back to the API lands in v1.0.52. */
const ScrunAPI = (function () {
  const BASE = "/api/v1";

  async function jget(path) {
    const r = await fetch(BASE + path, { credentials: "same-origin" });
    if (!r.ok) throw new Error(`${r.status} ${r.statusText} on ${path}`);
    return r.json();
  }

  async function listBoards() {
    const j = await jget("/boards");
    return j.boards || [];
  }

  async function getBoard(id) {
    const j = await jget(`/boards/${encodeURIComponent(id)}`);
    return j; // {board, columns, cards}
  }

  async function listAgents(boardID) {
    const j = await jget(`/boards/${encodeURIComponent(boardID)}/agents`);
    return j.agents || [];
  }

  async function getAgent(boardID, agentID) {
    return jget(`/boards/${encodeURIComponent(boardID)}/agents/${encodeURIComponent(agentID)}`);
  }

  async function updateAgent(boardID, agentID, patch) {
    const r = await fetch(
      `${BASE}/boards/${encodeURIComponent(boardID)}/agents/${encodeURIComponent(agentID)}`,
      {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", ...csrfHeaders() },
        body: JSON.stringify(patch),
      });
    if (!r.ok) throw new Error(`Update agent failed: ${r.statusText}`);
    return r.json();
  }

  // saveWorkflow ships the Workflow Builder's full intended state in
  // one PUT. The server diffs against current rows, upserts each column
  // (rows with an `id` already on the board are UPDATEd, rows without
  // are INSERTed) and DELETEs any IDs in `deleted` whose column has no
  // cards. A 409 lists the blocked column IDs in the `columns` field.
  async function saveWorkflow(boardID, payload) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/workflow`, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify(payload),
    });
    if (!r.ok) {
      let body = null;
      try { body = await r.json(); } catch (e) {}
      const err = new Error(`Save workflow failed: ${r.status}`);
      if (body && body.error) err.message = body.error;
      if (body && Array.isArray(body.columns)) err.blocked = body.columns;
      throw err;
    }
    return r.json();
  }

  // streamRunEvents opens a Server-Sent Events subscription on the
  // /api/v1/runs/{runID}/events endpoint and forwards each event /
  // lifecycle message to the supplied callback. Returns a close()
  // function the caller must invoke to release the connection.
  //
  //   const sub = ScrunAPI.streamRunEvents("run-id", {
  //     onEvent: (ev) => { ... },
  //     onLifecycle: (ev) => { ... },
  //     onError: (e) => { ... },
  //   });
  //   // later:
  //   sub.close();
  function streamRunEvents(runID, opts) {
    opts = opts || {};
    const url = `${BASE}/runs/${encodeURIComponent(runID)}/events`;
    let src;
    try {
      src = new EventSource(url, { withCredentials: true });
    } catch (e) {
      if (opts.onError) opts.onError(e);
      return { close: () => {} };
    }
    const parse = (raw) => { try { return JSON.parse(raw); } catch (e) { return null; } };
    src.addEventListener("event", (e) => {
      const ev = parse(e.data);
      if (ev && opts.onEvent) opts.onEvent(ev);
    });
    src.addEventListener("lifecycle", (e) => {
      const ev = parse(e.data);
      if (ev && opts.onLifecycle) opts.onLifecycle(ev);
    });
    src.onerror = (e) => {
      // EventSource auto-reconnects with Last-Event-ID; we don't have
      // to do anything special unless the caller wants a callback.
      if (opts.onError) opts.onError(e);
    };
    return { close: () => { try { src.close(); } catch (e) {} } };
  }

  async function deleteAgent(boardID, agentID) {
    const r = await fetch(
      `${BASE}/boards/${encodeURIComponent(boardID)}/agents/${encodeURIComponent(agentID)}`,
      {
        method: "DELETE",
        credentials: "same-origin",
        headers: csrfHeaders(),
      });
    if (!r.ok) {
      let msg = r.statusText;
      try { const j = await r.json(); if (j && j.error) msg = j.error; } catch (e) {}
      throw new Error(`Delete agent failed: ${msg}`);
    }
  }

  // ── Mapping helpers ────────────────────────────────────────────────

  // Stable two-letter initials from agent name.
  function ini(name) {
    if (!name) return "AG";
    const parts = String(name).trim().split(/\s+/);
    if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
    return name.slice(0, 2).toUpperCase();
  }

  // Pick a stable colour per agent id so two agents don't share one.
  const AGENT_COLORS = [
    "#2898da", "#f4685f", "#2dd4bf", "#a78bfa",
    "#34d399", "#f5b042", "#60a5fa", "#fb7185",
  ];
  function colorFor(idx) { return AGENT_COLORS[idx % AGENT_COLORS.length]; }

  // BoardAgent → Scrun's AGENTS entry.
  function mapAgent(a, idx) {
    const cfg = a.config || {};
    return {
      name: a.name,
      color: cfg.color || colorFor(idx),
      ini: cfg.ini || ini(a.name),
      model: cfg.model || a.agent_type || "—",
      provider: cfg.provider || a.provider_id || "",
      caps: cfg.capabilities || cfg.caps || [],
      role: cfg.role || "",
      instructions: cfg.instructions || cfg.system_prompt || "",
      knowledge: cfg.knowledge || [],
      memory: cfg.memory || { mode: "session", scope: "project", contextK: 64, retention: "7d" },
      autonomy: cfg.autonomy || "supervised",
      spendCap: cfg.spend_cap_daily || cfg.spendCap || 10,
      maxParallel: cfg.max_parallel || cfg.maxParallel || 2,
      _serverId: a.id,
    };
  }

  // BoardColumn (+ board.config.column_overrides) → Scrun stage.
  function mapColumn(col, override) {
    const ov = override || {};
    // Gate is stored at the column level; override can still override it
    // Backend stores: "none", "human", "auto-validate"
    let gate = ov.gate || col.gate || null;
    if (gate === "none" || gate === "") gate = null;
    if (gate === "auto-validate") gate = "auto";
    return {
      id: col.id,
      name: col.name,
      dot: col.color || "#586675",
      wip: col.wip_limit || 0,
      gate: gate,
      rules: ov.automation || { autoAssign: null, autoValidate: gate === "auto" },
      _serverId: col.id,
    };
  }

  // BoardCard → Scrun card.
  function mapCard(c, columnMap) {
    const md = c.metadata || {};
    return {
      id: c.id,
      col: c.column_id, // Scrun keys by stage id; column.id matches.
      type: shortType(c.card_type),
      prio: shortPrio(c.priority),
      title: c.title,
      desc: c.description || "",
      agents: c.assignee ? [c.assignee] : [],
      status: mapStatus(c.status),
      labels: md.labels || [],
      ac: md.acceptance_criteria || [],
      progress: md.progress || 0,
      branch: c.branch || md.branch || "",
      add: md.add || 0,
      del: md.del || 0,
      cost: c.cost_usd || 0,
      tokens: (c.token_in || 0) + (c.token_out || 0),
      duration: md.duration || "",
      eta: md.eta || "",
      conf: md.confidence || 0,
      tests: md.tests || "",
      when: relTime(c.updated_at || c.created_at),
      logs: md.logs || [],
      hitl: md.hitl || null,
      _serverId: c.id,
    };
  }

  // Backend card_type → Scrun's compact codes used by filter chips.
  function shortType(t) {
    if (!t) return "feat";
    const lower = String(t).toLowerCase();
    if (lower === "feature") return "feat";
    if (lower === "bug") return "fix";
    if (lower === "infrastructure") return "infra";
    if (lower === "security") return "sec";
    return lower; // research / chore / refactor pass through
  }

  // Backend priority (p0/p1/p2/p3) → Scrun's H/M/L.
  function shortPrio(p) {
    if (!p) return "M";
    if (p === "p0" || p === "p1") return "H";
    if (p === "p3") return "L";
    return "M";
  }

  function mapStatus(s) {
    // Scrun states: queued | running | awaiting | done
    if (!s) return "queued";
    if (s === "completed") return "done";
    return s; // queued | running | awaiting pass through
  }

  function relTime(iso) {
    if (!iso) return "—";
    const t = new Date(iso).getTime();
    if (Number.isNaN(t)) return "—";
    const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m`;
    if (s < 86400) return `${Math.floor(s / 3600)}h`;
    return `${Math.floor(s / 86400)}d`;
  }

  // ── Top-level loader ───────────────────────────────────────────────
  //
  // Returns {ok, board, agents, workflow, cards, stats} so the caller
  // can drop the result onto DB in-place. ok=false signals "fall back
  // to Demo fixtures" — either no boards exist or the API errored.

  async function loadFirstBoard() {
    try {
      const boards = await listBoards();
      if (!boards.length) return { ok: false, reason: "no-boards" };
      // Remember last opened board so multi-board users land on
      // the same one each visit.
      let pick = boards[0];
      const saved = localStorage.getItem("scrun.lastBoard");
      if (saved) {
        const hit = boards.find((b) => b.id === saved);
        if (hit) pick = hit;
      }
      localStorage.setItem("scrun.lastBoard", pick.id);

      const [detail, agents] = await Promise.all([
        getBoard(pick.id),
        listAgents(pick.id).catch(() => []),
      ]);

      const overrides =
        (detail.board && detail.board.config && detail.board.config.column_overrides) || {};
      const columns = (detail.columns || []).map((c) =>
        mapColumn(c, overrides[c.id])
      );
      const cards = (detail.cards || []).map(mapCard);

      // Index AGENTS by server id so card.agents (which carries server
      // ids) resolves cleanly inside the rest of Scrun.
      const agentsObj = {};
      const stats = {};
      agents.forEach((a, idx) => {
        agentsObj[a.id] = mapAgent(a, idx);
        stats[a.id] = { tasks: 0, success: 0, spend: 0 };
      });

      return {
        ok: true,
        boardName: detail.board && detail.board.name,
        agents: agentsObj,
        workflow: columns,
        cards,
        stats,
      };
    } catch (e) {
      console.warn("[scrun] API load failed; falling back to demo:", e);
      return { ok: false, reason: "error", error: e };
    }
  }

  function csrfHeaders() {
    const match = document.cookie.match(/(?:^|; )opi_csrf=([^;]*)/);
    const tok = match ? decodeURIComponent(match[1]) : "";
    return tok ? { "X-CSRF-Token": tok } : {};
  }

  async function moveCard(boardID, cardID, columnID) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}/move`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify({ column_id: columnID }),
    });
    if (!r.ok) throw new Error(`Move card failed: ${r.statusText}`);
    return r.json();
  }

  async function createCard(boardID, card) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/cards`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify(card),
    });
    if (!r.ok) throw new Error(`Create card failed: ${r.statusText}`);
    return r.json();
  }

  async function updateCard(boardID, cardID, cardUpdates) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}`, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify(cardUpdates),
    });
    if (!r.ok) throw new Error(`Update card failed: ${r.statusText}`);
    return r.json();
  }

  async function deleteCard(boardID, cardID) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}`, {
      method: "DELETE",
      credentials: "same-origin",
      headers: csrfHeaders(),
    });
    if (!r.ok) throw new Error(`Delete card failed: ${r.statusText}`);
  }

  async function dispatchAgent(boardID, cardID, agentID, personaID, model, slashCommand) {
    const r = await fetch(`${BASE}/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}/dispatch`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify({
        agent_id: agentID || undefined,
        persona_id: personaID || undefined,
        model: model || undefined,
        slash_command: slashCommand || undefined,
      }),
    });
    if (!r.ok) throw new Error(`Dispatch agent failed: ${r.statusText}`);
    return r.json();
  }

  async function stopRun(runID) {
    const r = await fetch(`${BASE}/runs/${encodeURIComponent(runID)}/stop`, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders(),
    });
    if (!r.ok) throw new Error(`Stop run failed: ${r.statusText}`);
    return r.json();
  }

  async function answerDecision(runID, decisionID, answer) {
    const r = await fetch(`${BASE}/runs/${encodeURIComponent(runID)}/decisions/${encodeURIComponent(decisionID)}`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify({ answer }),
    });
    if (!r.ok) throw new Error(`Answer decision failed: ${r.statusText}`);
    return r.json();
  }

  async function createBoard(boardData) {
    const r = await fetch(`${BASE}/boards`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...csrfHeaders() },
      body: JSON.stringify(boardData),
    });
    if (!r.ok) throw new Error(`Create board failed: ${r.statusText}`);
    return r.json();
  }

  async function getCardDetails(boardID, cardID) {
    const j = await jget(`/boards/${encodeURIComponent(boardID)}/cards/${encodeURIComponent(cardID)}`);
    return j;
  }

  async function getRunDetails(runID) {
    const j = await jget(`/runs/${encodeURIComponent(runID)}`);
    return j;
  }

  async function listPersonas() {
    const j = await jget("/personas");
    return j.personas || [];
  }

  return {
    listBoards,
    getBoard,
    listAgents,
    getAgent,
    updateAgent,
    deleteAgent,
    streamRunEvents,
    saveWorkflow,
    loadFirstBoard,
    moveCard,
    createCard,
    updateCard,
    deleteCard,
    dispatchAgent,
    stopRun,
    answerDecision,
    createBoard,
    getCardDetails,
    getRunDetails,
    listPersonas,
  };
})();
window.ScrunAPI = ScrunAPI;
