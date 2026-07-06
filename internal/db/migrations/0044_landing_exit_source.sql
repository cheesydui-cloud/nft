-- Add source column to distinguish auto-synced landing exits from repo-assigned ones.
-- 'auto' = managed by subscription/manual URI sync
-- 'repo' = imported from the node pool (never swept by sync)
-- ''     = legacy rows, treated as 'auto'
ALTER TABLE user_landing_exits ADD COLUMN source TEXT DEFAULT '';
