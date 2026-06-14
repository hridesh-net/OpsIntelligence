import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Topbar } from "@/chrome/Topbar";
import { stream } from "@/api/client";
import { whoami } from "@/api/auth";
import { Markdown } from "@/components/Markdown";
import "./chat.css";

interface Step { name: string; done: boolean }
interface Message {
  role: "user" | "assistant";
  content: string;
  steps?: Step[];
  error?: string;
}
interface Conversation {
  id: string;
  title: string;
  messages: Message[];
  updatedAt: number;
}

const STORE_KEY = "opsint:chats";

const SUGGESTIONS = [
  { icon: "≡", text: "Summarize recent changes across my indexed repos" },
  { icon: "!", text: "What are the biggest security risks right now?" },
  { icon: "{}", text: "Explain how the authentication flow works" },
  { icon: "↯", text: "What are my agents currently working on?" },
];

function loadChats(): Conversation[] {
  try {
    const raw = localStorage.getItem(STORE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function uid(): string {
  // session id for the backend; crypto.randomUUID where available.
  return (crypto as { randomUUID?: () => string }).randomUUID?.()
    ?? `c_${Date.now().toString(36)}${Math.floor(Math.random() * 1e6).toString(36)}`;
}

function titleFrom(text: string): string {
  const t = text.trim().replace(/\s+/g, " ");
  return t.length > 44 ? t.slice(0, 44) + "…" : t;
}

function relTime(ts: number): string {
  const diff = (Date.now() - ts) / 1000;
  if (diff < 60) return "now";
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

export function Chat() {
  const [conversations, setConversations] = useState<Conversation[]>(loadChats);
  const [currentId, setCurrentId] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [showHistory, setShowHistory] = useState(true);

  const abortRef = useRef<AbortController | null>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const idRef = useRef<string | null>(null);
  idRef.current = currentId;

  const meQ = useQuery({ queryKey: ["whoami"], queryFn: whoami, staleTime: 60_000 });
  const firstName = (meQ.data?.display_name || meQ.data?.username || "there").split(/\s+/)[0];

  const current = conversations.find((c) => c.id === currentId) ?? null;
  const messages = current?.messages ?? [];

  // persist whenever conversations change
  useEffect(() => {
    try { localStorage.setItem(STORE_KEY, JSON.stringify(conversations.slice(0, 50))); } catch { /* quota */ }
  }, [conversations]);

  // auto-grow textarea
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = `${Math.min(ta.scrollHeight, 180)}px`;
  }, [input]);

  const stick = () => queueMicrotask(() =>
    scrollerRef.current?.scrollTo({ top: scrollerRef.current.scrollHeight }));

  // mutate the active conversation's last assistant message
  function patchLast(id: string, fn: (m: Message) => Message) {
    setConversations((prev) => prev.map((c) => {
      if (c.id !== id) return c;
      const msgs = c.messages.slice();
      const last = msgs[msgs.length - 1];
      if (last?.role === "assistant") msgs[msgs.length - 1] = fn(last);
      return { ...c, messages: msgs, updatedAt: Date.now() };
    }));
  }

  async function send(text: string) {
    const msg = text.trim();
    if (!msg || busy) return;
    setInput("");

    // ensure a conversation exists
    let id = idRef.current;
    if (!id || !conversations.some((c) => c.id === id)) {
      id = uid();
      const convo: Conversation = { id, title: titleFrom(msg), messages: [], updatedAt: Date.now() };
      setConversations((prev) => [convo, ...prev]);
      setCurrentId(id);
      idRef.current = id;
    }
    const cid = id;

    setConversations((prev) => prev.map((c) => c.id === cid
      ? { ...c, messages: [...c.messages, { role: "user", content: msg }, { role: "assistant", content: "", steps: [] }], updatedAt: Date.now() }
      : c));
    setBusy(true);
    stick();

    abortRef.current?.abort();
    abortRef.current = new AbortController();

    try {
      for await (const evt of stream("/api/rag-chat", { message: msg, session_id: cid }, abortRef.current.signal)) {
        if (!evt || typeof evt !== "object") continue;
        const e = evt as { type?: string; content?: string; name?: string };
        if (e.type === "tool_start" && e.name) {
          patchLast(cid, (m) => ({ ...m, steps: [...(m.steps ?? []), { name: e.name!, done: false }] }));
        } else if (e.type === "tool_end" && e.name) {
          patchLast(cid, (m) => {
            const steps = (m.steps ?? []).slice();
            for (let i = steps.length - 1; i >= 0; i--) {
              if (steps[i].name === e.name && !steps[i].done) { steps[i] = { ...steps[i], done: true }; break; }
            }
            return { ...m, steps };
          });
        } else if (e.type === "error") {
          patchLast(cid, (m) => ({ ...m, error: e.content || "stream error" }));
        } else if (e.content) {
          // token (or any content-bearing event) → append to the answer
          patchLast(cid, (m) => ({ ...m, content: m.content + e.content }));
        }
        stick();
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        patchLast(cid, (m) => ({ ...m, error: (err as Error).message }));
      }
    } finally {
      setBusy(false);
    }
  }

  function newChat() {
    abortRef.current?.abort();
    setBusy(false);
    setCurrentId(null);
    idRef.current = null;
    setInput("");
    taRef.current?.focus();
  }

  function deleteChat(id: string, e: React.MouseEvent) {
    e.stopPropagation();
    setConversations((prev) => prev.filter((c) => c.id !== id));
    if (idRef.current === id) { setCurrentId(null); idRef.current = null; }
  }

  const empty = messages.length === 0;

  return (
    <>
      <Topbar
        title="Chat"
        sub="RAG over indexed repos"
        actions={
          <div className="chat-act">
            <button className="btn" onClick={() => setShowHistory((v) => !v)}>{showHistory ? "Hide history" : "History"}</button>
            <button className="btn primary" onClick={newChat}>+ New chat</button>
          </div>
        }
      />
      <div className="chat">
        <div className="chat-body">
          <div className="chat-main">
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
                      <div className="stack">
                        {m.steps && m.steps.length > 0 && (
                          <div className="steps">
                            {m.steps.map((s, si) => (
                              <span key={si} className={`step ${s.done ? "done" : "running"}`}>
                                <span className="si">{s.done ? "✓" : <Gear />}</span>
                                <span>Called</span><span className="sn">{s.name}</span>
                              </span>
                            ))}
                          </div>
                        )}
                        {(m.content || (m.role === "assistant" && !m.error && (!m.steps || m.steps.length === 0))) && (
                          <div className="bubble">
                            {m.content
                              ? <Markdown text={m.content} />
                              : <span className="typing"><i /><i /><i /></span>}
                          </div>
                        )}
                        {m.error && <div className="msg-error">{m.error}</div>}
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
                  {busy ? (
                    <button className="send stop" onClick={() => abortRef.current?.abort()} title="Stop">
                      <span style={{ width: 11, height: 11, background: "#fff", borderRadius: 2, display: "block" }} />
                    </button>
                  ) : (
                    <button className="send" onClick={() => send(input)} disabled={!input.trim()} title="Send">
                      <SendIcon />
                    </button>
                  )}
                </div>
                <div className="composer-hint">Enter to send · Shift+Enter for a new line · responses generated on-premise</div>
              </div>
            </div>
          </div>

          {showHistory && (
            <aside className="chat-history">
              <div className="hh"><span>History</span><span>{conversations.length}</span></div>
              <div className="hlist">
                {conversations.length === 0 ? (
                  <div className="hempty">No conversations yet.<br />Your chats are stored on this device.</div>
                ) : conversations.map((c) => (
                  <div
                    key={c.id}
                    className={`hitem${c.id === currentId ? " active" : ""}`}
                    onClick={() => setCurrentId(c.id)}
                  >
                    <div className="ht">{c.title}</div>
                    <div className="hd">{c.messages.filter((m) => m.role === "user").length} msg · {relTime(c.updatedAt)}</div>
                    <button className="hx" title="Delete" onClick={(e) => deleteChat(c.id, e)}>×</button>
                  </div>
                ))}
              </div>
            </aside>
          )}
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

function Gear() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2v4M12 18v4M2 12h4M18 12h4M4.9 4.9l2.8 2.8M16.3 16.3l2.8 2.8M19.1 4.9l-2.8 2.8M7.7 16.3l-2.8 2.8" />
    </svg>
  );
}
