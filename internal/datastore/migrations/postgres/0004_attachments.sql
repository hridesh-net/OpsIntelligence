-- 0004_attachments.sql — postgres flavour of the sqlite migration.
CREATE TABLE IF NOT EXISTS card_attachments (
    id            TEXT PRIMARY KEY,
    card_id       TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    filename      TEXT NOT NULL,
    mime_type     TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes    BIGINT NOT NULL,
    path          TEXT NOT NULL,
    created_by    TEXT,
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_card_attachments_card_id
    ON card_attachments(card_id);
