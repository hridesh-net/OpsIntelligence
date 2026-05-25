-- 0003_kanban.sql — Kanban board system for SQLite.
--
-- Scope: boards, columns, cards, agent runs, events, decisions,
-- board agents, and personas.

PRAGMA foreign_keys = ON;

-- Boards: one kanban board per workspace / repo
CREATE TABLE boards (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    team_id     TEXT,
    repo_url    TEXT,
    repo_path   TEXT,
    mode        TEXT NOT NULL DEFAULT 'local',
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_boards_team ON boards(team_id);

-- Board columns: customizable kanban columns per board
CREATE TABLE board_columns (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,
    color       TEXT,
    wip_limit   INTEGER,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_board_columns_board ON board_columns(board_id);

-- Board cards: issues / tasks on the board
CREATE TABLE board_cards (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    column_id       TEXT NOT NULL REFERENCES board_columns(id),
    issue_number    INTEGER,
    title           TEXT NOT NULL,
    description     TEXT,
    card_type       TEXT NOT NULL DEFAULT 'feature',
    priority        TEXT NOT NULL DEFAULT 'p2',
    effort          TEXT,
    status          TEXT NOT NULL DEFAULT 'queued',
    assignee        TEXT,
    assignee_type   TEXT DEFAULT 'agent',
    branch          TEXT,
    worktree_path   TEXT,
    cost_usd        REAL DEFAULT 0,
    token_in        INTEGER DEFAULT 0,
    token_out       INTEGER DEFAULT 0,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME
);

CREATE INDEX idx_board_cards_board ON board_cards(board_id);
CREATE INDEX idx_board_cards_column ON board_cards(column_id);
CREATE INDEX idx_board_cards_status ON board_cards(status);

-- Card runs: each agent execution on a card
CREATE TABLE card_runs (
    id              TEXT PRIMARY KEY,
    card_id         TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    run_number      INTEGER NOT NULL DEFAULT 1,
    agent_id        TEXT NOT NULL,
    agent_type      TEXT NOT NULL DEFAULT 'go',
    model           TEXT,
    persona_id      TEXT,
    status          TEXT NOT NULL DEFAULT 'running',
    cost_usd        REAL DEFAULT 0,
    token_in        INTEGER DEFAULT 0,
    token_out       INTEGER DEFAULT 0,
    elapsed_ms      INTEGER DEFAULT 0,
    worktree_path   TEXT,
    branch          TEXT,
    base_branch     TEXT,
    result_summary  TEXT,
    error           TEXT,
    created_by      TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME
);

CREATE INDEX idx_card_runs_card ON card_runs(card_id);
CREATE INDEX idx_card_runs_status ON card_runs(status);

-- Card run events: stream of agent events
CREATE TABLE card_run_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL REFERENCES card_runs(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    phase       TEXT,
    message     TEXT,
    metadata_json TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_card_run_events_run ON card_run_events(run_id);

-- Pending decisions: when an agent asks a question
CREATE TABLE pending_decisions (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES card_runs(id) ON DELETE CASCADE,
    card_id     TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    question    TEXT NOT NULL,
    options_json TEXT NOT NULL DEFAULT '[]',
    status      TEXT NOT NULL DEFAULT 'pending',
    answer      TEXT,
    answered_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_pending_decisions_run ON pending_decisions(run_id);
CREATE INDEX idx_pending_decisions_card ON pending_decisions(card_id);
CREATE INDEX idx_pending_decisions_status ON pending_decisions(status);

-- Board agents: registered agents / providers per board
CREATE TABLE board_agents (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    agent_type  TEXT NOT NULL,
    provider_id TEXT,
    config_json TEXT NOT NULL DEFAULT '{}',
    is_default  INTEGER NOT NULL DEFAULT 0,
    is_active   INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_board_agents_board ON board_agents(board_id);

-- Personas: system prompt lenses
CREATE TABLE personas (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    icon        TEXT,
    description TEXT,
    system_prompt TEXT NOT NULL,
    is_builtin  INTEGER NOT NULL DEFAULT 0,
    created_by  TEXT REFERENCES users(id),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_personas_builtin ON personas(is_builtin);
