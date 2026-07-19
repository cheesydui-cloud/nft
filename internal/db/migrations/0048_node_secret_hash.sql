-- Store node secrets hashed at rest (SHA-256), matching session/API tokens.
-- secret_hashed=0 marks a legacy plaintext row that the Go backfill
-- (hashLegacyNodeSecrets) will hash in place on next Open, preserving each
-- secret so already-connected agents keep authenticating. New nodes are
-- inserted with secret_hashed=1 and an already-hashed value.
ALTER TABLE nodes ADD COLUMN secret_hashed INTEGER NOT NULL DEFAULT 0;
