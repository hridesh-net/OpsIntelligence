-- 0007_card_relationships.sql
--
-- Directed edges between board cards (kanbots.dev-style dependency
-- graph). `kind` is one of 'parent' | 'blocks' | 'duplicates' |
-- 'related'. The UNIQUE constraint keeps a given edge idempotent so
-- repeated POSTs don't create duplicates.
--
-- 'parent' is the only kind that rejects cycles server-side (a card
-- can't be its own ancestor). 'blocks' edges are enforced on
-- move-to-done unless board.config.relationship_rules.enforce_blocks
-- is explicitly false.

CREATE TABLE IF NOT EXISTS card_relationships (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    src_card_id TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    dst_card_id TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by  TEXT,
    UNIQUE(src_card_id, dst_card_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_card_relationships_src
    ON card_relationships(src_card_id);
CREATE INDEX IF NOT EXISTS idx_card_relationships_dst
    ON card_relationships(dst_card_id);
