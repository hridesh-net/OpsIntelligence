import { useEffect, useRef, useState, type ReactNode } from "react";

/**
 * Lightweight dropdown: renders a trigger and an absolutely-positioned
 * menu (styled by the global .menu class) that closes on outside-click / Esc.
 */
export default function Dropdown({
  trigger,
  children,
  align = "left",
  menuStyle,
}: {
  trigger: (open: boolean, toggle: () => void) => ReactNode;
  children: (close: () => void) => ReactNode;
  align?: "left" | "right";
  menuStyle?: React.CSSProperties;
}) {
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={wrap} style={{ position: "relative", display: "inline-flex" }}>
      {trigger(open, () => setOpen((o) => !o))}
      {open && (
        <div
          className="menu"
          style={{ top: "calc(100% + 6px)", ...(align === "right" ? { right: 0 } : { left: 0 }), ...menuStyle }}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  );
}
