import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CallGraph, CallNode } from "@/api/repos";

/**
 * Dependency-free interactive call-graph viewer (Obsidian-style). A small force
 * simulation lays nodes out and the view auto-fits as it settles, so the graph
 * opens framed. Hover or click a node to highlight its neighbourhood; drag nodes,
 * scroll to zoom (towards the cursor), drag the background to pan, or use the
 * Fit / +/- controls. Large graphs are capped to the highest-degree nodes.
 */

const MAX_NODES = 200;

const KIND_COLOR: Record<string, string> = {
  function: "#e4572e",
  method: "#7c5cf0",
  class: "#0fa968",
  module: "#c9820a",
  file: "#2898da",
};
const colorFor = (kind: string) => KIND_COLOR[kind] ?? "var(--fg-muted)";
const clampK = (k: number) => Math.max(0.12, Math.min(3, k));

interface Sim extends CallNode { x: number; y: number; vx: number; vy: number; deg: number; r: number; fixed?: boolean }

export function CodeGraph({ graph }: { graph: CallGraph }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [, force] = useState(0);
  const [view, setView] = useState({ x: 0, y: 0, k: 1 });
  const [selected, setSelected] = useState<string | null>(null);
  const [hovered, setHovered] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const userMoved = useRef(false);

  const { nodes, edges, neighbors, truncated, total } = useMemo(() => {
    const deg: Record<string, number> = {};
    for (const e of graph.edges) { deg[e.from] = (deg[e.from] ?? 0) + 1; deg[e.to] = (deg[e.to] ?? 0) + 1; }
    const ranked = graph.nodes.slice().sort((a, b) => (deg[b.id] ?? 0) - (deg[a.id] ?? 0));
    const kept = ranked.slice(0, MAX_NODES);
    const keepIds = new Set(kept.map((n) => n.id));
    const sims: Sim[] = kept.map((n, i) => {
      const ang = (i / kept.length) * Math.PI * 2;
      const ring = 120 + (i % 9) * 26;
      return {
        ...n, deg: deg[n.id] ?? 0, r: Math.max(4, Math.min(15, 4 + Math.sqrt(deg[n.id] ?? 0) * 2.4)),
        x: 450 + Math.cos(ang) * ring, y: 300 + Math.sin(ang) * ring, vx: 0, vy: 0,
      };
    });
    const es = graph.edges.filter((e) => keepIds.has(e.from) && keepIds.has(e.to));
    const nb: Record<string, Set<string>> = {};
    for (const e of es) { (nb[e.from] ??= new Set()).add(e.to); (nb[e.to] ??= new Set()).add(e.from); }
    return { nodes: sims, edges: es, neighbors: nb, truncated: graph.nodes.length > kept.length, total: graph.nodes.length };
  }, [graph]);

  const nodesRef = useRef<Sim[]>(nodes);
  nodesRef.current = nodes;
  const byId = useMemo(() => { const m: Record<string, Sim> = {}; for (const n of nodes) m[n.id] = n; return m; }, [nodes]);

  const fitView = useCallback(() => {
    const ns = nodesRef.current, el = wrapRef.current;
    if (!ns.length || !el) return;
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of ns) { if (n.x < minX) minX = n.x; if (n.y < minY) minY = n.y; if (n.x > maxX) maxX = n.x; if (n.y > maxY) maxY = n.y; }
    const pad = 72, w = el.clientWidth, h = el.clientHeight;
    const gw = Math.max(1, maxX - minX), gh = Math.max(1, maxY - minY);
    const k = Math.max(0.12, Math.min(2.4, Math.min((w - pad * 2) / gw, (h - pad * 2) / gh)));
    setView({ k, x: w / 2 - ((minX + maxX) / 2) * k, y: h / 2 - ((minY + maxY) / 2) * k });
  }, []);

  // ── force simulation; auto-fits while settling unless the user takes over ──
  useEffect(() => {
    userMoved.current = false;
    let raf = 0, tick = 0;
    const LIMIT = 560;
    const step = () => {
      const ns = nodesRef.current;
      const cool = Math.max(0.15, 1 - tick / 420);
      for (let i = 0; i < ns.length; i++) {
        const a = ns[i];
        for (let j = i + 1; j < ns.length; j++) {
          const b = ns[j];
          let dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy;
          if (d2 < 0.01) { dx = Math.random(); dy = Math.random(); d2 = 1; }
          const d = Math.sqrt(d2), f = Math.min(40, 2800 / d2);
          a.vx += (dx / d) * f; a.vy += (dy / d) * f; b.vx -= (dx / d) * f; b.vy -= (dy / d) * f;
        }
      }
      for (const e of edges) {
        const a = byId[e.from], b = byId[e.to];
        if (!a || !b) continue;
        const dx = b.x - a.x, dy = b.y - a.y, d = Math.sqrt(dx * dx + dy * dy) || 1;
        const f = (d - 58) * 0.02;
        a.vx += (dx / d) * f; a.vy += (dy / d) * f; b.vx -= (dx / d) * f; b.vy -= (dy / d) * f;
      }
      for (const n of ns) {
        if (n.fixed) { n.vx = 0; n.vy = 0; continue; }
        n.vx += (450 - n.x) * 0.0012; n.vy += (300 - n.y) * 0.0012;
        n.vx *= 0.86 * (0.4 + cool * 0.6); n.vy *= 0.86 * (0.4 + cool * 0.6);
        n.x += Math.max(-26, Math.min(26, n.vx)); n.y += Math.max(-26, Math.min(26, n.vy));
      }
      tick++;
      if (!userMoved.current && tick % 18 === 0) fitView();
      force((v) => v + 1);
      if (tick < LIMIT) raf = requestAnimationFrame(step);
      else if (!userMoved.current) fitView();
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [edges, byId, fitView]);

  // ── interactions ──────────────────────────────────────────────────────────
  const dragRef = useRef<{ mode: "pan" | "node"; id?: string; sx: number; sy: number; ox: number; oy: number } | null>(null);
  const toGraph = (cx: number, cy: number) => {
    const r = wrapRef.current!.getBoundingClientRect();
    return { gx: (cx - r.left - view.x) / view.k, gy: (cy - r.top - view.y) / view.k };
  };
  function onWheel(e: React.WheelEvent) {
    userMoved.current = true;
    const r = wrapRef.current!.getBoundingClientRect();
    const mx = e.clientX - r.left, my = e.clientY - r.top;
    setView((v) => {
      const k = clampK(v.k * (1 - e.deltaY * 0.0015));
      const gx = (mx - v.x) / v.k, gy = (my - v.y) / v.k;
      return { k, x: mx - gx * k, y: my - gy * k };
    });
  }
  function zoomBy(f: number) {
    userMoved.current = true;
    const el = wrapRef.current; const w = el?.clientWidth ?? 0, h = el?.clientHeight ?? 0;
    setView((v) => { const k = clampK(v.k * f); const gx = (w / 2 - v.x) / v.k, gy = (h / 2 - v.y) / v.k; return { k, x: w / 2 - gx * k, y: h / 2 - gy * k }; });
  }
  function onDown(e: React.MouseEvent, id?: string) {
    e.stopPropagation();
    userMoved.current = true;
    if (id) {
      const n = byId[id]; n.fixed = true;
      const { gx, gy } = toGraph(e.clientX, e.clientY);
      dragRef.current = { mode: "node", id, sx: gx, sy: gy, ox: n.x, oy: n.y };
      setSelected(id);
    } else {
      dragRef.current = { mode: "pan", sx: e.clientX, sy: e.clientY, ox: view.x, oy: view.y };
    }
  }
  function onMove(e: React.MouseEvent) {
    const d = dragRef.current; if (!d) return;
    if (d.mode === "pan") setView((v) => ({ ...v, x: d.ox + (e.clientX - d.sx), y: d.oy + (e.clientY - d.sy) }));
    else if (d.id) { const { gx, gy } = toGraph(e.clientX, e.clientY); const n = byId[d.id]; n.x = d.ox + (gx - d.sx); n.y = d.oy + (gy - d.sy); n.vx = 0; n.vy = 0; force((v) => v + 1); }
  }
  function onUp() { const d = dragRef.current; if (d?.mode === "node" && d.id) byId[d.id].fixed = false; dragRef.current = null; }

  const q = query.trim().toLowerCase();
  const sel = selected ? byId[selected] : null;
  const focus = selected || hovered;
  const hi = focus ? neighbors[focus] ?? new Set<string>() : null;

  return (
    <div className="cg">
      <div className="cg-bar">
        <input className="cg-search" placeholder="Find a function…" value={query} onChange={(e) => setQuery(e.target.value)} />
        <span className="cg-stat">{nodes.length} of {total} nodes · {edges.length} edges{truncated ? " · top by connections" : ""}</span>
        <div className="cg-legend">
          {Object.entries(KIND_COLOR).map(([k, c]) => <span key={k} className="cg-leg"><span className="dot" style={{ background: c }} />{k}</span>)}
        </div>
      </div>

      <div className="cg-canvas" ref={wrapRef} onWheel={onWheel} onMouseDown={(e) => onDown(e)} onMouseMove={onMove} onMouseUp={onUp} onMouseLeave={onUp}>
        <svg width="100%" height="100%">
          <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
            {edges.map((e, i) => {
              const a = byId[e.from], b = byId[e.to]; if (!a || !b) return null;
              const active = focus && (e.from === focus || e.to === focus);
              return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke={active ? "var(--accent)" : "var(--border-strong)"} strokeWidth={active ? 1.6 : 0.7} strokeOpacity={focus && !active ? 0.12 : 0.5} />;
            })}
            {nodes.map((n) => {
              const match = q && n.name.toLowerCase().includes(q);
              const dim = (focus && n.id !== focus && !hi?.has(n.id)) || (q && !match);
              const showLabel = match || n.id === focus || (n.deg >= 5 && view.k > 0.5);
              return (
                <g key={n.id} transform={`translate(${n.x},${n.y})`} style={{ cursor: "pointer", opacity: dim ? 0.22 : 1 }}
                   onMouseDown={(e) => onDown(e, n.id)} onMouseEnter={() => setHovered(n.id)} onMouseLeave={() => setHovered(null)}>
                  <circle r={n.r} fill={colorFor(n.kind)} stroke={match || n.id === selected ? "var(--fg)" : "#fff"} strokeWidth={match || n.id === selected ? 2 : 1} />
                  {showLabel && <text x={n.r + 3} y={3.5} fontSize={10} fill="var(--fg-dim)" style={{ pointerEvents: "none", fontFamily: "var(--mono)" }}>{n.name.length > 26 ? n.name.slice(0, 26) + "…" : n.name}</text>}
                </g>
              );
            })}
          </g>
        </svg>

        <div className="cg-controls">
          <button className="cg-ctl wide" onClick={() => { userMoved.current = false; fitView(); }} title="Fit to view">⤢ Fit</button>
          <button className="cg-ctl" onClick={() => zoomBy(1.25)} title="Zoom in">+</button>
          <button className="cg-ctl" onClick={() => zoomBy(0.8)} title="Zoom out">−</button>
        </div>

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
