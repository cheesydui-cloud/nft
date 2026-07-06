-- Add expires_at to node_repo so admin can set expiry on pool entries.
-- 0 = never expires (default). When assigned to a user, this value is
-- carried over to the user_landing_exits row.
ALTER TABLE node_repo ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
