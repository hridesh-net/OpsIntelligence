import { useEffect, useMemo, useRef, useState } from "react";
import type { CallGraph, CallNode } from "@/api/repos";

/**
 * Dependency-free interactive call-graph viewer. Runs a small force simulation
 * (repulsion + edge springs + centering) for a few seconds to lay nodes out,
 * then lets you pan (drag background), zoom (wheel) and drag nodes. Click a node
 * to highlight its neighbours and inspect file/line. Large graphs are capped to
 * the highest-degree nodes for legibility.
 */

const MAX_NODES = 180;

const KIND_COLOR: Record<string, string> = {
  function: "#e4572e",
  method: "#7c5cf0",
  class: "#0fa968",
  module: "#c9820a",
  file: "#2898da",
};
function colorFor(kind: string): string {
  return KIND_COLOR[kind] ?? "var(--fg-muted)";
}

interface Sim extends CallNode {
  x: number; y: number; vx: number; vy: number; deg: number; r: number;
}

export function CodeGraph({ graph }: { graph: CallGraph }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [, force] = useState(0);
  const [view, setView] = useState({ x: 0, y: 0, k: 1 });
  const [selected, setSelected] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  // ── build capped node/edge set + initial layout ──────────────────────────
  const { nodes, edges, neighbors, truncated, total } = useMemo(() => {
    const deg: Record<string, number> = {};
    for (const e of graph.edges) { deg[e.from] = (deg[e.from] ?? 0) + 1; deg[e.to] = (deg[e.to] ?? 0) + 1; }
    const ranked = graph.nodes.slice().sort((a, b) => (deg[b.id] ?? 0) - (deg[a.id] ?? 0));
    const kept = ranked.slice(0, MAX_NODES);
    const keepIds = new Set(kept.map((n) => n.id));
    const W = 900, H = 600;
    const sims: Sim[] = kept.map((n, i) => {
      const ang = (i / kept.length) * Math.PI * 2;
      const d = (deg[n.id] ?? 0);
      return {
        ...n, deg: d, r: Math.max(4, Math.min(13, 4 + Math.sqrt(d) * 2.2)),
        x: W / 2 + Math.cos(ang) * 240 + (i % 7) * 6,
        y: H / 2 + Math.sin(ang) * 240 + (i % 5) * 6,
        vx: 0, vy: 0,
      };
    });
    const es = graph.edges.filter((e) => keepIds.has(e.from) && keepIds.has(e.to));
    const nb: Record<string, Set<string>> = {};
    for (const e of es) {
      (nb[e.from] ??= new Set()).add(e.to);
      (nb[e.to] ??= new Set()).add(e.from);
    }
    return { nodes: sims, edges: es, neighbors: nb, truncated: graph.nodes.length > kept.length, total: graph.nodes.length };
  }, [graph]);

  const nodesRef = useRef<Sim[]>(nodes);
  nodesRef.current = nodes;
  const byId = useMemo(() => { const m: Record<string, Sim> = {}; for (const n of nodes) m[n.id] = n; return m; }, [nodes]);

  // ── force simulation (rAF, auto-stops) ────────────────────────────────────
  useEffect(() => {
    let raf = 0, tick = 0;
    const W = 900, H = 600;
    const step = () => {
      const ns = nodesRef.current;
      const k = 0.9 - Math.min(0.85, tick / 500); // cooling
      // repulsion
      for (let i = 0; i < ns.length; i++) {
        const a = ns[i];
        for (let j = i + 1; j < ns.length; j++) {
          const b = ns[j];
          let dx = a.x - b.x, dy = a.y - b.y;
          let d2 = dx * dx + dy * dy;
          if (d2 < 0.01) { dx = Math.random(); dy = Math.random(); d2 = 1; }
          const f = 1400 / d2;
          const d = Math.sqrt(d2);
          const fx = (dx / d) * f, fy = (dy / d) * f;
          a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;
        }
      }
      // springs
      for (const e of edges) {
        const a = byId[e.from], b = byId[e.to];
        if (!a || !b) continue;
        const dx = b.x - a.x, dy = b.y - a.y;
        const d = Math.sqrt(dx * dx + dy * dy) || 1;
        const f = (d - 70) * 0.015;
        const fx = (dx / d) * f, fy = (dy / d) * f;
        a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;
      }
      // centering + integrate
      for (const n of ns) {
        if ((n as Sim & { fixed?: boolean }).fixed) { n.vx = 0; n.vy = 0; continue; }
        n.vx += (W / 2 - n.x) * 0.002;
        n.vy += (H / 2 - n.y) * 0.002;
        n.vx *= 0.82 * (0.5 + k); n.vy *= 0.82 * (0.5 + k);
        n.x += Math.max(-30, Math.min(30, n.vx));
        n.y += Math.max(-30, Math.min(30, n.vy));
      }
      tick++;
      force((v) => v + 1);
      if (tick < 480) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [edges, byId]);

  // ── pan / zoom ────────────────────────────────────────────────────────────
  const dragRef = useRef<{ mode: "pan" | "node"; id?: string; sx: number; sy: number; ox: number; oy: number } | null>(null);

  function onWheel(e: React.WheelEvent) {
    e.preventDefault();
    const delta = -e.deltaY * 0.0015;
    setView((v) => {
      const k = Math.max(0.25, Math.min(3, v.k * (1 + delta)));
      return { ...v, k };
    });
  }
  function toGraph(clientX: number, clientY: number) {
    const rect = wrapRef.current!.getBoundingClientRect();
    return { gx: (clientX - rect.left - view.x) / view.k, gy: (clientY - rect.top - view.y) / view.k };
  }
  function onDown(e: React.MouseEvent, id?: string) {
    e.stopPropagation();
    if (id) {
      const n = byId[id]; (n as Sim & { fixed?: boolean }).fixed = true;
      const { gx, gy } = toGraph(e.clientX, e.clientY);
      dragRef.current = { mode: "node", id, sx: gx, sy: gy, ox: n.x, oy: n.y };
      setSelected(id);
    } else {
      dragRef.current = { mode: "pan", sx: e.clientX, sy: e.clientY, ox: view.x, oy: view.y };
    }
  }
  function onMove(e: React.MouseEvent) {
    const d = dragRef.current; if (!d) return;
    if (d.mode === "pan") {
      setView((v) => ({ ...v, x: d.ox + (e.clientX - d.sx), y: d.oy + (e.clientY - d.sy) }));
    } else if (d.id) {
      const { gx, gy } = toGraph(e.clientX, e.clientY);
      const n = byId[d.id]; n.x = d.ox + (gx - d.sx); n.y = d.oy + (gy - d.sy); n.vx = 0; n.vy = 0;
      force((v) => v + 1);
    }
  }
  function onUp() {
    const d = dragRef.current;
    if (d?.mode === "node" && d.id) (byId[d.id] as Sim & { fixed?: boolean }).fixed = false;
    dragRef.current = null;
  }

  const q = query.trim().toLowerCase();
  const sel = selected ? byId[selected] : null;
  const hi = selected ? neighbors[selected] ?? new Set<string>() : null;

  return (
    <div className="cg">
      <div className="cg-bar">
        <input className="cg-search" placeholder="Find a function…" value={query} onChange={(e) => setQuery(e.target.value)} />
        <span className="cg-stat">{nodes.length} of {total} nodes · {edges.length} edges{truncated ? " · showing top by connections" : ""}</span>
        <div className="cg-legend">
          {Object.entries(KIND_COLOR).map(([k, c]) => (
            <span key={k} className="cg-leg"><span className="dot" style={{ background: c }} />{k}</span>
          ))}
        </div>
      </div>

      <div
        className="cg-canvas"
        ref={wrapRef}
        onWheel={onWheel}
        onMouseDown={(e) => onDown(e)}
        onMouseMove={onMove}
        onMouseUp={onUp}
        onMouseLeave={onUp}
      >
        <svg width="100%" height="100%">
          <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
            {edges.map((e, i) => {
              const a = byId[e.from], b = byId[e.to];
              if (!a || !b) return null;
              const active = selected && (e.from === selected || e.to === selected);
              return (
                <line
                  key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                  stroke={active ? "var(--accent)" : "var(--border-strong)"}
                  strokeWidth={active ? 1.6 : 0.7}
                  strokeOpacity={selected && !active ? 0.18 : 0.5}
                />
              );
            })}
            {nodes.map((n) => {
              const match = q && n.name.toLowerCase().includes(q);
              const dim = (selected && n.id !== selected && !hi?.has(n.id)) || (q && !match);
              const showLabel = match || n.id === selected || n.deg >= 6;
              return (
                <g key={n.id} transform={`translate(${n.x},${n.y})`} style={{ cursor: "pointer", opacity: dim ? 0.25 : 1 }}
                   onMouseDown={(e) => onDown(e, n.id)}>
                  <circle r={n.r} fill={colorFor(n.kind)} stroke={match ? "var(--fg)" : "#fff"} strokeWidth={match ? 2 : 1} />
                  {showLabel && (
                    <text x={n.r + 3} y={3.5} fontSize={10} fill="var(--fg-dim)" style={{ pointerEvents: "none", fontFamily: "var(--mono)" }}>
                      {n.name.length > 26 ? n.name.slice(0, 26) + "…" : n.name}
                    </text>
                  )}
                </g>
              );
            })}
          </g>
        </svg>

        {sel && (
          <div className="cg-info">
            <div className="cg-info-name"><span className="dot" style={{ background: colorFor(sel.kind) }} />{sel.name}</div>
            <div className="cg-info-meta">{sel.kind}{sel.package ? ` · ${sel.package}` : ""}</div>
            <div className="cg-info-file">{sel.file}{sel.line ? `:${sel.line}` : ""}</div>
            <div className="cg-info-deg">{neighbors[sel.id]?.size ?? 0} connections</div>
            <button className="btn ghost" onClick={() => setSelected(null)} style={{ marginTop: 8 }}>Clear</button>
          </div>
        )}
      </div>
    </div>
  );
}
