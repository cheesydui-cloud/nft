-- Folder-style groups: independent folders + membership via group_id.
-- Legacy free-text group_name is migrated into folders on first boot.

CREATE TABLE IF NOT EXISTS user_folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS node_repo_folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

ALTER TABLE users ADD COLUMN group_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE node_repo ADD COLUMN group_id INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_users_group_id ON users(group_id);
CREATE INDEX IF NOT EXISTS idx_node_repo_group_id ON node_repo(group_id);
