-- Cards created via the API before v1.0.87 were inserted with an explicit
-- empty status, bypassing the column DEFAULT 'queued'. Backfill them.
UPDATE board_cards SET status = 'queued' WHERE status = '';
