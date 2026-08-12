-- Per-user grants for proxy services (protocol-level), independent of whole-node
-- grants. Selecting only VLESS on a multi-protocol node must not expose Naive/SS/etc.
--
-- Backfill: existing user_nodes that host proxy instances get every co-located
-- service so pre-upgrade behaviour is preserved until admins re-scope grants.
CREATE TABLE IF NOT EXISTS user_proxy_services (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES proxy_services(id) ON DELETE CASCADE,
  granted_at INTEGER NOT NULL,
  PRIMARY KEY(user_id, service_id)
);

CREATE INDEX IF NOT EXISTS idx_user_proxy_services_service
  ON user_proxy_services(service_id);

INSERT OR IGNORE INTO user_proxy_services (user_id, service_id, granted_at)
SELECT DISTINCT g.user_id, i.service_id, g.granted_at
FROM user_nodes g
JOIN proxy_service_instances i ON i.node_id = g.node_id
WHERE i.node_id > 0 AND i.service_id > 0;
