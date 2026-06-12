/* ============================================================
   SCRUN — DOM side-effects for theme + accent custom properties
   (the only places we touch document directly)
   ============================================================ */
import type { Theme } from "../types";

type AccentDef = { a: string; b: string; rgb: string };

const ACCENTS: Record<string, AccentDef> = {
  "#e4572e": { a: "#e4572e", b: "#f06a40", rgb: "228,87,46" }, // OpsIntelligence brand
  "#2898da": { a: "#2898da", b: "#4db0ef", rgb: "40,152,218" },
  "#2dd4bf": { a: "#2dd4bf", b: "#5fe6d4", rgb: "45,212,191" },
  "#a78bfa": { a: "#a78bfa", b: "#c0acff", rgb: "167,139,250" },
  "#f5b042": { a: "#f5b042", b: "#ffc66b", rgb: "245,176,66" },
};

export function applyThemeAttr(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
}

export function applyAccentVars(hex: string): void {
  const r = document.documentElement.style;
  const p = ACCENTS[hex];
  if (p) {
    r.setProperty("--accent", p.a);
    r.setProperty("--accent-bright", p.b);
    r.setProperty("--running", p.a);
    r.setProperty("--accent-rgb", p.rgb);
    r.setProperty("--accent-soft", `rgba(${p.rgb},.16)`);
    r.setProperty("--accent-line", `rgba(${p.rgb},.45)`);
  } else {
    // arbitrary hex (setup wizard custom colours)
    r.setProperty("--accent", hex);
    r.setProperty("--accent-bright", hex);
    r.setProperty("--running", hex);
  }
}

export function setFavicon(innerSvg: string): void {
  const fav =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
    `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#e4572e"/><stop offset="1" stop-color="#f06a40"/></linearGradient></defs>` +
    `<rect width="100" height="100" rx="24" fill="url(#g)"/>` +
    `<g transform="translate(10,10) scale(0.8)">${innerSvg}</g></svg>`;
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (!link) {
    link = document.createElement("link");
    link.rel = "icon";
    document.head.appendChild(link);
  }
  link.type = "image/svg+xml";
  link.href = "data:image/svg+xml," + encodeURIComponent(fav);
}
