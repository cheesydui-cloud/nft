-- Proxy services: multi-node protocol publish (VLESS / Shadowsocks / mieru),
-- similar to Weir / 3X-UI inbounds, with optional sync into node_repo.

CREATE TABLE IF NOT EXISTS proxy_services (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL,           -- vless | shadowsocks | mieru
  core TEXT NOT NULL,              -- xray | sing-box | mieru
  config_json TEXT NOT NULL DEFAULT '{}',
  sub_visible INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'draft', -- draft | ready | partial | error
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS proxy_service_instances (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id INTEGER NOT NULL REFERENCES proxy_services(id) ON DELETE CASCADE,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  listen_port INTEGER NOT NULL DEFAULT 0,
  share_host TEXT NOT NULL DEFAULT '',
  uri TEXT NOT NULL DEFAULT '',
  deploy_status TEXT NOT NULL DEFAULT 'pending', -- pending | ready | error | offline
  last_error TEXT NOT NULL DEFAULT '',
  core_version TEXT NOT NULL DEFAULT '',
  synced_repo_id INTEGER NOT NULL DEFAULT 0,    -- node_repo.id when synced; 0 = not
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(service_id, node_id)
);

CREATE INDEX IF NOT EXISTS idx_proxy_instances_service ON proxy_service_instances(service_id);
CREATE INDEX IF NOT EXISTS idx_proxy_instances_node ON proxy_service_instances(node_id);

-- Cores detected on the agent (JSON array), reported via hello.
ALTER TABLE nodes ADD COLUMN cores_json TEXT NOT NULL DEFAULT '[]';
