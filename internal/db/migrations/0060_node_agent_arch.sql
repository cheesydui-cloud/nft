-- Persist agent-reported GOARCH so proxy-core push can pick amd64/arm64 cache.
ALTER TABLE nodes ADD COLUMN agent_arch TEXT NOT NULL DEFAULT '';
