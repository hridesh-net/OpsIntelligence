import React from "react";

/**
 * Minimal, dependency-free Markdown renderer tuned for streaming chat replies.
 * Handles fenced code, headings, ordered/unordered lists, paragraphs and the
 * common inline marks (bold, italic, inline code, links). Tolerant of partial
 * input so a half-streamed message still renders sensibly.
 */
export function Markdown({ text }: { text: string }) {
  return <>{renderBlocks(text)}</>;
}

function renderBlocks(src: string): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  // Split on fenced code blocks first so their contents are never parsed.
  const parts = src.split(/(```[\s\S]*?```|```[\s\S]*$)/g);
  let key = 0;
  for (const part of parts) {
    if (!part) continue;
    if (part.startsWith("```")) {
      const body = part.replace(/^```[^\n]*\n?/, "").replace(/```$/, "");
      out.push(
        <pre key={key++} className="md-pre"><code>{body.replace(/\n$/, "")}</code></pre>,
      );
      continue;
    }
    out.push(...renderProse(part, () => key++));
  }
  return out;
}

function renderProse(text: string, nextKey: () => number): React.ReactNode[] {
  const lines = text.split("\n");
  const out: React.ReactNode[] = [];
  let i = 0;
  let para: string[] = [];

  const flushPara = () => {
    if (para.length) {
      out.push(<p key={nextKey()} className="md-p">{inline(para.join("\n"), nextKey)}</p>);
      para = [];
    }
  };

  while (i < lines.length) {
    const line = lines[i];
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    const isUl = /^\s*[-*]\s+/.test(line);
    const isOl = /^\s*\d+\.\s+/.test(line);

    if (heading) {
      flushPara();
      const level = Math.min(heading[1].length, 4);
      const Tag = (`h${level + 2}`) as keyof React.JSX.IntrinsicElements;
      out.push(<Tag key={nextKey()} className="md-h">{inline(heading[2], nextKey)}</Tag>);
      i++;
    } else if (isUl || isOl) {
      flushPara();
      const items: React.ReactNode[] = [];
      const re = isOl ? /^\s*\d+\.\s+(.*)$/ : /^\s*[-*]\s+(.*)$/;
      while (i < lines.length && (isOl ? /^\s*\d+\.\s+/ : /^\s*[-*]\s+/).test(lines[i])) {
        const m = re.exec(lines[i]);
        items.push(<li key={items.length}>{inline(m ? m[1] : lines[i], nextKey)}</li>);
        i++;
      }
      out.push(isOl
        ? <ol key={nextKey()} className="md-list">{items}</ol>
        : <ul key={nextKey()} className="md-list">{items}</ul>);
    } else if (line.trim() === "") {
      flushPara();
      i++;
    } else {
      para.push(line);
      i++;
    }
  }
  flushPara();
  return out;
}

// Inline marks: `code`, **bold**, *italic*, [text](url). Order matters — code first.
function inline(text: string, nextKey: () => number): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*)|(\[[^\]]+\]\([^)]+\))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith("`")) {
      nodes.push(<code key={nextKey()} className="md-code">{tok.slice(1, -1)}</code>);
    } else if (tok.startsWith("**")) {
      nodes.push(<strong key={nextKey()}>{tok.slice(2, -2)}</strong>);
    } else if (tok.startsWith("*")) {
      nodes.push(<em key={nextKey()}>{tok.slice(1, -1)}</em>);
    } else {
      const lm = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok);
      if (lm) nodes.push(<a key={nextKey()} href={lm[2]} target="_blank" rel="noopener noreferrer">{lm[1]}</a>);
      else nodes.push(tok);
    }
    last = m.index + tok.length;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}
