-- Per-day raw (actual) traffic ledger for operator dashboards.
-- Independent of billing multipliers and user traffic resets.
CREATE TABLE IF NOT EXISTS daily_node_raw_traffic (
    day       TEXT    NOT NULL,
    node_id   INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    raw_bytes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, node_id)
);
