-- Optional Cloudflare DNS sync for line nodes (relay entry).
-- relay_host remains the stable client-facing target (domain preferred when CF on).
-- backend_ip is the A-record value pushed to CF when cf_sync=1.
ALTER TABLE nodes ADD COLUMN backend_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN cf_sync INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN cf_zone_id TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN cf_record_name TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN cf_last_sync_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN cf_last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN cf_last_ip TEXT NOT NULL DEFAULT '';
