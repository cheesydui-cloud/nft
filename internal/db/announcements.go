package db

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// Announcement is an admin-published notice pushed to users.
type Announcement struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	TargetUserID  int64  `json:"target_user_id"`           // 0 = all users (legacy single-target)
	TargetUserIDs string `json:"target_user_ids,omitempty"` // JSON array of user IDs, e.g. "[1,3,5]"
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"` // 0 = never
}

// CreateAnnouncement inserts a new announcement and returns it.
// targetUserIDs is a JSON array string (e.g. "[1,3,5]"), empty string means use targetUserID.
func CreateAnnouncement(d *sql.DB, title, content string, targetUserID, expiresAt int64, targetUserIDs string) (Announcement, error) {
	a := Announcement{Title: title, Content: content, TargetUserID: targetUserID, TargetUserIDs: targetUserIDs, ExpiresAt: expiresAt, CreatedAt: now()}
	res, err := d.Exec(`INSERT INTO announcements (title, content, target_user_id, target_user_ids, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.Title, a.Content, a.TargetUserID, a.TargetUserIDs, a.CreatedAt, a.ExpiresAt)
	if err != nil {
		return a, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

func scanAnnouncement(rows *sql.Rows, a *Announcement) error {
	var targetUserIDs sql.NullString
	if err := rows.Scan(&a.ID, &a.Title, &a.Content, &a.TargetUserID, &targetUserIDs, &a.CreatedAt, &a.ExpiresAt); err != nil {
		return err
	}
	a.TargetUserIDs = targetUserIDs.String
	return nil
}

// ListAnnouncements returns all announcements (admin view).
func ListAnnouncements(d *sql.DB) ([]Announcement, error) {
	rows, err := d.Query(`SELECT id, title, content, target_user_id, target_user_ids, created_at, expires_at FROM announcements ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := scanAnnouncement(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ListAnnouncementsForUser returns announcements targeted to a specific user
// that have not expired. A user sees an announcement if:
//   - target_user_ids is non-empty and contains the user's ID, OR
//   - target_user_ids is empty and target_user_id = 0 (all users), OR
//   - target_user_ids is empty and target_user_id = the user's ID
func ListAnnouncementsForUser(d *sql.DB, userID int64) ([]Announcement, error) {
	n := now()
	rows, err := d.Query(`SELECT id, title, content, target_user_id, target_user_ids, created_at, expires_at FROM announcements WHERE (expires_at = 0 OR expires_at > ?) ORDER BY created_at DESC`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		if err := scanAnnouncement(rows, &a); err != nil {
			return nil, err
		}
		if isTargetedToUser(a, userID) {
			out = append(out, a)
		}
	}
	return out, nil
}

// isTargetedToUser checks if an announcement targets a specific user.
func isTargetedToUser(a Announcement, userID int64) bool {
	// Multi-user targeting takes precedence
	if a.TargetUserIDs != "" {
		var ids []int64
		if err := json.Unmarshal([]byte(a.TargetUserIDs), &ids); err != nil {
			return false
		}
		for _, id := range ids {
			if id == userID {
				return true
			}
		}
		return false
	}
	// Legacy single-user targeting
	return a.TargetUserID == 0 || a.TargetUserID == userID
}

// targetUserNames returns a display string for the target column.
func TargetUserNames(a Announcement, userNames map[int64]string) string {
	if a.TargetUserIDs != "" {
		var ids []int64
		if err := json.Unmarshal([]byte(a.TargetUserIDs), &ids); err == nil && len(ids) > 0 {
			parts := make([]string, 0, len(ids))
			for _, id := range ids {
				if name, ok := userNames[id]; ok {
					parts = append(parts, name)
				} else {
					parts = append(parts, "用户#"+itoa(id))
				}
			}
			return strings.Join(parts, ", ")
		}
	}
	if a.TargetUserID == 0 {
		return "所有用户"
	}
	if name, ok := userNames[a.TargetUserID]; ok {
		return name
	}
	return "用户#" + itoa(a.TargetUserID)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// DeleteAnnouncement removes an announcement by ID.
func DeleteAnnouncement(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM announcements WHERE id = ?`, id)
	return err
}
