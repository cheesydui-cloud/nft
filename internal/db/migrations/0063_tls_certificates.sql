-- Central TLS certificate vault (Settings → ACME / 证书管理).
-- Proxy services may reference a row via config_json.cert_id instead of inlining PEM.

CREATE TABLE IF NOT EXISTS tls_certificates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL DEFAULT '',
  domain TEXT NOT NULL DEFAULT '',
  cert_pem TEXT NOT NULL DEFAULT '',
  key_pem TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'upload', -- acme | upload | selfsigned
  acme_enabled INTEGER NOT NULL DEFAULT 0,
  acme_provider TEXT NOT NULL DEFAULT '',
  acme_issuer TEXT NOT NULL DEFAULT '',
  not_before TEXT NOT NULL DEFAULT '',   -- RFC3339
  not_after TEXT NOT NULL DEFAULT '',    -- RFC3339
  fingerprint TEXT NOT NULL DEFAULT '', -- SHA-256 hex of leaf DER
  last_error TEXT NOT NULL DEFAULT '',
  last_renew_at TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tls_certificates_domain ON tls_certificates(domain);
CREATE INDEX IF NOT EXISTS idx_tls_certificates_not_after ON tls_certificates(not_after);
