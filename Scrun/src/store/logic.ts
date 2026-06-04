/* ============================================================
   SCRUN — Core domain logic (pure mutators over the draft)
   Ported from board.js / sim.js / panel.js / screens.js / setup.js.
   Every function mutates the Immer draft in place; React re-renders
   from the resulting state (no manual DOM patching needed).
   ============================================================ */
import type { AgentKey, Card, Stage, StageGate } from "../types";
import { nowTime, pad2 } from "../lib/helpers";
import type { AppData } from "./state";

const rnd = (n: number) => Math.floor(Math.random() * n);

/* ---------- lookups ---------- */
export const stageOf = (s: AppData, id: string): Stage | undefined =>
  s.workflow.find((x) => x.id === id);

export function nextId(s: AppData): string {
  return s.prefix + "-" + s.seq++;
}
export function setPrefix(s: AppData, p: string): void {
  s.prefix = (p || "AI").toUpperCase();
}

/* ---------- filtering ---------- */
export function cardVisible(s: AppData, k: Card): boolean {
  const f = s.filters;
  if (f.agent !== "all" && !k.agents.includes(f.agent)) return false;
  if (f.prio !== "all" && k.prio !== f.prio) return false;
  if (f.type !== "all" && k.type !== f.type) return false;
  if (f.q) {
    const q = f.q.toLowerCase();
    const hit =
      k.title.toLowerCase().includes(q) ||
      k.id.toLowerCase().includes(q) ||
      (k.branch || "").toLowerCase().includes(q) ||
      k.agents.some((a) => s.agents[a].name.toLowerCase().includes(q));
    if (!hit) return false;
  }
  return true;
}

export function totalSpend(s: AppData): number {
  return s.cards.reduce((sum, k) => sum + (k.cost || 0), 0) + 8.4;
}

export function anyFilter(s: AppData): boolean {
  const f = s.filters;
  return f.agent !== "all" || f.prio !== "all" || f.type !== "all" || !!f.q;
}

/* ---------- activity log ---------- */
export function logActivity(
  s: AppData,
  k: Card,
  tag: string,
  text: string,
  meta = "",
): void {
  s.activity.unshift({ id: k.id, agent: k.agents[0], tag, text, meta, time: nowTime() });
  if (s.activity.length > 120) s.activity.pop();
}

/* ---------- log-line templates ---------- */
const LOG_TPL: Record<string, [string, string][]> = {
  infra: [["ac", "terraform plan: %d to add"], ["ok", "apply complete"], ["", "provisioning IAM roles"], ["ac", "kubectl rollout status"], ["ok", "cluster healthy"]],
  fix: [["ac", "reproduce in test harness"], ["wr", "null deref at line %d"], ["ok", "patch applied"], ["ac", "running regression suite"]],
  feat: [["ac", "scaffold component"], ["", "wiring state + props"], ["ok", "unit tests green"], ["ac", "tsc --noEmit"]],
  research: [["ac", "collecting benchmarks"], ["", "p99 latency sampled"], ["ok", "analysis complete"], ["ac", "drafting recommendation"]],
  sec: [["ac", "scanning IAM policies"], ["wr", "over-broad permission found"], ["", "checking network rules"], ["ok", "no critical findings"]],
  chore: [["ac", "rotating credentials"], ["ok", "secrets re-sealed"], ["", "updating manifests"]],
};

function nextLogTime(k: Card): string {
  const last = k.logs && k.logs.length ? k.logs[k.logs.length - 1].t : "00:00:00";
  let [h, m, sec] = last.split(":").map(Number);
  sec += 18 + rnd(40);
  if (sec >= 60) {
    m += Math.floor(sec / 60);
    sec %= 60;
  }
  if (m >= 60) {
    h += Math.floor(m / 60);
    m %= 60;
  }
  return [h, m, sec].map(pad2).join(":");
}

function pushLog(k: Card): void {
  const tpl = LOG_TPL[k.type] || LOG_TPL.feat;
  const pick = tpl[rnd(tpl.length)];
  const x = pick[1].replace("%d", String(10 + rnd(200)));
  k.logs = k.logs || [];
  k.logs.push({ t: nextLogTime(k), k: pick[0] as Card["logs"][number]["k"], x });
  if (k.logs.length > 14) k.logs.shift();
}

/* ---------- board moves ---------- */
export function moveCard(s: AppData, id: string, colId: string, user = false): void {
  const k = s.cards.find((x) => x.id === id);
  if (!k || k.col === colId) return;
  const to = stageOf(s, colId);
  if (!to) return;
  k.col = colId;
  if (colId === "done") {
    k.status = "done";
    k.progress = 100;
    k.eta = "—";
    if (k.hitl) delete k.hitl;
  } else if (to.gate === "human") {
    if ((k.progress ?? 0) >= 100 && !k.hitl) k.status = "awaiting";
  } else if (colId === "backlog") {
    k.status = "queued";
  } else if (k.status === "done") {
    k.status = "running";
  }
  if (user) logActivity(s, k, "move", `moved to <b>${to.name}</b>`);
  if (user) s.flashId = id;
}

/* ---------- human-in-the-loop resolution ---------- */
export function resolveHitl(s: AppData, k: Card, optIdx: number, reject = false): void {
  const choice = k.hitl ? k.hitl.opts[optIdx] : "approved";
  delete k.hitl;
  if (reject) {
    k.status = "running";
    k.progress = Math.max(40, (k.progress || 60) - 20);
    logActivity(s, k, "hitl", "decision rejected — sent back for rework");
    moveCard(s, k.id, "inprogress");
  } else {
    logActivity(s, k, "hitl", `approved — <b>${choice}</b>`);
    const i = s.workflow.findIndex((st) => st.id === k.col);
    if (k.col === "review") {
      moveCard(s, k.id, "done");
    } else if (i < s.workflow.length - 1) {
      k.status = "running";
      moveCard(s, k.id, s.workflow[i + 1].id);
    } else {
      k.status = "running";
    }
  }
}

export function panelAction(s: AppData, k: Card, act: string): string | null {
  if (act === "approve") {
    resolveHitl(s, k, 0);
  } else if (act === "reject") {
    resolveHitl(s, k, k.hitl ? k.hitl.opts.length - 1 : 1, true);
    return "Sent back for rework";
  } else if (act === "advance") {
    const i = s.workflow.findIndex((st) => st.id === k.col);
    if (i < s.workflow.length - 1) moveCard(s, k.id, s.workflow[i + 1].id, true);
  } else if (act === "reopen") {
    moveCard(s, k.id, "inprogress", true);
    k.status = "running";
    k.progress = 60;
  } else if (act === "reassign") {
    return "Reassignment routed to orchestrator";
  } else if (act === "view") {
    return "Opening full task view…";
  }
  return null;
}

/* ---------- live simulation tick ---------- */
export function tick(s: AppData): void {
  if (!s.simRunning || s.dragging) return;
  s.tickN += 1;

  for (const k of s.cards) {
    if (k.status !== "running") continue;
    const inc = 4 + rnd(9);
    k.progress = Math.min(100, (k.progress || 0) + inc);
    k.cost = Math.round(((k.cost || 0) + (0.004 + Math.random() * 0.02)) * 100) / 100;
    k.tokens = (k.tokens || 0) + 200 + rnd(600);
    k.when = "now";
    if (Math.random() < 0.7) pushLog(k);

    if ((k.progress ?? 0) >= 100) {
      const stage = stageOf(s, k.col)!;
      const idx = s.workflow.findIndex((st) => st.id === k.col);
      if (stage.gate === "human") {
        k.status = "awaiting";
        if (!k.hitl)
          k.hitl = {
            q: `Approve ${k.type === "sec" ? "security findings" : "changes"} for #${k.id}?`,
            opts: ["Human approval", "Auto-validate"],
          };
        logActivity(s, k, "hitl", "completed work — awaiting human approval");
      } else if (idx < s.workflow.length - 1) {
        const next = s.workflow[idx + 1];
        if (k.tests == null && (next.id === "testing" || k.type !== "research")) k.tests = "45/45 pass";
        k.col = next.id;
        k.progress = next.id === "done" ? 100 : 8;
        k.logs = k.logs.slice(-2);
        if (next.id === "done") {
          k.status = "done";
          k.eta = "—";
          if (k.hitl) delete k.hitl;
          logActivity(s, k, "done", "merged & deployed", `${k.branch || ""} · $${(k.cost || 0).toFixed(2)}`);
        } else {
          logActivity(s, k, "move", `advanced to <b>${next.name}</b>`);
          if (next.rules.autoAssign === "review" && !k.agents.includes("review")) k.agents.push("review");
        }
      } else {
        k.status = "done";
      }
    } else if (Math.random() < 0.25) {
      logActivity(
        s,
        k,
        k.type === "infra" ? "commit" : "run",
        k.type === "infra" ? `pushed to <b>${k.branch || "branch"}</b>` : `progress <b>${k.progress}%</b>`,
      );
    }
  }

  /* orchestrator picks up queued work if capacity allows */
  if (s.tickN % 2 === 0) {
    const ip = stageOf(s, "inprogress");
    const ipCount = s.cards.filter((k) => k.col === "inprogress").length;
    const cap = ip && ip.wip ? ip.wip : 5;
    if (ipCount < cap) {
      const cand = s.cards.find((k) => k.col === "todo" && k.status === "queued");
      if (cand) {
        cand.col = "inprogress";
        cand.status = "running";
        cand.progress = 6;
        cand.branch = cand.branch || `${cand.type}/${cand.id.toLowerCase()}`;
        cand.add = cand.add || 0;
        cand.del = cand.del || 0;
        cand.cost = cand.cost || 0;
        cand.tokens = cand.tokens || 0;
        cand.eta = "~" + (6 + rnd(16)) + "m";
        cand.logs = [];
        pushLog(cand);
        logActivity(s, cand, "run", `picked up by <b>${s.agents[cand.agents[0]].name}</b>`);
      } else {
        const b = s.cards.find((k) => k.col === "backlog" && k.status === "queued");
        if (b && Math.random() < 0.5) {
          b.col = "todo";
          logActivity(s, b, "move", "pulled into <b>To Do</b>");
        }
      }
    }
  }
}

/* ---------- workflow-builder presets ---------- */
const PRESET_SETS: Record<string, [string, string, string][]> = {
  dev: [["backlog", "Backlog", "#586675"], ["todo", "To Do", "#2898da"], ["build", "Build", "#4db0ef"], ["test", "Test", "#a78bfa"], ["review", "Review", "#f5b042"], ["done", "Done", "#34d399"]],
  research: [["intake", "Intake", "#586675"], ["analyse", "Analyse", "#2898da"], ["synth", "Synthesise", "#a78bfa"], ["report", "Report", "#34d399"]],
  support: [["inbox", "Inbox", "#586675"], ["triage", "Triage", "#f5b042"], ["fix", "Fix", "#2898da"], ["verify", "Verify", "#a78bfa"], ["closed", "Closed", "#34d399"]],
};

export function applyWorkflowPreset(s: AppData, p: string): void {
  const def = PRESET_SETS[p];
  if (!def) return;
  s.workflow = def.map((d, i) => ({
    id: d[0],
    name: d[1],
    dot: d[2],
    wip: 0,
    gate: (i === def.length - 2 ? "human" : null) as StageGate,
    rules: { autoAssign: i === 1 ? ("auto" as const) : null, autoValidate: false },
  }));
  s.cards.forEach((k) => {
    if (!s.workflow.some((st) => st.id === k.col)) k.col = s.workflow[0].id;
  });
}

/* ---------- seed initial activity ---------- */
export function seedActivity(s: AppData): void {
  if (s.activity.length) return;
  s.activity.push(
    { id: "AI-338", agent: "devops", tag: "done", text: "merged & deployed", meta: "infra/cicd · $0.27", time: "08:41:12" },
    { id: "AI-339", agent: "obs", tag: "done", text: "12 monitors live — merged", meta: "feat/alerts", time: "08:39:55" },
    { id: "AI-350", agent: "sec", tag: "hitl", text: "flagged 2 IAM findings — awaiting approval", meta: "", time: "09:02:18" },
    { id: "AI-358", agent: "frontend", tag: "commit", text: "pushed to <b>feat/stripe-tests</b>", meta: "+210 −18", time: "09:06:40" },
    { id: "AI-345", agent: "research", tag: "run", text: "benchmark complete — redis 2.1ms vs memorydb 3.4ms", meta: "", time: "09:11:03" },
    { id: "AI-348", agent: "devops", tag: "run", text: "applied 3 managed node groups", meta: "infra/eks-cluster", time: "09:18:22" },
    { id: "AI-351", agent: "review", tag: "hitl", text: "review complete — 3 suggestions, awaiting merge", meta: "", time: "09:24:50" },
    { id: "AI-355", agent: "frontend", tag: "hitl", text: "blocked on HEIC conversion decision", meta: "", time: "09:30:11" },
  );
}
