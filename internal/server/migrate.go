package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nft/internal/db"
)

// normalizeMigratePanelURL validates an operator-facing panel URL for
// panel_redirect. Accepts https:// (preferred), http:// (local only), or
// bare host:port (treated as https). Returns a URL suitable for agents
// (still operator-facing; agents re-normalize to wss).
func normalizeMigratePanelURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("请填写新面板地址")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("面板地址格式无效")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http", "wss", "ws":
	default:
		return "", fmt.Errorf("面板地址须为 http(s):// 或 ws(s)://")
	}
	// Strip agents path if operator pasted a full connect URL; agents append it.
	path := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(path, "/v1/agents") {
		u.Path = strings.TrimSuffix(path, "/v1/agents")
		if u.Path == "" {
			u.Path = ""
		}
	}
	u.RawQuery = ""
	u.Fragment = ""
	// Prefer https/http base for settings.panel_url (upgrade downloads use HTTP).
	switch strings.ToLower(u.Scheme) {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	out := strings.TrimRight(u.String(), "/")
	// url.String for empty path is fine.
	return out, nil
}

// apiMigrateExport streams a VACUUM INTO snapshot of the live panel DB.
func (s *Server) apiMigrateExport(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	if s.DBPath == "" {
		// Tests / New() without path: still allow export via temp next to nothing —
		// use OS temp and VACUUM INTO from the open connection.
		tmp, err := os.CreateTemp("", "panel-migrate-*.db")
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		path := tmp.Name()
		tmp.Close()
		os.Remove(path) // VACUUM INTO requires non-existent dest
		if err := db.Backup(s.DB, path); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer os.Remove(path)
		s.serveMigrateFile(w, r, u.ID, path)
		return
	}
	dir := filepath.Join(filepath.Dir(s.DBPath), "backups")
	_ = os.MkdirAll(dir, 0o755)
	name := "panel-migrate-" + time.Now().Format("20060102-150405") + ".db"
	path := filepath.Join(dir, name)
	// Avoid collision within the same second.
	if _, err := os.Stat(path); err == nil {
		path = filepath.Join(dir, "panel-migrate-"+time.Now().Format("20060102-150405.000")+".db")
	}
	if err := db.Backup(s.DB, path); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Keep the file under backups/ for operator convenience; also stream it.
	s.serveMigrateFile(w, r, u.ID, path)
}

func (s *Server) serveMigrateFile(w http.ResponseWriter, r *http.Request, adminID int64, path string) {
	f, err := os.Open(path)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := filepath.Base(path)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	db.WriteAudit(s.DB, adminID, "migrate.export", name, strconv.FormatInt(st.Size(), 10))
	_, _ = io.Copy(w, f)
}

// apiMigrateStatus reports pending redirect + online nodes + recent acks.
func (s *Server) apiMigrateStatus(w http.ResponseWriter, r *http.Request) {
	pending, _ := db.GetSetting(s.DB, "pending_panel_redirect_url")
	panelURL, _ := db.GetSetting(s.DB, "panel_url")
	nodes, err := db.ListNodes(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reconcileNodeOnline(nodes)
	acks := s.Hub.RedirectAcks()
	onlineIDs := map[int64]bool{}
	for _, id := range s.Hub.OnlineNodeIDs() {
		onlineIDs[id] = true
	}
	type row struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		NodeType     string `json:"node_type"`
		Online       bool   `json:"online"`
		AgentVersion string `json:"agent_version"`
		RedirectOK   *bool  `json:"redirect_ok,omitempty"`
		RedirectErr  string `json:"redirect_error,omitempty"`
		RedirectAt   int64  `json:"redirect_at,omitempty"`
	}
	var list []row
	var onlineN, ackedOK, ackedFail, offlineN int
	for _, n := range nodes {
		if n.NodeType == "self" || n.NodeType == "composite" {
			continue
		}
		on := onlineIDs[n.ID] || n.Online == 1
		if onlineIDs[n.ID] {
			on = true
		} else {
			// Prefer hub map for "currently connected to this process".
			on = false
		}
		if on {
			onlineN++
		} else {
			offlineN++
		}
		rr := row{
			ID: n.ID, Name: n.Name, NodeType: n.NodeType, Online: on,
			AgentVersion: n.AgentVersion,
		}
		if a, ok := acks[n.ID]; ok && a.Attempted {
			okv := a.OK
			rr.RedirectOK = &okv
			rr.RedirectErr = a.Error
			rr.RedirectAt = a.At
			if a.OK {
				ackedOK++
			} else {
				ackedFail++
			}
		}
		list = append(list, rr)
	}
	jsonOK(w, map[string]any{
		"pending_panel_redirect_url": strings.TrimSpace(pending),
		"panel_url":                  panelURL,
		"online":                     onlineN,
		"offline":                    offlineN,
		"redirect_ok":                ackedOK,
		"redirect_fail":              ackedFail,
		"nodes":                      list,
		"steps": []string{
			"1. 在本页「导出数据库」下载 panel-migrate-*.db",
			"2. 新机器 install.sh server 装好面板后停止服务，用导出文件覆盖 /var/lib/nft/panel.db，再启动",
			"3. 确认新面板能登录且节点列表一致（节点会暂时离线，属正常）",
			"4. 旧面板仍开着时，填写新面板地址并「通知 Agent 切换」",
			"5. 观察状态：在线节点会断开并连到新面板；离线节点连上旧面板后会自动补推",
			"6. 新面板节点都在线后，停掉旧机，可点「清除待迁移」",
		},
	})
}

// apiMigrateRedirect stores pending URL, updates panel_url, broadcasts redirect.
func (s *Server) apiMigrateRedirect(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	var body struct {
		PanelURL string `json:"panel_url"`
		Force    bool   `json:"force"`
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	base, err := normalizeMigratePanelURL(body.PanelURL)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Agent-facing form (wss/ws + /v1/agents) is derived on the agent; we store
	// the https/http base so settings.panel_url stays usable for binary upgrades.
	if err := db.SetSetting(s.DB, "pending_panel_redirect_url", base); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := db.SetSetting(s.DB, "panel_url", base); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	results := s.Hub.BroadcastPanelRedirect(base, body.Force)
	okN, failN := 0, 0
	detail := map[string]string{}
	for id, errMsg := range results {
		key := strconv.FormatInt(id, 10)
		if errMsg == "" {
			okN++
			detail[key] = "ok"
		} else {
			failN++
			detail[key] = errMsg
		}
	}
	db.WriteAudit(s.DB, u.ID, "migrate.redirect", base,
		fmt.Sprintf("online=%d ok=%d fail=%d", len(results), okN, failN))
	jsonOK(w, map[string]any{
		"ok":                         true,
		"panel_url":                  base,
		"pending_panel_redirect_url": base,
		"pushed":                     len(results),
		"ok_count":                   okN,
		"fail_count":                 failN,
		"results":                    detail,
		"message":                    "已写入待迁移地址并对在线节点推送；离线节点连上本面板后会自动补推",
	})
}

// apiMigrateClearPending clears the pending redirect (stop auto-push on hello).
func (s *Server) apiMigrateClearPending(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	if err := db.SetSetting(s.DB, "pending_panel_redirect_url", ""); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	db.WriteAudit(s.DB, u.ID, "migrate.clear_pending", "", "")
	jsonOK(w, map[string]any{"ok": true})
}
