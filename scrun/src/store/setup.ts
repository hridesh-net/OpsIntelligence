/* ============================================================
   SCRUN — Setup wizard data + helpers
   ============================================================ */
import type { StageGate } from "../types";
import type { AppData } from "./state";
import { setPrefix } from "./logic";

export interface SuStage {
  id: string;
  name: string;
  dot: string;
  gate: StageGate;
}

export interface SuWorkflowDef {
  name: string;
  desc: string;
  stages: [string, string, string, StageGate?][];
}

export const SU_WORKFLOWS: Record<string, SuWorkflowDef> = {
  dev: {
    name: "Software delivery",
    desc: "Build, test and ship features with review gates.",
    stages: [["backlog", "Backlog", "#586675"], ["todo", "To Do", "#2898da"], ["inprogress", "In Progress", "#4db0ef"], ["testing", "Testing", "#a78bfa", "auto"], ["review", "Review", "#f5b042", "human"], ["done", "Done", "#34d399"]],
  },
  research: {
    name: "Research spike",
    desc: "Investigate, benchmark and synthesise a recommendation.",
    stages: [["intake", "Intake", "#586675"], ["analyse", "Analyse", "#2898da"], ["synth", "Synthesise", "#a78bfa"], ["review", "Report Review", "#f5b042", "human"], ["done", "Done", "#34d399"]],
  },
  support: {
    name: "Triage & resolve",
    desc: "Intake issues, triage, fix and verify before close.",
    stages: [["inbox", "Inbox", "#586675"], ["triage", "Triage", "#f5b042"], ["fix", "Fix", "#2898da"], ["verify", "Verify", "#a78bfa", "auto"], ["closed", "Closed", "#34d399"]],
  },
  ops: {
    name: "Ops & incident",
    desc: "Detect, mitigate and post-mortem operational work.",
    stages: [["detected", "Detected", "#f4685f"], ["mitigating", "Mitigating", "#f5b042"], ["monitoring", "Monitoring", "#4db0ef"], ["postmortem", "Post-mortem", "#a78bfa", "human"], ["resolved", "Resolved", "#34d399"]],
  },
};

export const SU_COLORS = ["#2898da", "#2dd4bf", "#a78bfa", "#f5b042", "#34d399", "#f4685f", "#60a5fa"];

/** Resolve the wizard's working stage list (custom override or preset default). */
export function suStages(setup: AppData["setup"]): SuStage[] {
  const raw = setup.stages || SU_WORKFLOWS[setup.preset].stages;
  return raw.map((s) => ({ id: s[0], name: s[1], dot: s[2], gate: (s[3] || null) as StageGate }));
}

/** Apply the wizard's choices to the live board model. */
export function applyBoardConfig(s: AppData): void {
  const stages = suStages(s.setup);
  const keepFirst = stages[0].id;
  const keepDone = stages[stages.length - 1].id;
  s.workflow = stages.map((st, i) => ({
    id: st.id,
    name: st.name,
    dot: st.dot,
    wip: 0,
    gate: st.gate,
    rules: {
      autoAssign: i === 1 ? "auto" : st.gate === "auto" ? "review" : null,
      autoValidate: st.gate === "auto",
    },
  }));
  s.cards.forEach((k) => {
    if (!s.workflow.some((st) => st.id === k.col)) k.col = keepFirst;
    if (k.status === "done") k.col = keepDone;
  });
  setPrefix(s, s.setup.key);
  s.boardAgents = [...s.setup.agents];
  s.boardName = s.setup.name;
  s.boardKey = s.setup.key;
  s.boardColor = s.setup.color;
  s.boardDesc = s.setup.desc;
}
