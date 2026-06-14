import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { stream } from "@/api/client";
import { whoami } from "@/api/auth";
import { Markdown } from "@/components/Markdown";
import "./chat.css";

interface Message {
  role: "user" | "assistant";
  content: string;
}

const SUGGESTIONS = [
  { icon: "≡", text: "Summarize recent changes across my indexed repos" },
  { icon: "!", text: "What are the biggest security risks right now?" },
  { icon: "{}", text: "Explain how the authentication flow works" },
  { icon: "↯", text: "What are my agents currently working on?" },
];

export function Chat() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  const meQ = useQuery({ queryKey: ["whoami"], queryFn: whoami, staleTime: 60_000 });
  const firstName = (meQ.data?.display_name || meQ.data?.username || "there").split(/\s+/)[0];

  // auto-grow the textarea
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, 180)}px`;
  }, [input]);

  async function send(text: string) {
    const msg = text.trim();
    if (!msg || busy) return;
    setInput("");
    setMessages((m) => [...m, { role: "user", content: msg }, { role: "assistant", content: "" }]);
    setBusy(true);

    abortRef.current?.abort();
    abortRef.current = new AbortController();

    const stick = () => queueMicrotask(() =>
      scrollerRef.current?.scrollTo({ top: scrollerRef.current.scrollHeight }));
    stick();

    try {
      for await (const evt of stream("/api/rag-chat", { message: msg }, abortRef.current.signal)) {
        if (evt && typeof evt === "object" && "content" in evt) {
          const chunk = (evt as { content?: string }).content ?? "";
          setMessages((m) => {
            const next = m.slice();
            const last = next[next.length - 1];
            if (last?.role === "assistant") next[next.length - 1] = { ...last, content: last.content + chunk };
            return next;
          });
        }
        stick();
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        setMessages((m) => {
          const next = m.slice();
          const last = next[next.length - 1];
          if (last?.role === "assistant") next[next.length - 1] = { ...last, content: last.content + `\n\n_[error: ${(err as Error).message}]_` };
          return next;
        });
      }
    } finally {
      setBusy(false);
    }
  }

  const empty = messages.length === 0;

  return (
    <>
      <Topbar title="Chat" sub="RAG over indexed repos" />
      <div className="chat">
        <div className="chat-scroll" ref={scrollerRef}>
          {empty ? (
            <div className="chat-hero">
              <div className="mark">✦</div>
              <h2>Good to see you, <span className="accent">{firstName}</span>.<br />How can I help today?</h2>
              <p>Ask anything about your indexed repositories, agents and operations. Answers stay scoped to your access — and on this server.</p>
              <div className="chat-chips">
                {SUGGESTIONS.map((s) => (
                  <button key={s.text} className="chat-chip" onClick={() => send(s.text)}>
                    <span className="ci">{s.icon}</span>{s.text}
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="chat-col">
              {messages.map((m, i) => (
                <div key={i} className={`msg ${m.role}`}>
                  <div className="av">{m.role === "assistant" ? "✦" : (firstName[0] || "?").toUpperCase()}</div>
                  <div className="bubble">
                    {m.content
                      ? <Markdown text={m.content} />
                      : <span className="typing"><i /><i /><i /></span>}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="chat-composer">
          <div className="inner">
            <div className="composer-box">
              <button className="plus" title="Attach (coming soon)" tabIndex={-1}>+</button>
              <textarea
                ref={taRef}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(input); } }}
                placeholder="Ask anything…"
                rows={1}
              />
              <button className="send" onClick={() => send(input)} disabled={busy || !input.trim()} title="Send">
                <SendIcon />
              </button>
            </div>
            <div className="composer-hint">Enter to send · Shift+Enter for a new line · responses generated on-premise</div>
          </div>
        </div>
      </div>
    </>
  );
}

function SendIcon() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M22 2 11 13" />
      <path d="M22 2 15 22l-4-9-9-4 20-7Z" />
    </svg>
  );
}
