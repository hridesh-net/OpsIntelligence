// Thin typed fetch wrapper. Sends cookies, surfaces errors, threads CSRF + If-Match.
// All endpoints are relative — Vite dev proxies /api to localhost:8080; in prod the
// embedded SPA is served same-origin as the gateway so paths just work.

export class ApiError extends Error {
  constructor(public status: number, public body: unknown, message: string) {
    super(message);
  }
}

interface RequestOptions extends Omit<RequestInit, "body"> {
  body?: unknown;
  ifMatch?: string;
}

let csrfToken: string | null = null;

export function setCsrfToken(token: string | null) {
  csrfToken = token;
}

export async function api<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { body, ifMatch, headers, method = body ? "POST" : "GET", ...rest } = opts;
  const h = new Headers(headers);
  if (body !== undefined && !h.has("Content-Type")) h.set("Content-Type", "application/json");
  if (ifMatch) h.set("If-Match", ifMatch);
  if (csrfToken && method !== "GET" && method !== "HEAD") h.set("X-CSRF-Token", csrfToken);

  const res = await fetch(path, {
    ...rest,
    method,
    headers: h,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  const text = await res.text();
  let parsed: unknown = null;
  if (text) {
    try { parsed = JSON.parse(text); } catch { parsed = text; }
  }

  if (!res.ok) {
    const msg = (parsed && typeof parsed === "object" && "error" in parsed && typeof (parsed as { error: unknown }).error === "string")
      ? (parsed as { error: string }).error
      : `HTTP ${res.status}`;
    throw new ApiError(res.status, parsed, msg);
  }
  return parsed as T;
}

// Streaming helper for SSE-style NDJSON responses (chat).
export async function* stream(path: string, body: unknown, signal?: AbortSignal): AsyncGenerator<unknown> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify(body),
    signal,
  });
  if (!res.ok || !res.body) {
    throw new ApiError(res.status, null, `stream HTTP ${res.status}`);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx: number;
    while ((idx = buf.indexOf("\n")) !== -1) {
      const line = buf.slice(0, idx).trim();
      buf = buf.slice(idx + 1);
      if (!line) continue;
      const payload = line.startsWith("data:") ? line.slice(5).trim() : line;
      if (!payload || payload === "[DONE]") continue;
      try { yield JSON.parse(payload); } catch { yield payload; }
    }
  }
}
