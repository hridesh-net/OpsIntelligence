-- 0002_github_app.sql — GitHub App installation registry for SQLite.

CREATE TABLE github_app_installations (
    id                  INTEGER  PRIMARY KEY,   -- GitHub installation_id (numeric)
    account_login       TEXT     NOT NULL,       -- org or user slug
    account_type        TEXT     NOT NULL DEFAULT '',
    ops_endpoint        TEXT     NOT NULL DEFAULT '',
    ops_webhook_secret  TEXT     NOT NULL DEFAULT '',
    active              INTEGER  NOT NULL DEFAULT 1,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_github_app_installations_login ON github_app_installations(account_login);

-- Connect tokens: one-time secrets used by the client's OpsIntelligence
-- to authenticate its outbound WebSocket to the relay hub.
CREATE TABLE github_app_connect_tokens (
    installation_id  INTEGER  PRIMARY KEY,
    token            TEXT     NOT NULL UNIQUE,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_github_app_connect_tokens_token ON github_app_connect_tokens(token);
