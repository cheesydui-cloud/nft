-- Per-user subscription tokens for public /sub/{token} client feeds.
-- Separate from api_tokens so resetting API keys does not break Clash/SR links.

CREATE TABLE IF NOT EXISTS sub_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  token_prefix TEXT NOT NULL DEFAULT '',
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_sub_tokens_token ON sub_tokens(token);
