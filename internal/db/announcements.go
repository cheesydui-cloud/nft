package db

import "database/sql"

// Announcement is an admin-published notice pushed to users.
type Announcement struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	TargetUserID int64  `json:"target_user_id"` // 0 = all users
	CreatedAt    int64  `json:"created_at"`
	ExpiresAt    int64  `json:"expires_at"` // 0 = never
}

// CreateAnnouncement inserts a new announcement and returns it.
func CreateAnnouncement(d *sql.DB, title, content string, targetUserID, expiresAt int64) (Announcement, error) {
	a := Announcement{Title: title, Content: content, TargetUserID: targetUserID, ExpiresAt: expiresAt, CreatedAt: now()}
	res, err := d.Exec(`INSERT INTO announcements (title, content, target_user_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		a.Title, a.Content, a.TargetUserID, a.CreatedAt, a.ExpiresAt)
	if err != nil {
		return a, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

// ListAnnouncements returns all announcements (admin view).
func ListAnnouncements(d *sql.DB) ([]Announcement, error) {
	rows, err := d.Query(`SELECT id, title, content, target_user_id, created_at, expires_at FROM announcements ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.TargetUserID, &a.CreatedAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ListAnnouncementsForUser returns announcements targeted to a specific user
// (target_user_id = 0 means all users) that have not expired.
func ListAnnouncementsForUser(d *sql.DB, userID int64) ([]Announcement, error) {
	n := now()
	rows, err := d.Query(`SELECT id, title, content, target_user_id, created_at, expires_at FROM announcements WHERE (target_user_id = 0 OR target_user_id = ?) AND (expires_at = 0 OR expires_at > ?) ORDER BY created_at DESC`,
		userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.TargetUserID, &a.CreatedAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// DeleteAnnouncement removes an announcement by ID.
func DeleteAnnouncement(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM announcements WHERE id = ?`, id)
	return err
}
