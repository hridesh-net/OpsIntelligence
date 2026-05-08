-- 0002_github_app.sql — GitHub App installation registry for Postgres.

CREATE TABLE github_app_installations (
    id                  BIGINT       PRIMARY KEY,   -- GitHub installation_id
    account_login       TEXT         NOT NULL,
    account_type        TEXT         NOT NULL DEFAULT '',
    ops_endpoint        TEXT         NOT NULL DEFAULT '',
    ops_webhook_secret  TEXT         NOT NULL DEFAULT '',
    active              BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_github_app_installations_login ON github_app_installations(account_login);

CREATE TABLE github_app_connect_tokens (
    installation_id  BIGINT       PRIMARY KEY,
    token            TEXT         NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_github_app_connect_tokens_token ON github_app_connect_tokens(token);
