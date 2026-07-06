-- Add expires_at to user_landing_exits for per-node expiry.
-- NULL = never expires (default). Set to a unix timestamp to
-- auto-disable the landing exit when that time passes.
ALTER TABLE user_landing_exits ADD COLUMN expires_at INTEGER;
