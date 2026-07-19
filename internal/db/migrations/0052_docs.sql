-- Admin-editable usage docs shown to logged-in users.
-- content is Markdown (images via /api/docs/assets/...).
-- published=0 draft (admin only); published=1 visible to users.
CREATE TABLE IF NOT EXISTS docs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  published INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_docs_sort ON docs(sort_order, id);
CREATE INDEX IF NOT EXISTS idx_docs_published ON docs(published, sort_order, id);
