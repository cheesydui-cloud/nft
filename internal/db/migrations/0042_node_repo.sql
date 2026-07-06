-- Node repository: admin-maintained pool of pre-configured proxy nodes
-- that can be assigned to users as a landing source.
CREATE TABLE IF NOT EXISTS node_repo (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    uri TEXT NOT NULL DEFAULT '',
    remark TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
