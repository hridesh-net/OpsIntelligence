import type { CSSProperties } from "react";

/** Square monogram avatar used across cards, panels and lists. */
export default function Avatar({
  color,
  ini,
  name,
  stack = false,
  className = "",
  style,
}: {
  color: string;
  ini: string;
  name?: string;
  stack?: boolean;
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <span
      className={"av" + (stack ? " stack" : "") + (className ? " " + className : "")}
      style={{ background: color, ...style }}
      title={name}
    >
      {ini}
    </span>
  );
}
