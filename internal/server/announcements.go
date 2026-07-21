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
// Body: { title, content, target_user_id (0=all), target_user_ids ([1,3,5]),
// expires_at (0=never), pinned, color, login_popup }
func (s *Server) apiCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string  `json:"title"`
		Content       string  `json:"content"`
		TargetUserID  int64   `json:"target_user_id"`
		TargetUserIDs []int64 `json:"target_user_ids"`
		ExpiresAt     int64   `json:"expires_at"`
		Pinned        bool    `json:"pinned"`
		Color         string  `json:"color"`
		LoginPopup    bool    `json:"login_popup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Title == "" || body.Content == "" {
		jsonErr(w, http.StatusBadRequest, "title and content are required")
		return
	}
	var targetUserIDs string
	if len(body.TargetUserIDs) > 0 {
		b, _ := json.Marshal(body.TargetUserIDs)
		targetUserIDs = string(b)
	}
	pinned, loginPopup := 0, 0
	if body.Pinned {
		pinned = 1
	}
	if body.LoginPopup {
		loginPopup = 1
	}
	a, err := db.CreateAnnouncement(s.DB, body.Title, body.Content, body.TargetUserID, body.ExpiresAt, targetUserIDs, pinned, body.Color, loginPopup)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, a)
}

// apiUpdateAnnouncement updates an existing announcement (admin only).
func (s *Server) apiUpdateAnnouncement(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Title         string  `json:"title"`
		Content       string  `json:"content"`
		TargetUserID  int64   `json:"target_user_id"`
		TargetUserIDs []int64 `json:"target_user_ids"`
		ExpiresAt     int64   `json:"expires_at"`
		Pinned        bool    `json:"pinned"`
		Color         string  `json:"color"`
		LoginPopup    bool    `json:"login_popup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Title == "" || body.Content == "" {
		jsonErr(w, http.StatusBadRequest, "title and content are required")
		return
	}
	var targetUserIDs string
	if len(body.TargetUserIDs) > 0 {
		b, _ := json.Marshal(body.TargetUserIDs)
		targetUserIDs = string(b)
	}
	pinned, loginPopup := 0, 0
	if body.Pinned {
		pinned = 1
	}
	if body.LoginPopup {
		loginPopup = 1
	}
	a, err := db.UpdateAnnouncement(s.DB, id, body.Title, body.Content, body.TargetUserID, body.ExpiresAt, targetUserIDs, pinned, body.Color, loginPopup)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, a)
}

// apiPatchAnnouncementFlags toggles pin / login_popup / color without rewriting the body.
func (s *Server) apiPatchAnnouncementFlags(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Pinned     *bool   `json:"pinned"`
		LoginPopup *bool   `json:"login_popup"`
		Color      *string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cur, err := db.GetAnnouncement(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "announcement not found")
		return
	}
	pinned := cur.Pinned
	loginPopup := cur.LoginPopup
	color := cur.Color
	if body.Pinned != nil {
		if *body.Pinned {
			pinned = 1
		} else {
			pinned = 0
		}
	}
	if body.LoginPopup != nil {
		if *body.LoginPopup {
			loginPopup = 1
		} else {
			loginPopup = 0
		}
	}
	if body.Color != nil {
		color = *body.Color
	}
	a, err := db.UpdateAnnouncement(s.DB, id, cur.Title, cur.Content, cur.TargetUserID, cur.ExpiresAt, cur.TargetUserIDs, pinned, color, loginPopup)
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

// apiMyLoginAnnouncement returns the single notice marked for the login popup,
// when it targets the current (non-admin) user. Admin sessions get null.
func (s *Server) apiMyLoginAnnouncement(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	if u.Role == "admin" {
		jsonOK(w, map[string]any{"announcement": nil})
		return
	}
	a, err := db.GetLoginPopupForUser(s.DB, u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"announcement": a})
}
