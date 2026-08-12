-- Per-instance traffic ledgers for published proxy services (VLESS/SS/mieru/…).
-- Bytes are raw uplink/downlink as reported by the agent (core stats API or
-- equivalent). last_* snapshot agent-side cumulative counters for delta math
-- on the panel is not needed — agent already sends deltas.

ALTER TABLE proxy_service_instances ADD COLUMN traffic_up_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_service_instances ADD COLUMN traffic_down_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE proxy_service_instances ADD COLUMN traffic_updated_at INTEGER NOT NULL DEFAULT 0;
