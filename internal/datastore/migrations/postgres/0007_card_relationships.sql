-- 0007_card_relationships.sql — postgres flavour of the sqlite migration.
CREATE TABLE IF NOT EXISTS card_relationships (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    src_card_id TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    dst_card_id TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_by  TEXT,
    UNIQUE(src_card_id, dst_card_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_card_relationships_src
    ON card_relationships(src_card_id);
CREATE INDEX IF NOT EXISTS idx_card_relationships_dst
    ON card_relationships(dst_card_id);
