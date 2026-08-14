-- Per-user inbound credentials for published proxy services.
-- The service config_json stays the admin template; live inbound clients are
-- rebuilt from this table for users who still have an enabled rule.
CREATE TABLE IF NOT EXISTS user_proxy_service_creds (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  service_id INTEGER NOT NULL REFERENCES proxy_services(id) ON DELETE CASCADE,
  uuid TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  password TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY(user_id, service_id)
);

CREATE INDEX IF NOT EXISTS idx_user_proxy_service_creds_service
  ON user_proxy_service_creds(service_id);
