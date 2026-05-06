-- Composite index for the default unfiltered sort
-- (`ORDER BY datetime DESC, id ASC`). Lets the planner stream rows in
-- order when no collection filter is present; idx_items_datetime alone
-- doesn't cover the id tiebreak.
CREATE INDEX IF NOT EXISTS idx_items_datetime_id
    ON items(datetime DESC, id);
