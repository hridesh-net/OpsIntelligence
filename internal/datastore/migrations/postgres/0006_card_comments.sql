-- 0006_card_comments.sql — postgres flavour of the sqlite migration.
CREATE TABLE IF NOT EXISTS card_comments (
    id           TEXT PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    card_id      TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    author_id    TEXT NOT NULL,
    author_kind  TEXT NOT NULL,
    body         TEXT NOT NULL,
    mentions     TEXT,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    edited_at    TIMESTAMP WITH TIME ZONE,
    reply_to     TEXT REFERENCES card_comments(id) ON DELETE SET NULL,
    deleted_at   TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_card_comments_card
    ON card_comments(card_id, created_at);
CREATE INDEX IF NOT EXISTS idx_card_comments_author
    ON card_comments(author_id);
