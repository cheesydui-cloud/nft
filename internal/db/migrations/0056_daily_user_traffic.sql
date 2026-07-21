-- Per-user per-day raw traffic (final-hop up+down), Asia/Shanghai calendar day.
-- Independent of billing_rate and admin traffic resets; rate is applied at display time.
CREATE TABLE IF NOT EXISTS daily_user_traffic (
    day        TEXT    NOT NULL,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    raw_bytes  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, user_id)
);
