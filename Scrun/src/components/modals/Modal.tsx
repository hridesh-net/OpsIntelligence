import type { ReactNode } from "react";
import s from "./Modal.module.css";

/** Centred modal with blurred backdrop; clicking the backdrop closes it. */
export default function Modal({
  onClose,
  children,
  maxWidth,
}: {
  onClose: () => void;
  children: ReactNode;
  maxWidth?: number;
}) {
  return (
    <div
      className={s.overlay}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className={s.modal} style={maxWidth ? { maxWidth } : undefined}>
        {children}
      </div>
    </div>
  );
}
