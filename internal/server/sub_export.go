package server

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"nft/internal/db"
	"nft/internal/subexport"

	"github.com/go-chi/chi/v5"
)

// --- Public subscription feed (token in path, no session) ---

func (s *Server) publicSubPlain(w http.ResponseWriter, r *http.Request) {
	s.servePublicSub(w, r, "plain")
}

func (s *Server) publicSubMihomo(w http.ResponseWriter, r *http.Request) {
	s.servePublicSub(w, r, "mihomo")
}

func (s *Server) publicSubGlobal(w http.ResponseWriter, r *http.Request) {
	s.servePublicSub(w, r, "global")
}

func (s *Server) publicSubShadowrocket(w http.ResponseWriter, r *http.Request) {
	s.servePublicSub(w, r, "sr")
}

func (s *Server) servePublicSub(w http.ResponseWriter, r *http.Request, kind string) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" || len(token) < 16 {
		http.NotFound(w, r)
		return
	}
	u, st, err := db.GetUserBySubToken(s.DB, token)
	if err != nil || u == nil || st == nil || st.Disabled {
		http.NotFound(w, r)
		return
	}
	if u.Disabled {
		http.NotFound(w, r)
		return
	}
	_ = db.TouchSubTokenUsage(s.DB, st.ID)

	nodes, err := s.loadSubExportNodes(u.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Optional single-node filter for per-proxy subscription links
	// (Clash Verge / OpenClash import one host:port).
	nodes = filterSubExportNodes(nodes, r)
	opt := subexport.Options{Username: u.Username, Panel: s.panelBrandName()}
	body, ctype, filename := renderSubKind(kind, nodes, opt, r.URL.Query().Get("raw") == "1")
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	if filename != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	// Optional subscription-userinfo for clients that show traffic.
	if info := subscriptionUserinfoHeader(u); info != "" {
		w.Header().Set("Subscription-Userinfo", info)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func (s *Server) loadSubExportNodes(userID int64) ([]subexport.Node, error) {
	rows, err := db.ListSubVisibleReadyInstancesForUser(s.DB, userID)
	if err != nil {
		return nil, err
	}
	out := make([]subexport.Node, 0, len(rows))
	for _, r := range rows {
		out = append(out, subexport.Node{
			Name:     r.Name,
			Protocol: r.Protocol,
			URI:      r.URI,
			Host:     strings.TrimSpace(r.ShareHost),
			Port:     r.ListenPort,
		})
	}
	return out, nil
}

// filterSubExportNodes narrows the feed when clients pass host/port(/protocol)
// query params (used by 我的代理 → 订阅链接 per row).
func filterSubExportNodes(nodes []subexport.Node, r *http.Request) []subexport.Node {
	if r == nil || len(nodes) == 0 {
		return nodes
	}
	q := r.URL.Query()
	host := strings.TrimSpace(q.Get("host"))
	portStr := strings.TrimSpace(q.Get("port"))
	proto := strings.ToLower(strings.TrimSpace(q.Get("protocol")))
	if host == "" && portStr == "" && proto == "" {
		return nodes
	}
	var port int
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p <= 0 {
			return nil
		}
		port = p
	}
	out := make([]subexport.Node, 0, 1)
	for _, n := range nodes {
		if host != "" {
			nh := strings.TrimSpace(n.Host)
			if nh == "" {
				// Fall back to host parsed from share URI when ShareHost empty.
				if ep := hostPortFromURI(n.URI); ep != "" {
					nh = strings.Split(ep, ":")[0]
				}
			}
			if !strings.EqualFold(nh, host) {
				continue
			}
		}
		if port > 0 {
			np := n.Port
			if np <= 0 {
				if ep := hostPortFromURI(n.URI); ep != "" {
					if i := strings.LastIndex(ep, ":"); i > 0 {
						if p, err := strconv.Atoi(ep[i+1:]); err == nil {
							np = p
						}
					}
				}
			}
			if np != port {
				continue
			}
		}
		if proto != "" && strings.ToLower(strings.TrimSpace(n.Protocol)) != proto {
			continue
		}
		out = append(out, n)
	}
	return out
}

func hostPortFromURI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	// scheme://...@host:port or scheme://host:port
	i := strings.Index(uri, "://")
	if i < 0 {
		return ""
	}
	rest := uri[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	// IPv6 in brackets
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]:"); end > 0 {
			return rest[:end+1] + rest[end+1:]
		}
		return rest
	}
	return rest
}

func (s *Server) panelBrandName() string {
	if s.DB == nil {
		return "nft-panel"
	}
	name, _ := db.GetSetting(s.DB, "panel_name")
	name = strings.TrimSpace(name)
	if name == "" {
		return "nft-panel"
	}
	return name
}

func renderSubKind(kind string, nodes []subexport.Node, opt subexport.Options, rawPlain bool) (body, contentType, filename string) {
	switch kind {
	case "mihomo":
		return subexport.ClashSplit(nodes, opt), "text/yaml; charset=utf-8", "mihomo.yaml"
	case "global":
		return subexport.ClashGlobal(nodes, opt), "text/yaml; charset=utf-8", "global.yaml"
	case "sr":
		return subexport.ShadowrocketConf(nodes, opt), "text/plain; charset=utf-8", "shadowrocket.conf"
	default:
		if rawPlain {
			return subexport.PlainRaw(nodes), "text/plain; charset=utf-8", "subscription.txt"
		}
		return subexport.PlainBase64(nodes), "text/plain; charset=utf-8", "subscription.txt"
	}
}

func subscriptionUserinfoHeader(u *db.User) string {
	if u == nil {
		return ""
	}
	// upload=0; download=billable used (raw × billing_rate); total=quota; expire=unix.
	// Must match enforceUserQuota / account UI so clients don't show under-quota
	// while the panel has already disabled the user for 流量超额.
	parts := []string{
		"upload=0",
		"download=" + strconv.FormatInt(userBillableTraffic(u), 10),
	}
	if u.TrafficQuotaBytes > 0 {
		parts = append(parts, "total="+strconv.FormatInt(u.TrafficQuotaBytes, 10))
	} else {
		parts = append(parts, "total=0")
	}
	if u.ExpiresAt.Valid && u.ExpiresAt.Int64 > 0 {
		parts = append(parts, "expire="+strconv.FormatInt(u.ExpiresAt.Int64, 10))
	} else {
		parts = append(parts, "expire=0")
	}
	return strings.Join(parts, "; ")
}

// --- Session APIs for /my/subscription ---

func (s *Server) apiMyGetSubscription(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	// Auto-create on first open so UI can show full links once.
	st, plaintext, created, err := db.EnsureSubToken(s.DB, u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "读取订阅凭证失败")
		return
	}
	nodes, _ := db.ListSubVisibleReadyInstancesForUser(s.DB, u.ID)
	base := panelBaseURL(s.DB, r)
	resp := map[string]any{
		"has_token":    true,
		"token_prefix": st.TokenPrefix,
		"disabled":     st.Disabled,
		"created_at":   st.CreatedAt,
		"node_count":   len(nodes),
		"base_url":     base,
		// Paths relative to base; token filled when plaintext available.
		"paths": map[string]string{
			"plain":  "/sub/{token}",
			"mihomo": "/sub/{token}/mihomo.yaml",
			"global": "/sub/{token}/global.yaml",
			"sr":     "/sub/{token}/shadowrocket.conf",
		},
	}
	if st.LastUsedAt.Valid {
		resp["last_used_at"] = st.LastUsedAt.Int64
	}
	if plaintext != "" {
		resp["token"] = plaintext
		resp["token_just_created"] = created
		resp["urls"] = subURLMap(base, plaintext)
	} else {
		// No plaintext stored — client must refresh to get new full URLs.
		resp["token_available"] = false
		resp["hint"] = "完整订阅链接仅在创建或重置时显示一次。若浏览器未保存，请点「重置订阅」。"
	}
	// Node summary (no secrets beyond host already in share)
	list := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, map[string]any{
			"name":     n.Name,
			"protocol": n.Protocol,
			"host":     n.ShareHost,
			"port":     n.ListenPort,
		})
	}
	resp["nodes"] = list
	jsonOK(w, resp)
}

func (s *Server) apiMyCreateSubscription(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	if _, err := db.GetSubTokenByUser(s.DB, u.ID); err == nil {
		jsonErr(w, http.StatusConflict, "已存在订阅凭证，请使用重置")
		return
	} else if err != sql.ErrNoRows {
		jsonErr(w, http.StatusInternalServerError, "读取失败")
		return
	}
	token, err := db.CreateSubToken(s.DB, u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "创建失败")
		return
	}
	base := panelBaseURL(s.DB, r)
	jsonOK(w, map[string]any{
		"token": token,
		"urls":  subURLMap(base, token),
	})
}

func (s *Server) apiMyRefreshSubscription(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	token, err := db.RefreshSubToken(s.DB, u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "重置失败")
		return
	}
	base := panelBaseURL(s.DB, r)
	jsonOK(w, map[string]any{
		"token": token,
		"urls":  subURLMap(base, token),
	})
}

func (s *Server) apiMyToggleSubscription(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	disabled, err := db.ToggleSubToken(s.DB, u.ID)
	if err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "订阅凭证不存在")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "操作失败")
		return
	}
	jsonOK(w, map[string]any{"disabled": disabled})
}

func (s *Server) apiMyPreviewSubscription(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind == "" {
		kind = "mihomo"
	}
	switch kind {
	case "mihomo", "global", "sr", "plain", "plain_raw":
	default:
		jsonErr(w, http.StatusBadRequest, "kind 须为 mihomo|global|sr|plain")
		return
	}
	nodes, err := s.loadSubExportNodes(u.ID)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "加载节点失败")
		return
	}
	opt := subexport.Options{Username: u.Username, Panel: s.panelBrandName()}
	raw := kind == "plain_raw"
	if kind == "plain_raw" {
		kind = "plain"
	}
	body, ctype, _ := renderSubKind(kind, nodes, opt, raw)
	// For API consumers prefer JSON; UI can also request text via Accept — keep JSON.
	if r.URL.Query().Get("format") == "raw" {
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		return
	}
	jsonOK(w, map[string]any{
		"kind":    kind,
		"content": body,
		"nodes":   len(nodes),
	})
}

func subURLMap(base, token string) map[string]string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = ""
	}
	p := func(suffix string) string {
		if base == "" {
			return "/sub/" + token + suffix
		}
		return base + "/sub/" + token + suffix
	}
	return map[string]string{
		"plain":  p(""),
		"mihomo": p("/mihomo.yaml"),
		"global": p("/global.yaml"),
		"sr":     p("/shadowrocket.conf"),
	}
}
