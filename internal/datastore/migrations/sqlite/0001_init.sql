-- 0001_init.sql — ops-plane baseline schema for SQLite.
--
-- Scope: users, roles+permissions, api keys, sessions, audit log,
-- task history+events, oidc state. Agent memory has its own schema
-- and DSN under internal/memory — do not add memory tables here.

PRAGMA foreign_keys = ON;

CREATE TABLE users (
    id              TEXT PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    email           TEXT,
    display_name    TEXT,
    password_hash   TEXT,
    totp_secret     TEXT,
    status          TEXT NOT NULL DEFAULT 'active',
    oidc_issuer     TEXT,
    oidc_subject    TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at   DATETIME
);

CREATE UNIQUE INDEX idx_users_oidc ON users(oidc_issuer, oidc_subject)
    WHERE oidc_subject IS NOT NULL;

CREATE TABLE roles (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT,
    is_builtin      INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE role_permissions (
    role_id         TEXT NOT NULL,
    permission_key  TEXT NOT NULL,
    PRIMARY KEY (role_id, permission_key),
    FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
);

CREATE TABLE user_roles (
    user_id         TEXT NOT NULL,
    role_id         TEXT NOT NULL,
    assigned_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, role_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
);

CREATE TABLE api_keys (
    id              TEXT PRIMARY KEY,
    key_id          TEXT NOT NULL UNIQUE,
    hash            TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    name            TEXT NOT NULL,
    scopes          TEXT NOT NULL DEFAULT '[]',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at    DATETIME,
    expires_at      DATETIME,
    revoked_at      DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_api_keys_user ON api_keys(user_id);

CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME NOT NULL,
    last_seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_agent      TEXT,
    remote_addr     TEXT,
    revoked_at      DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE audit_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT,
    action          TEXT NOT NULL,
    resource_type   TEXT,
    resource_id     TEXT,
    metadata_json   TEXT,
    remote_addr     TEXT,
    user_agent      TEXT,
    success         INTEGER NOT NULL DEFAULT 1,
    error_message   TEXT
);

CREATE INDEX idx_audit_timestamp ON audit_log(timestamp);
CREATE INDEX idx_audit_actor ON audit_log(actor_id);
CREATE INDEX idx_audit_action ON audit_log(action);

CREATE TABLE task_history (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL UNIQUE,
    session_id      TEXT,
    subagent_id     TEXT,
    goal            TEXT,
    prompt          TEXT,
    response        TEXT,
    status          TEXT NOT NULL,
    iterations      INTEGER NOT NULL DEFAULT 0,
    error           TEXT,
    actor_id        TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_task_history_status ON task_history(status);
CREATE INDEX idx_task_history_actor ON task_history(actor_id);
CREATE INDEX idx_task_history_created ON task_history(created_at);

CREATE TABLE task_history_events (
    task_id         TEXT NOT NULL,
    event_index     INTEGER NOT NULL,
    kind            TEXT NOT NULL,
    phase           TEXT,
    source          TEXT,
    message         TEXT NOT NULL,
    metadata_json   TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, event_index),
    FOREIGN KEY (task_id) REFERENCES task_history(task_id) ON DELETE CASCADE
);

CREATE INDEX idx_task_events_created ON task_history_events(created_at);

CREATE TABLE oidc_state (
    state           TEXT PRIMARY KEY,
    nonce           TEXT NOT NULL,
    pkce_verifier   TEXT,
    redirect_after  TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME NOT NULL
);

CREATE INDEX idx_oidc_state_expiry ON oidc_state(expires_at);
