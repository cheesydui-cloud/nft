-- Optional Cloudflare DNS sync for node_repo entries.
-- host remains the stable rule target (IP or domain). backend_ip is the
-- A-record value pushed to CF when cf_sync=1; pure-IP rows leave these empty.
ALTER TABLE node_repo ADD COLUMN backend_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE node_repo ADD COLUMN cf_sync INTEGER NOT NULL DEFAULT 0;
ALTER TABLE node_repo ADD COLUMN cf_zone_id TEXT NOT NULL DEFAULT '';
ALTER TABLE node_repo ADD COLUMN cf_record_name TEXT NOT NULL DEFAULT '';
ALTER TABLE node_repo ADD COLUMN cf_last_sync_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE node_repo ADD COLUMN cf_last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE node_repo ADD COLUMN cf_last_ip TEXT NOT NULL DEFAULT '';
