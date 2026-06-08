import { Fragment } from "react";

/**
 * Renders activity-log strings that may contain <b>…</b> emphasis markers
 * (the only "markup" the sim engine produces) — without dangerouslySetInnerHTML.
 */
export default function RichText({ text }: { text: string }) {
  const parts = text.split(/(<b>.*?<\/b>)/g);
  return (
    <>
      {parts.map((p, i) => {
        const m = p.match(/^<b>(.*?)<\/b>$/);
        return m ? <b key={i}>{m[1]}</b> : <Fragment key={i}>{p}</Fragment>;
      })}
    </>
  );
}
