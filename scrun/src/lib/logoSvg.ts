/* ============================================================
   SCRUN — Spiral logo mark (procedural SVG)
   ============================================================ */
const C = 50,
  TURNS = 1.35,
  STEPS = 54,
  R_MAX = 26,
  R_MIN = 3,
  SW = 6.4,
  HEAD_R = 8;

function points(): string {
  const out: string[] = [];
  for (let i = 0; i <= STEPS; i++) {
    const t = i / STEPS;
    const r = R_MIN + t * (R_MAX - R_MIN);
    const d = ((-90 + t * 360 * TURNS) * Math.PI) / 180;
    out.push((C + r * Math.cos(d)).toFixed(2) + " " + (C + r * Math.sin(d)).toFixed(2));
  }
  return out.join(" ");
}

function head(): [number, number] {
  const d = ((-90 + 360 * TURNS) * Math.PI) / 180;
  return [C + R_MAX * Math.cos(d), C + R_MAX * Math.sin(d)];
}

/** Inner spiral geometry as an SVG string (used for favicon too). */
export function logoInner(ink: string): string {
  const [hx, hy] = head();
  return (
    `<polyline points="${points()}" fill="none" stroke="${ink}" stroke-width="${SW}" stroke-linecap="round" stroke-linejoin="round"/>` +
    `<circle cx="${hx.toFixed(2)}" cy="${hy.toFixed(2)}" r="${HEAD_R}" fill="${ink}"/>`
  );
}
