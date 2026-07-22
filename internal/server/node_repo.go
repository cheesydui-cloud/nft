package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
		GroupName string `json:"group_name"`
		GroupID   int64  `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Host == "" || body.Port == 0 {
		jsonErr(w, http.StatusBadRequest, "name, host and port are required")
		return
	}
	// group_id wins when provided; otherwise resolve from group_name.
	groupName := strings.TrimSpace(body.GroupName)
	if body.GroupID > 0 {
		if f, err := db.GetNodeRepoFolder(s.DB, body.GroupID); err == nil {
			groupName = f.Name
		}
	}
	n, err := db.CreateNodeRepoEntry(s.DB, body.Name, body.Protocol, body.Host, body.Port, body.URI, body.Remark, body.ExpiresAt, groupName)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, n)
}

// apiUpdateNodeRepoEntry updates an existing node in the repository.
// When host/port (or URI metadata) changes, every user landing exit that was
// imported from the repo at the previous endpoint — and every rule still dialing
// that endpoint — is rewritten so admin + user UIs and the data plane follow.
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
		GroupName string `json:"group_name"`
		GroupID   int64  `json:"group_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Host == "" || body.Port == 0 {
		jsonErr(w, http.StatusBadRequest, "name, host and port are required")
		return
	}
	prev, err := db.GetNodeRepoEntry(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "节点不存在")
		return
	}
	groupName := strings.TrimSpace(body.GroupName)
	if body.GroupID > 0 {
		if f, err := db.GetNodeRepoFolder(s.DB, body.GroupID); err == nil {
			groupName = f.Name
		}
	} else if body.GroupID == 0 && body.GroupName == "" {
		// Explicit ungroup when client sends group_id:0 with empty name.
		groupName = ""
	}
	if err := db.UpdateNodeRepoEntry(s.DB, id, body.Name, body.Protocol, body.Host, body.Port, body.URI, body.Remark, body.ExpiresAt, groupName); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Cascade: repo template → user landing exits (source=repo) + rules/hops.
	// Always run so name/uri/expires refresh even when host:port is unchanged.
	prop, err := db.PropagateRepoExitChange(s.DB,
		prev.Host, body.Host, prev.Port, body.Port,
		body.Name, body.Protocol, body.URI, body.ExpiresAt,
	)
	if err != nil {
		// Repo row already saved; surface cascade failure so the admin can retry.
		jsonErr(w, http.StatusInternalServerError, "仓库已保存，但同步用户/规则失败: "+err.Error())
		return
	}
	if len(prop.NodeIDs) > 0 {
		s.redispatchNodes(prop.NodeIDs)
	}

	jsonOK(w, map[string]any{
		"ok":               true,
		"endpoint_changed": prop.EndpointChanged,
		"exits_updated":    prop.ExitsUpdated,
		"rules_updated":    prop.RulesUpdated,
	})
}

// apiBatchSetNodeRepoGroup moves many repo entries into a folder.
// Prefer group_id; group_name creates/ensures a folder for legacy clients.
func (s *Server) apiBatchSetNodeRepoGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs       []int64 `json:"ids"`
		GroupID   *int64  `json:"group_id"`
		GroupName string  `json:"group_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		jsonErr(w, http.StatusBadRequest, "no nodes selected")
		return
	}
	if body.GroupID != nil {
		if err := db.SetNodeRepoFoldersBatch(s.DB, body.IDs, *body.GroupID); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if err := db.SetNodeRepoGroupsBatch(s.DB, body.IDs, strings.TrimSpace(body.GroupName)); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true, "count": len(body.IDs)})
}

// --- Node-repo folders ---

func (s *Server) apiListNodeRepoFolders(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListNodeRepoFolders(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*db.Folder{}
	}
	var ungrouped int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM node_repo WHERE group_id=0`).Scan(&ungrouped)
	jsonOK(w, map[string]any{"folders": list, "ungrouped": ungrouped})
}

func (s *Server) apiCreateNodeRepoFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	f, err := db.CreateNodeRepoFolder(s.DB, body.Name)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, f)
}

func (s *Server) apiRenameNodeRepoFolder(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := db.RenameNodeRepoFolder(s.DB, id, body.Name); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

func (s *Server) apiDeleteNodeRepoFolder(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := db.DeleteNodeRepoFolder(s.DB, id); err != nil {
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
		s.goAsync(func() { s.redispatchUserExit(uid, k.Host, k.Port) })
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
