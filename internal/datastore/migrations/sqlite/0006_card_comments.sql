-- 0006_card_comments.sql
--
-- Per-card threaded comments. kanbots.dev surfaces a Conversation tab
-- on each card; this is the persistence layer behind it. author_kind
-- distinguishes "user" (gateway user UUID) from "agent" (board_agent
-- UUID) so the UI can render a different avatar per author family.
-- `mentions` is a CSV of resolved IDs so @mention notifications and
-- webhooks can dispatch without re-parsing the body.

CREATE TABLE IF NOT EXISTS card_comments (
    id           TEXT PRIMARY KEY,
    board_id     TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    card_id      TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    author_id    TEXT NOT NULL,
    author_kind  TEXT NOT NULL,                                       -- 'user' | 'agent'
    body         TEXT NOT NULL,
    mentions     TEXT,                                                -- CSV of resolved IDs
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    edited_at    TIMESTAMP,
    reply_to     TEXT REFERENCES card_comments(id) ON DELETE SET NULL,
    deleted_at   TIMESTAMP                                            -- soft-delete; row stays for thread structure
);

CREATE INDEX IF NOT EXISTS idx_card_comments_card
    ON card_comments(card_id, created_at);
CREATE INDEX IF NOT EXISTS idx_card_comments_author
    ON card_comments(author_id);
