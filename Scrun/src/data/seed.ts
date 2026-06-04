/* ============================================================
   SCRUN — Seed data (single source of truth at boot)
   Factory functions return fresh deep copies so the board can
   be reconfigured / reset cleanly.
   ============================================================ */
import type { Agent, AgentKey, AgentStat, Card, Stage } from "../types";

/* per-agent base identity */
const AGENTS_BASE: Record<AgentKey, Omit<Agent, "role" | "instructions" | "knowledge" | "memory" | "autonomy" | "spendCap" | "maxParallel">> = {
  devops: { name: "DevOps Agent", color: "#2898da", ini: "DO", model: "claude-opus-4.7", provider: "Anthropic", caps: ["terraform", "kubernetes", "ci/cd", "aws"] },
  sec: { name: "Security Agent", color: "#f4685f", ini: "SE", model: "claude-opus-4.7", provider: "Anthropic", caps: ["iam", "secrets", "sast", "audit"] },
  research: { name: "Research Agent", color: "#2dd4bf", ini: "RS", model: "gpt-4-turbo", provider: "OpenAI", caps: ["analysis", "benchmark", "spike", "docs"] },
  dba: { name: "DBA Agent", color: "#a78bfa", ini: "DB", model: "claude-sonnet-4.6", provider: "Anthropic", caps: ["postgres", "redis", "backup", "migrate"] },
  obs: { name: "Observability", color: "#34d399", ini: "OB", model: "llama-3.3-70b", provider: "Meta", caps: ["datadog", "slo", "alerts", "traces"] },
  review: { name: "Code Review Agent", color: "#f5b042", ini: "CR", model: "claude-opus-4.7", provider: "Anthropic", caps: ["review", "lint", "types", "style"] },
  frontend: { name: "Frontend Agent", color: "#60a5fa", ini: "FE", model: "gpt-4-turbo", provider: "OpenAI", caps: ["react", "ui", "a11y", "css"] },
};

/* per-agent configurable profile: role, personal context, capabilities, memory */
const AGENT_CFG: Record<AgentKey, Pick<Agent, "role" | "instructions" | "knowledge" | "memory" | "autonomy" | "spendCap" | "maxParallel">> = {
  devops: {
    role: "Infrastructure & Platform Engineer",
    instructions: "You are the DevOps Agent. Own infrastructure-as-code, clusters and delivery pipelines. Always plan before apply, prefer reversible changes, and never touch production IAM without a human gate. Write Terraform that is modular and documented.",
    knowledge: [["Terraform module registry", "42 modules"], ["Platform runbooks", "18 docs"], ["Cloud architecture ADRs", "31 records"]],
    memory: { mode: "persistent", scope: "project", contextK: 128, retention: "30d" },
    autonomy: "auto", spendCap: 25, maxParallel: 3,
  },
  sec: {
    role: "Security & Compliance Analyst",
    instructions: "You are the Security Agent. Audit changes for IAM, secrets and network exposure. Escalate any over-broad permission or compliance risk to a human reviewer. Never auto-approve changes that widen the attack surface.",
    knowledge: [["Security policies", "SOC2 + CIS"], ["Threat model library", "12 models"], ["Secrets inventory", "read-only"]],
    memory: { mode: "persistent", scope: "global", contextK: 64, retention: "90d" },
    autonomy: "supervised", spendCap: 15, maxParallel: 2,
  },
  research: {
    role: "Technical Research Analyst",
    instructions: "You are the Research Agent. Run spikes and benchmarks, compare options objectively, and produce a clear recommendation with trade-offs. Cite measured numbers, not vibes. Time-box exploration.",
    knowledge: [["Benchmark archive", "210 runs"], ["Vendor docs index", "live"], ["Past spike reports", "37 docs"]],
    memory: { mode: "session", scope: "task", contextK: 200, retention: "7d" },
    autonomy: "auto", spendCap: 12, maxParallel: 2,
  },
  dba: {
    role: "Database Reliability Engineer",
    instructions: "You are the DBA Agent. Plan migrations with zero-downtime strategies and tested rollbacks. Protect data integrity above all; require approval before destructive operations on primary stores.",
    knowledge: [["Schema catalog", "live"], ["Migration playbooks", "24 docs"], ["Backup policy", "PITR + snapshots"]],
    memory: { mode: "persistent", scope: "project", contextK: 96, retention: "90d" },
    autonomy: "supervised", spendCap: 18, maxParallel: 2,
  },
  obs: {
    role: "Observability Engineer",
    instructions: "You are the Observability Agent. Instrument services, define SLOs and tune alerts to minimise noise. Prefer actionable alerts tied to user impact. Keep dashboards current.",
    knowledge: [["SLO definitions", "48 services"], ["Alert runbooks", "61 docs"], ["Dashboard library", "live"]],
    memory: { mode: "persistent", scope: "project", contextK: 64, retention: "30d" },
    autonomy: "auto", spendCap: 10, maxParallel: 3,
  },
  review: {
    role: "Senior Code Reviewer",
    instructions: "You are the Code Review Agent. Review for correctness, security, types and style. Be specific and constructive. Block merges that reduce test coverage or introduce known anti-patterns.",
    knowledge: [["Style guide", "TS + Go"], ["Past review threads", "live"], ["Architecture decisions", "31 records"]],
    memory: { mode: "persistent", scope: "project", contextK: 160, retention: "30d" },
    autonomy: "supervised", spendCap: 8, maxParallel: 4,
  },
  frontend: {
    role: "Frontend & UX Engineer",
    instructions: "You are the Frontend Agent. Build accessible, responsive UI that matches the design system. Verify keyboard and screen-reader paths. Never ship untested interactive states.",
    knowledge: [["Design system tokens", "live"], ["Component library", "94 parts"], ["A11y checklist", "WCAG 2.2"]],
    memory: { mode: "session", scope: "project", contextK: 128, retention: "7d" },
    autonomy: "auto", spendCap: 14, maxParallel: 2,
  },
};

export function createAgents(): Record<AgentKey, Agent> {
  const out: Record<AgentKey, Agent> = {};
  for (const k of Object.keys(AGENTS_BASE)) {
    out[k] = { ...AGENTS_BASE[k], ...AGENT_CFG[k] } as Agent;
    // deep-copy mutable nested bits
    out[k].caps = [...out[k].caps];
    out[k].knowledge = out[k].knowledge.map((p) => [p[0], p[1]]);
    out[k].memory = { ...out[k].memory };
  }
  return out;
}

export const AGENT_STATS: Record<AgentKey, AgentStat> = {
  devops: { tasks: 128, success: 97, spend: 42.1 },
  sec: { tasks: 64, success: 99, spend: 18.4 },
  research: { tasks: 51, success: 94, spend: 12.8 },
  dba: { tasks: 73, success: 96, spend: 21.3 },
  obs: { tasks: 39, success: 99, spend: 7.9 },
  review: { tasks: 142, success: 98, spend: 9.6 },
  frontend: { tasks: 88, success: 93, spend: 15.2 },
};

/* Workflow — fully configurable stages with WIP limits + entry gates + rules */
export function createWorkflow(): Stage[] {
  return [
    { id: "backlog", name: "Backlog", dot: "#586675", wip: 0, gate: null, rules: { autoAssign: null, autoValidate: false } },
    { id: "todo", name: "To Do", dot: "#2898da", wip: 0, gate: null, rules: { autoAssign: "auto", autoValidate: false } },
    { id: "inprogress", name: "In Progress", dot: "#4db0ef", wip: 4, gate: null, rules: { autoAssign: "keep", autoValidate: false } },
    { id: "testing", name: "Testing", dot: "#a78bfa", wip: 3, gate: "auto", rules: { autoAssign: "review", autoValidate: true } },
    { id: "review", name: "Review", dot: "#f5b042", wip: 0, gate: "human", rules: { autoAssign: null, autoValidate: false } },
    { id: "done", name: "Done", dot: "#34d399", wip: 0, gate: null, rules: { autoAssign: null, autoValidate: false } },
  ];
}

export function createCards(): Card[] {
  return [
    { id: "AI-341", col: "backlog", type: "infra", prio: "H", title: "Build Terraform module for EKS cluster", agents: ["devops"], status: "queued", labels: ["infrastructure", "eks", "terraform", "aws"], ac: ["Module accepts cluster name, version and node config as inputs", "Outputs kubeconfig and cluster endpoint", "Documented with a usage example in the README", "Passes tflint and tfsec with no high findings"], desc: "Provision a reusable Terraform module for an EKS cluster with managed node groups, autoscaling and required add-ons.", when: "2h", logs: [] },
    { id: "AI-352", col: "backlog", type: "chore", prio: "L", title: "Rotate service-account credentials across agents", agents: ["sec"], status: "queued", labels: ["secrets", "rotation"], desc: "Quarterly rotation of service-account credentials across all on-prem agents.", when: "5h", logs: [] },
    { id: "AI-360", col: "backlog", type: "feat", prio: "M", title: "Dark-mode pass on billing dashboard", agents: ["frontend"], status: "queued", labels: ["ui", "billing", "theme"], desc: "Audit billing dashboard for dark-mode contrast and token coverage.", when: "3h", logs: [] },

    { id: "AI-344", col: "todo", type: "research", prio: "H", title: "Research best RDS backup strategies", agents: ["research", "dba"], status: "queued", labels: ["rds", "backup", "research"], desc: "Compare point-in-time recovery vs snapshot cadence for the primary Postgres estate and recommend an approach.", when: "1h", logs: [] },
    { id: "AI-342", col: "todo", type: "sec", prio: "M", title: "Migrate SSO to Okta and AWS IAM Identity Center", agents: ["sec"], status: "queued", labels: ["sso", "okta", "iam"], desc: "Plan and stage the SSO migration with zero downtime and a rollback path.", when: "4h", logs: [] },

    { id: "AI-348", col: "inprogress", type: "infra", prio: "H", title: "Implement EKS infrastructure", agents: ["devops", "sec"], status: "running", progress: 65, branch: "infra/eks-cluster", add: 142, del: 31, cost: 0.42, tokens: 18400, duration: "23m", eta: "8m", conf: 92, labels: ["infrastructure", "eks", "terraform", "aws"], ac: ["Cluster provisions cleanly from a single terraform apply", "Managed node groups autoscale 2→10 nodes", "IRSA / OIDC provider configured for workload identity", "All add-ons (CNI, CoreDNS, autoscaler) pass health checks"], desc: "Provision EKS cluster with managed node groups, autoscaling and required add-ons. Wiring IRSA roles and the cluster autoscaler add-on.", when: "3m", logs: [{ t: "00:21:04", k: "ac", x: "plan infra/eks/node-group.tf" }, { t: "00:22:10", k: "ok", x: "apply 3 managed node groups" }, { t: "00:22:48", k: "", x: "configuring IRSA + OIDC provider" }] },
    { id: "AI-349", col: "inprogress", type: "infra", prio: "L", title: "Create Terraform modules for networking", agents: ["devops"], status: "running", progress: 40, branch: "infra/vpc-modules", add: 88, del: 12, cost: 0.21, tokens: 9100, duration: "11m", eta: "15m", conf: 84, labels: ["networking", "vpc"], desc: "Author reusable VPC / subnet / NAT modules for the platform landing zone.", when: "5m", logs: [{ t: "00:09:31", k: "ac", x: "write infra/network/vpc.tf" }, { t: "00:10:22", k: "", x: "generating subnet matrix (3 AZ)" }] },
    { id: "AI-355", col: "inprogress", type: "fix", prio: "M", title: "Profile picture upload broken on iOS Safari", agents: ["frontend"], status: "awaiting", progress: 55, branch: "fix/ios-upload", add: 24, del: 6, cost: 0.13, tokens: 6200, duration: "9m", eta: "—", conf: 71, labels: ["bug", "ios", "upload"], desc: "HEIC images from iOS fail to upload. Agent needs a decision on the conversion strategy before continuing.", when: "16m", logs: [{ t: "00:08:01", k: "wr", x: "HEIC decode unsupported in canvas" }], hitl: { q: "How should iOS HEIC files be handled?", opts: ["Convert in browser", "Convert on server", "Reject HEIC"] } },

    { id: "AI-345", col: "testing", type: "research", prio: "M", title: "Evaluate Redis vs MemoryDB for cache layer", agents: ["research"], status: "running", progress: 78, branch: "spike/cache-eval", cost: 0.18, tokens: 7700, duration: "14m", eta: "4m", conf: 88, labels: ["cache", "redis", "spike"], desc: "Benchmark latency and cost for Redis vs MemoryDB under projected load.", when: "15m", logs: [{ t: "00:12:40", k: "ac", x: "run bench: p99 latency" }, { t: "00:13:55", k: "ok", x: "redis 2.1ms · memorydb 3.4ms" }] },
    { id: "AI-358", col: "testing", type: "feat", prio: "H", title: "Stripe billing integration test suite", agents: ["frontend", "review"], status: "running", progress: 91, branch: "feat/stripe-tests", add: 210, del: 18, cost: 0.31, tokens: 12000, duration: "19m", eta: "2m", conf: 95, labels: ["stripe", "billing", "tests"], desc: "Integration tests for the Stripe billing flow. Awaiting final lint + type pass before review.", when: "6m", tests: "45/45 pass", logs: [{ t: "00:18:02", k: "ok", x: "45/45 integration tests pass" }, { t: "00:18:40", k: "ac", x: "running tsc --noEmit" }] },

    { id: "AI-350", col: "review", type: "sec", prio: "H", title: "Review Terraform for security issues", agents: ["sec"], status: "awaiting", progress: 100, branch: "infra/eks-cluster", add: 142, del: 31, cost: 0.09, tokens: 4100, duration: "6m", eta: "—", conf: 90, labels: ["security", "review", "terraform"], desc: "Security agent has flagged 2 IAM findings on the EKS module and is requesting human approval before merge.", when: "20m", logs: [{ t: "00:05:12", k: "wr", x: "2 IAM findings: over-broad policy" }], hitl: { q: "Approve IAM policy changes for EKS module?", opts: ["Human approval", "Auto-validate"] } },
    { id: "AI-351", col: "review", type: "feat", prio: "M", title: "CSV export from admin dashboard", agents: ["review"], status: "awaiting", progress: 100, branch: "feat/csv-export", add: 41, del: 5, cost: 0.12, tokens: 5000, duration: "8m", eta: "—", conf: 86, labels: ["export", "admin", "csv"], desc: "Code review agent completed review with 3 suggestions. Ready for human merge decision.", when: "35m", logs: [{ t: "00:07:33", k: "ok", x: "review complete · 3 suggestions" }], hitl: { q: "Merge with reviewer suggestions applied?", opts: ["Human approval", "Auto-validate"] } },

    { id: "AI-338", col: "done", type: "infra", prio: "M", title: "Setup CI/CD pipeline", agents: ["devops"], status: "done", progress: 100, branch: "infra/cicd", add: 96, del: 9, cost: 0.27, tokens: 11200, duration: "21m", eta: "—", conf: 97, labels: ["cicd", "pipeline"], desc: "GitHub Actions pipeline with build, test, scan and deploy stages — merged to main.", when: "1h", logs: [{ t: "00:20:40", k: "ok", x: "merged → main · deploy ok" }] },
    { id: "AI-339", col: "done", type: "feat", prio: "L", title: "Configure monitoring alerts", agents: ["obs"], status: "done", progress: 100, branch: "feat/alerts", add: 54, del: 3, cost: 0.11, tokens: 4800, duration: "7m", eta: "—", conf: 99, labels: ["monitoring", "alerts", "datadog"], desc: "Datadog monitors and SLO alerts configured and merged to main.", when: "2h", logs: [{ t: "00:06:55", k: "ok", x: "12 monitors live · merged" }] },
  ];
}
