-- 0004_attachments.sql
--
-- File attachments for kanban cards. Mirrors kanbots.dev's "drag a file
-- onto a card" affordance: the operator uploads a file via the gateway,
-- we persist it under <state_dir>/workspace/kanban/attachments/<card_id>/,
-- and store the metadata here so the UI can list / download / delete it
-- without scanning the filesystem.

CREATE TABLE IF NOT EXISTS card_attachments (
    id            TEXT PRIMARY KEY,
    card_id       TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    filename      TEXT NOT NULL,
    mime_type     TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes    INTEGER NOT NULL,
    path          TEXT NOT NULL,
    created_by    TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_card_attachments_card_id
    ON card_attachments(card_id);
