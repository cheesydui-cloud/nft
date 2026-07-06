-- Announcements table: admin-published notices pushed to users.
-- target_user_id = 0 means "all users"; >0 means a specific user.
-- expires_at = 0 means never expires; otherwise unix timestamp.
CREATE TABLE IF NOT EXISTS announcements (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    target_user_id INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL DEFAULT 0
);
