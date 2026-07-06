-- Add target_user_ids column for multi-user announcement targeting.
-- Stores a JSON array of user IDs (e.g. "[1,3,5]"). NULL or empty = use
-- the existing target_user_id column (0 = all users, backward compatible).
ALTER TABLE announcements ADD COLUMN target_user_ids TEXT DEFAULT NULL;
