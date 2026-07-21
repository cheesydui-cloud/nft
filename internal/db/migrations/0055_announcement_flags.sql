-- Announcement display flags: pin, color mark, and login-popup selection.
-- pinned=1 sorts above non-pinned; color is a short token (default/red/amber/blue/green);
-- login_popup=1 marks the notice shown in the user login modal (at most one active).
ALTER TABLE announcements ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE announcements ADD COLUMN color TEXT NOT NULL DEFAULT 'default';
ALTER TABLE announcements ADD COLUMN login_popup INTEGER NOT NULL DEFAULT 0;
