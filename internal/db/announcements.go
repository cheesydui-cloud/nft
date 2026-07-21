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
	TargetUserID  int64  `json:"target_user_id"`            // 0 = all users (legacy single-target)
	TargetUserIDs string `json:"target_user_ids,omitempty"` // JSON array of user IDs, e.g. "[1,3,5]"
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"` // 0 = never
	// Pinned sorts the notice above non-pinned ones in list views.
	Pinned int `json:"pinned"`
	// Color is a short token used by the UI for emphasis (default/red/amber/blue/green).
	Color string `json:"color"`
	// LoginPopup marks the single notice shown as a modal on every user login.
	LoginPopup int `json:"login_popup"`
}

// NormalizeAnnouncementColor maps free-form color input to a known token.
func NormalizeAnnouncementColor(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "red", "amber", "blue", "green":
		return strings.ToLower(strings.TrimSpace(c))
	default:
		return "default"
	}
}

// CreateAnnouncement inserts a new announcement and returns it.
// targetUserIDs is a JSON array string (e.g. "[1,3,5]"), empty string means use targetUserID.
func CreateAnnouncement(d *sql.DB, title, content string, targetUserID, expiresAt int64, targetUserIDs string, pinned int, color string, loginPopup int) (Announcement, error) {
	color = NormalizeAnnouncementColor(color)
	if pinned != 0 {
		pinned = 1
	}
	if loginPopup != 0 {
		loginPopup = 1
	}
	a := Announcement{
		Title: title, Content: content, TargetUserID: targetUserID, TargetUserIDs: targetUserIDs,
		ExpiresAt: expiresAt, CreatedAt: now(), Pinned: pinned, Color: color, LoginPopup: loginPopup,
	}
	if loginPopup == 1 {
		// Only one login-popup notice is active at a time.
		if _, err := d.Exec(`UPDATE announcements SET login_popup = 0 WHERE login_popup != 0`); err != nil {
			return a, err
		}
	}
	res, err := d.Exec(`INSERT INTO announcements (title, content, target_user_id, target_user_ids, created_at, expires_at, pinned, color, login_popup) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Title, a.Content, a.TargetUserID, nullIfEmpty(a.TargetUserIDs), a.CreatedAt, a.ExpiresAt, a.Pinned, a.Color, a.LoginPopup)
	if err != nil {
		return a, err
	}
	a.ID, _ = res.LastInsertId()
	return a, nil
}

// UpdateAnnouncement rewrites mutable fields on an existing notice.
func UpdateAnnouncement(d *sql.DB, id int64, title, content string, targetUserID, expiresAt int64, targetUserIDs string, pinned int, color string, loginPopup int) (Announcement, error) {
	color = NormalizeAnnouncementColor(color)
	if pinned != 0 {
		pinned = 1
	}
	if loginPopup != 0 {
		loginPopup = 1
	}
	if loginPopup == 1 {
		if _, err := d.Exec(`UPDATE announcements SET login_popup = 0 WHERE login_popup != 0 AND id != ?`, id); err != nil {
			return Announcement{}, err
		}
	}
	_, err := d.Exec(`UPDATE announcements SET title=?, content=?, target_user_id=?, target_user_ids=?, expires_at=?, pinned=?, color=?, login_popup=? WHERE id=?`,
		title, content, targetUserID, nullIfEmpty(targetUserIDs), expiresAt, pinned, color, loginPopup, id)
	if err != nil {
		return Announcement{}, err
	}
	return GetAnnouncement(d, id)
}

// GetAnnouncement loads one notice by id.
func GetAnnouncement(d *sql.DB, id int64) (Announcement, error) {
	row := d.QueryRow(`SELECT id, title, content, target_user_id, target_user_ids, created_at, expires_at, pinned, color, login_popup FROM announcements WHERE id = ?`, id)
	var a Announcement
	if err := scanAnnouncementRow(row, &a); err != nil {
		return a, err
	}
	return a, nil
}

// SetAnnouncementLoginPopup toggles the login-popup flag; enabling one clears others.
func SetAnnouncementLoginPopup(d *sql.DB, id int64, on bool) error {
	if on {
		if _, err := d.Exec(`UPDATE announcements SET login_popup = 0 WHERE login_popup != 0`); err != nil {
			return err
		}
		_, err := d.Exec(`UPDATE announcements SET login_popup = 1 WHERE id = ?`, id)
		return err
	}
	_, err := d.Exec(`UPDATE announcements SET login_popup = 0 WHERE id = ?`, id)
	return err
}

// SetAnnouncementPinned sets the pinned flag.
func SetAnnouncementPinned(d *sql.DB, id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := d.Exec(`UPDATE announcements SET pinned = ? WHERE id = ?`, v, id)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type scannable interface {
	Scan(dest ...any) error
}

func scanAnnouncementRow(row scannable, a *Announcement) error {
	var targetUserIDs sql.NullString
	var color sql.NullString
	if err := row.Scan(&a.ID, &a.Title, &a.Content, &a.TargetUserID, &targetUserIDs, &a.CreatedAt, &a.ExpiresAt, &a.Pinned, &color, &a.LoginPopup); err != nil {
		return err
	}
	a.TargetUserIDs = targetUserIDs.String
	a.Color = NormalizeAnnouncementColor(color.String)
	return nil
}

func scanAnnouncement(rows *sql.Rows, a *Announcement) error {
	return scanAnnouncementRow(rows, a)
}

const announcementSelect = `SELECT id, title, content, target_user_id, target_user_ids, created_at, expires_at, pinned, color, login_popup FROM announcements`

// ListAnnouncements returns all announcements (admin view), pinned first.
func ListAnnouncements(d *sql.DB) ([]Announcement, error) {
	rows, err := d.Query(announcementSelect + ` ORDER BY pinned DESC, created_at DESC`)
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
//
// Results are pinned-first then newest-first.
func ListAnnouncementsForUser(d *sql.DB, userID int64) ([]Announcement, error) {
	n := now()
	rows, err := d.Query(announcementSelect+` WHERE (expires_at = 0 OR expires_at > ?) ORDER BY pinned DESC, created_at DESC`, n)
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

// GetLoginPopupForUser returns the active login-popup notice for a user, if any.
// Prefer the marked login_popup row that targets the user; otherwise nil.
func GetLoginPopupForUser(d *sql.DB, userID int64) (*Announcement, error) {
	n := now()
	rows, err := d.Query(announcementSelect+` WHERE login_popup = 1 AND (expires_at = 0 OR expires_at > ?) ORDER BY created_at DESC`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Announcement
		if err := scanAnnouncement(rows, &a); err != nil {
			return nil, err
		}
		if isTargetedToUser(a, userID) {
			return &a, nil
		}
	}
	return nil, nil
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

// TargetUserNames returns a display string for the target column.
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
