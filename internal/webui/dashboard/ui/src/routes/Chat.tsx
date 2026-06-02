import { useRef, useState } from "react";
import { Topbar } from "@/chrome/Topbar";
import { stream } from "@/api/client";

interface Message {
  role: "user" | "assistant";
  content: string;
}

export function Chat() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);

  async function send() {
    const text = input.trim();
    if (!text || busy) return;
    setInput("");
    setMessages((m) => [...m, { role: "user", content: text }, { role: "assistant", content: "" }]);
    setBusy(true);

    abortRef.current?.abort();
    abortRef.current = new AbortController();

    try {
      for await (const evt of stream("/api/rag-chat", { message: text }, abortRef.current.signal)) {
        if (evt && typeof evt === "object" && "content" in evt) {
          const chunk = (evt as { content?: string }).content ?? "";
          setMessages((m) => {
            const next = m.slice();
            const last = next[next.length - 1];
            if (last?.role === "assistant") next[next.length - 1] = { ...last, content: last.content + chunk };
            return next;
          });
        }
        queueMicrotask(() => scrollerRef.current?.scrollTo({ top: scrollerRef.current.scrollHeight }));
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        setMessages((m) => {
          const next = m.slice();
          const last = next[next.length - 1];
          if (last?.role === "assistant") next[next.length - 1] = { ...last, content: last.content + `\n\n[error: ${(err as Error).message}]` };
          return next;
        });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Topbar title="Chat" sub="RAG over indexed repos" />
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
        <div ref={scrollerRef} style={{ flex: 1, overflow: "auto", padding: 24, display: "flex", flexDirection: "column", gap: 14 }}>
          {messages.length === 0 && (
            <div className="empty">Ask anything about your indexed repos.</div>
          )}
          {messages.map((m, i) => (
            <div key={i} style={{
              alignSelf: m.role === "user" ? "flex-end" : "flex-start",
              maxWidth: "70%",
              background: m.role === "user" ? "var(--accent-soft)" : "var(--bg-elev)",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius)",
              padding: "10px 14px",
              whiteSpace: "pre-wrap",
              color: "var(--fg)",
            }}>
              {m.content || <span style={{ color: "var(--fg-muted)" }}>…</span>}
            </div>
          ))}
        </div>
        <div style={{ padding: 16, borderTop: "1px solid var(--border)", background: "var(--bg-elev)" }}>
          <div style={{ display: "flex", gap: 8 }}>
            <input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }}
              placeholder="Ask…"
              style={{
                flex: 1,
                padding: "10px 14px",
                border: "1px solid var(--border)",
                borderRadius: "var(--radius-sm)",
                background: "var(--bg)",
                color: "var(--fg)",
                fontSize: 14,
                outline: "none",
              }}
            />
            <button className="btn primary" onClick={send} disabled={busy}>{busy ? "…" : "Send"}</button>
          </div>
        </div>
      </div>
    </>
  );
}
