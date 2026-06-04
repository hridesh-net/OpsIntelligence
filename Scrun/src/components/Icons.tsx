import type { SVGProps } from "react";

/* ============================================================
   SCRUN — Reusable inline icons (stroke = currentColor)
   ============================================================ */
type P = SVGProps<SVGSVGElement> & { size?: number };

const base = (size = 16): P => ({
  width: size,
  height: size,
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 2,
});

export const Close = ({ size = 18, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M18 6 6 18M6 6l12 12" />
  </svg>
);
export const Check = ({ size = 12, ...p }: P) => (
  <svg {...base(size)} strokeWidth={3} {...p}>
    <path d="m5 12 5 5L20 6" />
  </svg>
);
export const Plus = ({ size = 16, ...p }: P) => (
  <svg {...base(size)} strokeWidth={2.4} {...p}>
    <path d="M12 5v14M5 12h14" />
  </svg>
);
export const ChevronDown = ({ size = 14, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="m6 9 6 6 6-6" />
  </svg>
);
export const ChevronRight = ({ size = 14, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M9 6l6 6-6 6" />
  </svg>
);
export const ArrowRight = ({ size = 18, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M5 12h14M13 6l6 6-6 6" />
  </svg>
);
export const Search = ({ size = 14, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <circle cx="11" cy="11" r="7" />
    <path d="m20 20-3.5-3.5" />
  </svg>
);
export const Edit = ({ size = 15, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M12 20h9" />
    <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
  </svg>
);
export const Cog = ({ size = 13, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <circle cx="12" cy="12" r="2.4" />
    <path d="M12 4v2M12 18v2M4 12h2M18 12h2" />
  </svg>
);
export const Gate = ({ size = 12, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <rect x="5" y="11" width="14" height="10" rx="2" />
    <path d="M8 11V7a4 4 0 0 1 8 0v4" />
  </svg>
);
export const Grip = ({ size = 13, ...p }: P) => (
  <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" {...p}>
    <circle cx="9" cy="6" r="1.6" />
    <circle cx="15" cy="6" r="1.6" />
    <circle cx="9" cy="12" r="1.6" />
    <circle cx="15" cy="12" r="1.6" />
    <circle cx="9" cy="18" r="1.6" />
    <circle cx="15" cy="18" r="1.6" />
  </svg>
);
export const Trash = ({ size = 13, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14" />
  </svg>
);
export const Warn = ({ size = 13, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M12 9v4M12 17h.01" />
    <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
  </svg>
);
export const Activity = ({ size = 16, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M3 12h4l2 6 4-12 2 6h6" />
  </svg>
);
export const Doc = ({ size = 13, ...p }: P) => (
  <svg {...base(size)} {...p}>
    <path d="M4 19V5a2 2 0 0 1 2-2h9l5 5v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2Z" />
    <path d="M14 3v5h5" />
  </svg>
);
