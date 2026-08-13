-- Sticky per-family relay disable. When set, the control plane keeps that
-- family's relay_host empty and fillNodeRelayHosts will not re-seed it from
-- agent probes (so a dual-stack node can be forced to v4-only or v6-only).
ALTER TABLE nodes ADD COLUMN relay_v4_disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN relay_v6_disabled INTEGER NOT NULL DEFAULT 0;
