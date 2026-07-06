package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"nft/internal/db"
)

// apiListNodeRepo returns all nodes in the repository (admin only).
func (s *Server) apiListNodeRepo(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListNodeRepo(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.NodeRepoEntry{}
	}
	jsonOK(w, map[string]any{"nodes": list})
}

// apiCreateNodeRepoEntry creates a new node in the repository.
func (s *Server) apiCreateNodeRepoEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Protocol  string `json:"protocol"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		URI       string `json:"uri"`
		Remark    string `json:"remark"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Host == "" || body.Port == 0 {
		jsonErr(w, http.StatusBadRequest, "name, host and port are required")
		return
	}
	n, err := db.CreateNodeRepoEntry(s.DB, body.Name, body.Protocol, body.Host, body.Port, body.URI, body.Remark, body.ExpiresAt)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, n)
}

// apiUpdateNodeRepoEntry updates an existing node in the repository.
func (s *Server) apiUpdateNodeRepoEntry(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Name      string `json:"name"`
		Protocol  string `json:"protocol"`
		Host      string `json:"host"`
		Port      int    `json:"port"`
		URI       string `json:"uri"`
		Remark    string `json:"remark"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Host == "" || body.Port == 0 {
		jsonErr(w, http.StatusBadRequest, "name, host and port are required")
		return
	}
	if err := db.UpdateNodeRepoEntry(s.DB, id, body.Name, body.Protocol, body.Host, body.Port, body.URI, body.Remark, body.ExpiresAt); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// apiDeleteNodeRepoEntry deletes a node from the repository.
func (s *Server) apiDeleteNodeRepoEntry(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := db.DeleteNodeRepoEntry(s.DB, id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// apiAssignRepoToUser takes selected repo node IDs and assigns them to a user
// as landing exits. Uses Append (not Sync) so existing exits are preserved.
// expires_at from the repo entry is carried over to the user's landing exit.
func (s *Server) apiAssignRepoToUser(w http.ResponseWriter, r *http.Request) {
	uidStr := r.PathValue("id")
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		NodeIDs []int64 `json:"node_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.NodeIDs) == 0 {
		jsonErr(w, http.StatusBadRequest, "no nodes selected")
		return
	}
	entries, err := db.ListNodeRepoByIDs(s.DB, body.NodeIDs)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var inputs []db.LandingExitInput
	for _, e := range entries {
		inputs = append(inputs, db.LandingExitInput{
			Host:     e.Host,
			Port:     e.Port,
			Name:     e.Name,
			Protocol: e.Protocol,
			URI:      e.URI,
		})
	}
	flipped, err := db.AppendUserLandingExits(s.DB, uid, inputs)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Re-dispatch rules pointed at flipped exits (present changed → push-exclusion may change)
	for _, k := range flipped {
		go s.redispatchUserExit(uid, k.Host, k.Port)
	}
	// Carry over expires_at from repo entries to user landing exits.
	for _, e := range entries {
		if e.ExpiresAt > 0 {
			db.SetUserLandingExitExpires(s.DB, uid, e.Host, e.Port, e.ExpiresAt)
		}
	}
	// Return updated exits so frontend can refresh.
	exits, _ := db.ListUserLandingExits(s.DB, uid)
	jsonOK(w, map[string]any{"ok": true, "assigned": len(inputs), "exits": exits})
}
