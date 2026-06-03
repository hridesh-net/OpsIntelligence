-- 0005_kanban_webhooks.sql
--
-- Outbound webhooks for kanban run lifecycle events. The dispatcher
-- writes every CardRunEvent into an in-process events.Bus (see
-- internal/kanban/events); a delivery worker subscribes once per
-- active run and POSTs each event to every registered webhook whose
-- `events` filter matches the event kind.
--
-- Delivery signs the body with HMAC-SHA256 keyed by `secret` and stamps
-- X-OpsIntel-Signature / X-OpsIntel-Event / X-OpsIntel-Delivery so
-- receivers can verify provenance and dedupe retries.

CREATE TABLE IF NOT EXISTS kanban_webhooks (
    id            TEXT PRIMARY KEY,
    board_id      TEXT,                                       -- NULL = all boards
    url           TEXT NOT NULL,
    secret        TEXT NOT NULL,                              -- HMAC-SHA256 signing key
    events        TEXT NOT NULL,                              -- CSV: run.started,run.completed,run.error,...
    active        INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_status   INTEGER,                                    -- last HTTP status code observed
    last_error    TEXT,
    last_delivery TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_kanban_webhooks_board
    ON kanban_webhooks(board_id);
CREATE INDEX IF NOT EXISTS idx_kanban_webhooks_active
    ON kanban_webhooks(active);
