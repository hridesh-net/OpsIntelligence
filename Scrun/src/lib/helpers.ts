/* ============================================================
   SCRUN — Small pure helpers
   ============================================================ */
export const pad2 = (n: number): string => String(n).padStart(2, "0");

export function nowTime(): string {
  const d = new Date();
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

export const fmtCost = (n: number): string => "$" + n.toFixed(2);

export const STATUS_LABEL: Record<string, string> = {
  running: "Running",
  awaiting: "Awaiting",
  queued: "Queued",
  blocked: "Blocked",
  done: "Done",
};

export const TYPE_LABEL: Record<string, string> = {
  feat: "feat",
  fix: "fix",
  chore: "chore",
  infra: "infra",
  sec: "sec",
  research: "rsch",
};

export const PRIO_NAME: Record<string, string> = { H: "High", M: "Medium", L: "Low" };

export function autoKey(name: string): string {
  return (
    name
      .split(/\s+/)
      .map((w) => w[0])
      .join("")
      .replace(/[^a-z]/gi, "") || "BD"
  )
    .slice(0, 4)
    .toUpperCase();
}
