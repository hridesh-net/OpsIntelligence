-- 0005_kanban_webhooks.sql — postgres flavour of the sqlite migration.
CREATE TABLE IF NOT EXISTS kanban_webhooks (
    id            TEXT PRIMARY KEY,
    board_id      TEXT,
    url           TEXT NOT NULL,
    secret        TEXT NOT NULL,
    events        TEXT NOT NULL,
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    last_status   INTEGER,
    last_error    TEXT,
    last_delivery TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_kanban_webhooks_board
    ON kanban_webhooks(board_id);
CREATE INDEX IF NOT EXISTS idx_kanban_webhooks_active
    ON kanban_webhooks(active);
