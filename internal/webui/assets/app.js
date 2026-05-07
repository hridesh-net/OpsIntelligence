/* ═══════════════════════════════════════════════════════════
   OpsIntelligence — Knowledge Chat UI
   ═══════════════════════════════════════════════════════════ */

'use strict';

// ── State ────────────────────────────────────────────────────
const state = {
    token: '',
    sessionId: '',
    streaming: false,
    mode: 'rag',           // 'rag' | 'agent'
    selectedRepos: [],     // [] = all repos; ['id1',...] = specific repos
    repos: [],             // fetched repo list
};

// ── Init ─────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
    state.token     = localStorage.getItem('ac_token')   || '';
    state.sessionId = localStorage.getItem('ac_session') || generateId();
    state.mode      = localStorage.getItem('ac_mode')    || 'rag';
    saveSession();
    applyMode(state.mode);

    document.getElementById('token-input').addEventListener('keydown', e => {
        if (e.key === 'Enter') submitAuth();
    });

    if (state.token) {
        hideAuthGate();
        fetchStatus();
        fetchRepos();
    }
});

// ── Auth ─────────────────────────────────────────────────────
function submitAuth() {
    const input = document.getElementById('token-input');
    const tok = input.value.trim();
    if (!tok) { showAuthError('Token is required.'); return; }
    state.token = tok;
    localStorage.setItem('ac_token', tok);
    showAuthError('');
    hideAuthGate();
    fetchStatus();
    fetchRepos();
}

function logout() {
    localStorage.removeItem('ac_token');
    state.token = '';
    document.getElementById('token-input').value = '';
    document.getElementById('auth-gate').classList.remove('hidden');
}

function showAuthError(msg) { document.getElementById('auth-error').textContent = msg; }
function hideAuthGate()     { document.getElementById('auth-gate').classList.add('hidden'); }

// ── Mode switching ───────────────────────────────────────────
function setMode(mode) {
    state.mode = mode;
    localStorage.setItem('ac_mode', mode);
    applyMode(mode);
}

function applyMode(mode) {
    document.getElementById('tab-rag').classList.toggle('active', mode === 'rag');
    document.getElementById('tab-agent').classList.toggle('active', mode === 'agent');

    const repoSection = document.getElementById('repo-section');
    const modeHint    = document.getElementById('mode-hint');
    const badge       = document.getElementById('input-mode-badge');
    const title       = document.getElementById('chat-title');
    const input       = document.getElementById('msg-input');

    if (mode === 'rag') {
        repoSection.style.display = '';
        modeHint.textContent = 'Answers grounded in your indexed repos.';
        badge.textContent    = 'RAG';
        badge.className      = 'input-mode-badge rag';
        title.textContent    = 'Knowledge Chat';
        input.placeholder    = 'Ask about your repositories… (Enter to send)';
    } else {
        repoSection.style.display = 'none';
        modeHint.textContent = 'Full autonomous agent with tools.';
        badge.textContent    = 'AGENT';
        badge.className      = 'input-mode-badge agent';
        title.textContent    = 'Agent Chat';
        input.placeholder    = 'Message OpsIntelligence… (Enter to send)';
    }
    updateContextBadge();
}

// ── Repo list ────────────────────────────────────────────────
async function fetchRepos() {
    try {
        const res = await apiFetch('/api/v1/repos');
        if (!res.ok) return;
        const data = await res.json();
        state.repos = data.repos || [];
        renderRepoList();
    } catch {
        // Silently ignore — repointel might be disabled.
    }
}

function renderRepoList() {
    const el = document.getElementById('repo-list');
    if (!el) return;

    if (state.repos.length === 0) {
        el.innerHTML = '<div class="repo-empty">No repos indexed yet. Use <code>opsintelligence repos add</code>.</div>';
        return;
    }

    let html = `<button class="repo-item ${state.selectedRepos.length === 0 ? 'active' : ''}" onclick="selectRepo(null)">
      <span class="repo-icon">🌐</span>
      <div class="repo-info"><div class="repo-name">All Repositories</div><div class="repo-meta">${state.repos.length} indexed</div></div>
    </button>`;

    for (const r of state.repos) {
        const active = state.selectedRepos.includes(r.id);
        const risk   = r.risk_level ? `<span class="risk-badge ${r.risk_level}">${r.risk_level}</span>` : '';
        const lang   = r.language   ? `<span class="lang-tag">${r.language}</span>` : '';
        html += `<button class="repo-item ${active ? 'active' : ''}" onclick="selectRepo('${escHtml(r.id)}')">
          <span class="repo-icon">📦</span>
          <div class="repo-info">
            <div class="repo-name">${escHtml(r.full_name || r.id)}</div>
            <div class="repo-meta">${lang}${risk}</div>
          </div>
        </button>`;
    }

    el.innerHTML = html;
}

function selectRepo(id) {
    if (id === null) {
        state.selectedRepos = [];
    } else {
        state.selectedRepos = [id];
    }
    renderRepoList();
    updateContextBadge();
}

function updateContextBadge() {
    const badge = document.getElementById('context-badge');
    if (!badge) return;
    if (state.mode !== 'rag') {
        badge.style.display = 'none';
        return;
    }
    if (state.selectedRepos.length === 0) {
        badge.style.display = state.repos.length > 0 ? '' : 'none';
        badge.textContent   = `${state.repos.length} repos`;
    } else {
        badge.style.display = '';
        const r = state.repos.find(x => x.id === state.selectedRepos[0]);
        badge.textContent   = r ? r.full_name : state.selectedRepos[0];
    }
}

// ── Status ───────────────────────────────────────────────────
async function fetchStatus() {
    try {
        const res = await apiFetch('/api/status');
        if (!res.ok) { setOffline(); return; }
        const data = await res.json();
        setOnline(data);
    } catch {
        setOffline();
    }
}

function setOnline(data) {
    document.getElementById('status-dot').className  = 'dot online';
    document.getElementById('status-text').textContent = 'Running' + (data.pid ? ` (PID ${data.pid})` : '');
    if (data.version) document.getElementById('version-badge').textContent = data.version;
    if (data.model)   document.getElementById('model-chip').textContent    = data.model;
}

function setOffline() {
    document.getElementById('status-dot').className  = 'dot offline';
    document.getElementById('status-text').textContent = 'Unreachable';
}

// ── Send message ─────────────────────────────────────────────
async function sendMessage() {
    if (state.streaming) return;
    const input = document.getElementById('msg-input');
    const text  = input.value.trim();
    if (!text) return;

    input.value = '';
    autoResize(input);
    appendMessage('user', text);
    hideEmptyState();

    const typingId   = appendTyping();
    state.streaming  = true;
    setSendDisabled(true);

    const agentMsgId  = 'msg-' + generateId();
    let agentText     = '';
    let bubbleEl      = null;
    let hasStartedBubble = false;
    let sourcesList   = [];

    const endpoint = state.mode === 'rag' ? '/api/rag-chat' : '/api/chat';
    const body     = state.mode === 'rag'
        ? { message: text, session_id: state.sessionId, repo_ids: state.selectedRepos, limit: 8 }
        : { message: text, session_id: state.sessionId };

    try {
        const res = await apiFetch(endpoint, {
            method:  'POST',
            headers: { 'Content-Type': 'application/json' },
            body:    JSON.stringify(body),
        });

        if (!res.ok) {
            removeTyping(typingId);
            const errText = await res.text();
            showToast('Error: ' + (errText || res.status), 'error');
            state.streaming = false;
            setSendDisabled(false);
            return;
        }

        const reader = res.body.getReader();
        const dec    = new TextDecoder();
        let buf      = '';

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buf += dec.decode(value, { stream: true });

            const lines = buf.split('\n');
            buf = lines.pop();

            for (const line of lines) {
                if (!line.startsWith('data: ')) continue;
                const raw = line.slice(6);
                if (raw === '[DONE]') break;

                let evt;
                try { evt = JSON.parse(raw); } catch { continue; }

                if (evt.type === 'sources') {
                    // Attribution list — capture for rendering after the reply.
                    sourcesList = evt.sources || [];
                } else if (evt.type === 'token') {
                    if (!hasStartedBubble) {
                        removeTyping(typingId);
                        bubbleEl = appendAgentBubble(agentMsgId);
                        hasStartedBubble = true;
                    }
                    agentText += evt.content;
                    renderBubble(bubbleEl, agentText);
                } else if (evt.type === 'tool_start') {
                    if (!hasStartedBubble) {
                        removeTyping(typingId);
                        bubbleEl = appendAgentBubble(agentMsgId);
                        hasStartedBubble = true;
                    }
                    appendToolCall(bubbleEl.parentElement.parentElement, evt.name, false);
                } else if (evt.type === 'tool_end') {
                    markToolCallDone(agentMsgId, evt.name);
                } else if (evt.type === 'error') {
                    showToast('Agent error: ' + evt.content, 'error');
                }
            }
        }

        // Append source attribution below the reply (RAG mode only).
        if (sourcesList.length > 0 && hasStartedBubble) {
            const msgEl = document.getElementById(agentMsgId);
            if (msgEl) appendSources(msgEl, sourcesList);
        }

    } catch (err) {
        showToast('Connection error: ' + err.message, 'error');
    } finally {
        removeTyping(typingId);
        if (!hasStartedBubble && agentText) {
            bubbleEl = appendAgentBubble(agentMsgId);
            renderBubble(bubbleEl, agentText);
        }
        state.streaming = false;
        setSendDisabled(false);
        scrollToBottom();
    }
}

// ── Example prompts ──────────────────────────────────────────
function useExample(btn) {
    document.getElementById('msg-input').value = btn.textContent;
    sendMessage();
}

// ── Keyboard ─────────────────────────────────────────────────
function handleKey(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
    }
}

function autoResize(el) {
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 180) + 'px';
}

// ── DOM helpers ───────────────────────────────────────────────
function hideEmptyState() {
    const es = document.getElementById('empty-state');
    if (es) es.remove();
}

function appendMessage(role, text) {
    const id   = 'msg-' + generateId();
    const wrap = buildMsgEl(role, id);
    const bubble = wrap.querySelector('.msg-bubble');
    if (role === 'user') {
        bubble.textContent = text;
    } else {
        renderBubble(bubble, text);
    }
    document.getElementById('messages').appendChild(wrap);
    scrollToBottom();
    return id;
}

function appendAgentBubble(id) {
    let existing = document.getElementById(id);
    if (existing) return existing.querySelector('.msg-bubble');
    const wrap = buildMsgEl('agent', id);
    document.getElementById('messages').appendChild(wrap);
    scrollToBottom();
    return wrap.querySelector('.msg-bubble');
}

function buildMsgEl(role, id) {
    const avatar = role === 'user' ? '🙂' : '🦅';
    const wrap   = document.createElement('div');
    wrap.className = 'msg ' + role;
    wrap.id        = id;
    wrap.innerHTML = `
      <div class="msg-avatar">${avatar}</div>
      <div class="msg-body"><div class="msg-bubble"></div></div>`;
    return wrap;
}

function renderBubble(el, text) {
    // Simple markdown rendering — no external dependency.
    let html = escHtml(text)
        .replace(/```([\s\S]*?)```/g, '<pre><code>$1</code></pre>')
        .replace(/`([^`\n]+)`/g, '<code>$1</code>')
        .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
        .replace(/\*(.+?)\*/g, '<em>$1</em>')
        .replace(/^#{1,3} (.+)$/gm, (_, h) => `<strong class="md-h">${h}</strong>`)
        .replace(/^- (.+)$/gm, (_, item) => `<li>${item}</li>`)
        .split('\n\n')
        .map(p => {
            if (p.includes('<li>')) return '<ul>' + p + '</ul>';
            if (p.startsWith('<pre>') || p.startsWith('<ul>')) return p;
            return '<p>' + p.replace(/\n/g, '<br>') + '</p>';
        })
        .join('');
    el.innerHTML = html;
    scrollToBottom();
}

function appendTyping() {
    const mid  = 'typing-' + generateId();
    const wrap = document.createElement('div');
    wrap.className = 'msg agent';
    wrap.id        = mid;
    wrap.innerHTML = `
      <div class="msg-avatar">🦅</div>
      <div class="msg-body"><div class="typing-dots"><span></span><span></span><span></span></div></div>`;
    document.getElementById('messages').appendChild(wrap);
    scrollToBottom();
    return mid;
}

function removeTyping(id) {
    const el = document.getElementById(id);
    if (el) el.remove();
}

function appendToolCall(msgWrap, name, done) {
    const tc = document.createElement('div');
    tc.className = 'tool-call' + (done ? ' done' : '');
    tc.id        = 'tc-' + name.replace(/\W/g, '_');
    tc.innerHTML = `⚙ <span>${escHtml(name)}</span>`;
    const body   = msgWrap.querySelector('.msg-body');
    if (body) body.insertBefore(tc, body.firstChild);
}

function markToolCallDone(msgId, name) {
    const tc = document.getElementById('tc-' + name.replace(/\W/g, '_'));
    if (tc) tc.classList.add('done');
}

function appendSources(msgEl, sources) {
    // Deduplicate by repo_id.
    const seen  = new Set();
    const dedup = sources.filter(s => {
        const key = s.repo_id + (s.file_path || '') + (s.heading || '');
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
    });
    if (dedup.length === 0) return;

    const div = document.createElement('div');
    div.className = 'sources-bar';
    div.innerHTML = '<span class="sources-label">Sources:</span> ' +
        dedup.slice(0, 6).map(s => {
            const label = s.file_path
                ? s.file_path.split('/').pop()
                : (s.heading || shortRepoName(s.repo_id));
            return `<span class="source-pill" title="${escHtml(s.repo_id + (s.file_path ? ' · ' + s.file_path : ''))}">${escHtml(label)}</span>`;
        }).join('');

    const body = msgEl.querySelector('.msg-body');
    if (body) body.appendChild(div);
}

function shortRepoName(id) {
    const parts = (id || '').split('/');
    return parts[parts.length - 1] || id;
}

function setSendDisabled(v) {
    document.getElementById('send-btn').disabled = v;
}

function scrollToBottom() {
    const msgs = document.getElementById('messages');
    msgs.scrollTop = msgs.scrollHeight;
}

// ── Session actions ───────────────────────────────────────────
function clearChat() {
    const msgs = document.getElementById('messages');
    msgs.innerHTML = '';
    const es = document.createElement('div');
    es.className = 'empty-state';
    es.id        = 'empty-state';
    es.innerHTML = `
      <div class="empty-icon">🦅</div>
      <h2>Ask about your repositories</h2>
      <p>Select a knowledge base in the sidebar and ask anything about your indexed repos.</p>`;
    msgs.appendChild(es);
}

function newSession() {
    state.sessionId = generateId();
    saveSession();
    clearChat();
    showToast('New session started.');
}

function saveSession() {
    localStorage.setItem('ac_session', state.sessionId);
    const el = document.getElementById('session-hint');
    if (el) el.textContent = 'Session: ' + state.sessionId.slice(0, 8) + '…';
}

// ── Toast ─────────────────────────────────────────────────────
function showToast(msg, type) {
    const area = document.getElementById('toast-area');
    const t    = document.createElement('div');
    t.className  = 'toast' + (type === 'error' ? ' error' : '');
    t.textContent = msg;
    area.appendChild(t);
    setTimeout(() => t.remove(), 4000);
}

// ── Utilities ─────────────────────────────────────────────────
function generateId() {
    return Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
}

function escHtml(str) {
    if (!str) return '';
    return str
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

async function apiFetch(path, opts = {}) {
    const headers = { ...(opts.headers || {}) };
    if (state.token) headers['Authorization'] = 'Bearer ' + state.token;
    return fetch(path, { ...opts, headers });
}
