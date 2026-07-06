package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"nft/internal/db"
)

// apiListAnnouncements returns all announcements (admin only).
func (s *Server) apiListAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListAnnouncements(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Announcement{}
	}
	jsonOK(w, map[string]any{"announcements": list})
}

// apiCreateAnnouncement creates a new announcement.
// Body: { title, content, target_user_id (0=all), target_user_ids ([1,3,5]), expires_at (0=never) }
func (s *Server) apiCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string  `json:"title"`
		Content       string  `json:"content"`
		TargetUserID  int64   `json:"target_user_id"`
		TargetUserIDs []int64 `json:"target_user_ids"`
		ExpiresAt     int64   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Title == "" || body.Content == "" {
		jsonErr(w, http.StatusBadRequest, "title and content are required")
		return
	}
	// Serialize target_user_ids to JSON string if provided
	var targetUserIDs string
	if len(body.TargetUserIDs) > 0 {
		b, _ := json.Marshal(body.TargetUserIDs)
		targetUserIDs = string(b)
	}
	a, err := db.CreateAnnouncement(s.DB, body.Title, body.Content, body.TargetUserID, body.ExpiresAt, targetUserIDs)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, a)
}

// apiDeleteAnnouncement deletes an announcement by ID.
func (s *Server) apiDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := db.DeleteAnnouncement(s.DB, id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// apiMyAnnouncements returns announcements targeted to the current user.
func (s *Server) apiMyAnnouncements(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	list, err := db.ListAnnouncementsForUser(s.DB, u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Announcement{}
	}
	jsonOK(w, map[string]any{"announcements": list})
}
