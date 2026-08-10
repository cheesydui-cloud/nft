package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"nft/internal/db"
	"nft/internal/landing"
	"nft/internal/proxysvc"
	"nft/internal/wsproto"
)

// apiListProxyServices returns all proxy services with instance counts.
func (s *Server) apiListProxyServices(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListProxyServices(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*db.ProxyService{}
	}
	jsonOK(w, map[string]any{"services": list})
}

// apiGetProxyService returns one service with instances.
func (s *Server) apiGetProxyService(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	inst, err := db.ListProxyInstances(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc.Instances = inst
	svc.InstanceCount = len(inst)
	ready := 0
	for _, i := range inst {
		if i.DeployStatus == db.ProxyDeployReady {
			ready++
		}
	}
	svc.ReadyCount = ready
	jsonOK(w, map[string]any{"service": svc})
}

type proxyServiceBody struct {
	Name       string          `json:"name"`
	Protocol   string          `json:"protocol"`
	Core       string          `json:"core"`
	Config     json.RawMessage `json:"config"`
	SubVisible *bool           `json:"sub_visible"`
}

// apiCreateProxyService creates a draft service (step 1–2 of the wizard).
func (s *Server) apiCreateProxyService(w http.ResponseWriter, r *http.Request) {
	var body proxyServiceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	protocol := db.NormalizeProxyProtocol(body.Protocol)
	core := body.Core
	if core == "" {
		core = db.DefaultCoreForProtocol(protocol)
	}
	core = db.CanonicalCore(core)
	sub := true
	if body.SubVisible != nil {
		sub = *body.SubVisible
	}
	cfg, err := proxysvc.EnsureSecrets(protocol, body.Config)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	svc, err := db.CreateProxyService(s.DB, body.Name, protocol, core, cfg, sub)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOK(w, map[string]any{"service": svc})
}

// apiUpdateProxyService updates name/config/sub_visible.
func (s *Server) apiUpdateProxyService(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	var body proxyServiceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := body.Name
	if name == "" {
		name = svc.Name
	}
	cfg := body.Config
	if len(cfg) == 0 {
		cfg = svc.ConfigJSON
	} else {
		cfg, err = proxysvc.EnsureSecrets(svc.Protocol, cfg)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	sub := svc.SubVisible
	if body.SubVisible != nil {
		sub = *body.SubVisible
	}
	if err := db.UpdateProxyService(s.DB, id, name, cfg, sub); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	svc, _ = db.GetProxyService(s.DB, id)
	jsonOK(w, map[string]any{"service": svc})
}

// apiDeleteProxyService removes a service and instances.
func (s *Server) apiDeleteProxyService(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := db.DeleteProxyService(s.DB, id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

type proxyPublishBody struct {
	NodeIDs []int64 `json:"node_ids"`
	// Optional per-node port overrides: map as list of {node_id, port}
	Ports []struct {
		NodeID int64 `json:"node_id"`
		Port   int   `json:"port"`
	} `json:"ports"`
}

// apiPublishProxyService deploys the service onto selected nodes.
func (s *Server) apiPublishProxyService(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	var body proxyPublishBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.NodeIDs) == 0 {
		jsonErr(w, http.StatusBadRequest, "请至少选择一个节点")
		return
	}
	portByNode := map[int64]int{}
	for _, p := range body.Ports {
		if p.Port > 0 {
			portByNode[p.NodeID] = p.Port
		}
	}
	defaultPort := proxysvc.ListenPortFromConfig(svc.ConfigJSON)
	cfgShare := proxysvc.ShareHostFromConfig(svc.ConfigJSON)

	// Ensure secrets present before deploy.
	cfg, err := proxysvc.EnsureSecrets(svc.Protocol, svc.ConfigJSON)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = db.UpdateProxyService(s.DB, id, svc.Name, cfg, svc.SubVisible)
	svc.ConfigJSON = cfg

	results := make([]map[string]any, 0, len(body.NodeIDs))
	for _, nodeID := range body.NodeIDs {
		node, err := db.GetNode(s.DB, nodeID)
		if err != nil || node == nil {
			results = append(results, map[string]any{"node_id": nodeID, "ok": false, "error": "节点不存在"})
			continue
		}
		port := defaultPort
		if p, ok := portByNode[nodeID]; ok && p > 0 {
			port = p
		}
		shareHost := cfgShare
		if shareHost == "" {
			shareHost = firstNonEmpty(node.RelayHost, node.Address)
			// Address may be "ip:port" from connect — strip port if present.
			if h, _, err := splitHostPortLoose(shareHost); err == nil && h != "" {
				shareHost = h
			}
		}
		inst, err := db.UpsertProxyInstance(s.DB, id, nodeID, port, shareHost)
		if err != nil {
			results = append(results, map[string]any{"node_id": nodeID, "ok": false, "error": err.Error()})
			continue
		}
		// Build URI on panel side always (even dry-run).
		uri, uriErr := proxysvc.BuildShareURI(svc.Protocol, svc.Name, shareHost, port, cfg)
		if uriErr != nil {
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, "", uriErr.Error(), "")
			results = append(results, map[string]any{"node_id": nodeID, "ok": false, "error": uriErr.Error()})
			continue
		}

		// Ensure proxy core is on the node (push from panel cache if needed).
		if err := s.ensureCoreOnNode(node, svc.Protocol); err != nil {
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, uri, err.Error(), "")
			results = append(results, map[string]any{
				"node_id": nodeID, "ok": false, "uri": uri, "error": err.Error(), "instance_id": inst.ID,
			})
			continue
		}

		// Try live apply on agent.
		applyRes := s.applyProxyInstance(nodeID, svc, inst, shareHost, port, cfg)
		if applyRes.OK {
			status := db.ProxyDeployReady
			finalURI := uri
			if applyRes.URI != "" {
				finalURI = applyRes.URI
			}
			note := ""
			if applyRes.DryRun {
				// URI-only path (e.g. VLESS/SS phase-1): keep ready but surface note.
				note = applyRes.Error
				if note == "" {
					note = "dry-run：仅生成链接，节点未启动核心进程"
				}
			}
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, status, finalURI, note, applyRes.CoreVersion)
			results = append(results, map[string]any{
				"node_id": nodeID, "ok": true, "uri": finalURI, "dry_run": applyRes.DryRun,
				"instance_id": inst.ID, "warning": note,
			})
		} else {
			// Real failure (e.g. mita missing / apply error): mark error so admin
			// does not treat the share link as a live endpoint.
			msg := applyRes.Error
			if msg == "" {
				msg = "agent 部署失败"
			}
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, uri, msg, "")
			results = append(results, map[string]any{
				"node_id": nodeID, "ok": false, "uri": uri, "dry_run": applyRes.DryRun,
				"error": msg, "instance_id": inst.ID,
			})
		}
	}
	_ = db.RecomputeProxyServiceStatus(s.DB, id)
	svc, _ = db.GetProxyService(s.DB, id)
	inst, _ := db.ListProxyInstances(s.DB, id)
	svc.Instances = inst
	jsonOK(w, map[string]any{"service": svc, "results": results})
}

type applyOutcome struct {
	OK          bool
	Error       string
	URI         string
	CoreVersion string
	DryRun      bool
}

// ensureCoreOnNode pushes a cached core binary to the node when the agent
// does not already report that core. Returns nil when the core is present or
// was installed successfully.
func (s *Server) ensureCoreOnNode(node *db.Node, protocol string) error {
	need := coreNeededForProtocol(protocol)
	if need == "" {
		return nil
	}
	cores, _ := db.GetNodeCores(s.DB, node.ID)
	if nodeCorePresent(cores, need) {
		return nil
	}
	arch := sanitizeArch(node.AgentArch)
	if arch == "" {
		arch = "amd64"
	}
	meta, err := readCoreMeta(need, arch)
	if err != nil {
		label := need
		if need == "mita" {
			label = "mita（mieru 服务端）"
		}
		return fmt.Errorf("节点未安装 %s，且面板缓存无 %s/%s：请先在 系统设置 → 代理核心缓存 下载", label, need, arch)
	}
	if s.Hub == nil || !s.Hub.IsOnline(node.ID) {
		return fmt.Errorf("节点离线，无法推送核心 %s", need)
	}
	panelURL, _ := db.GetSetting(s.DB, "panel_url")
	base := normalizePanelBaseURL(panelURL)
	if base == "" {
		return fmt.Errorf("未配置面板地址 panel_url，无法生成核心下载链接")
	}
	dl := fmt.Sprintf("%s/v1/cores/%s?arch=%s", base, need, arch)
	ack, err := s.Hub.SendCoreInstall(node.ID, wsproto.CoreInstall{
		Type:       need,
		Arch:       arch,
		Version:    meta.Version,
		SHA256:     meta.SHA256,
		Size:       meta.Size,
		DownloadAt: dl,
	})
	if err != nil {
		return fmt.Errorf("推送核心 %s 失败: %w", need, err)
	}
	if !ack.OK {
		msg := ack.Error
		if msg == "" {
			msg = "agent 安装核心失败"
		}
		return fmt.Errorf("安装核心 %s 失败: %s", need, msg)
	}
	updated := false
	for i := range cores {
		n := strings.ToLower(cores[i].Name)
		if n == coreDetectName(need) || (need == "mita" && (n == "mieru" || n == "mita")) || n == need {
			cores[i].Version = ack.Version
			if ack.Path != "" {
				cores[i].Path = ack.Path
			}
			updated = true
			break
		}
	}
	if !updated {
		cores = append(cores, db.NodeCore{Name: coreDetectName(need), Version: ack.Version, Path: ack.Path})
	}
	if b, err := json.Marshal(cores); err == nil {
		_ = db.SetNodeCoresJSON(s.DB, node.ID, string(b))
	}
	return nil
}

func coreDetectName(cacheType string) string {
	switch sanitizeCoreType(cacheType) {
	case "mita":
		return "mieru"
	default:
		return sanitizeCoreType(cacheType)
	}
}

func nodeCorePresent(cores []db.NodeCore, cacheType string) bool {
	want := sanitizeCoreType(cacheType)
	for _, c := range cores {
		n := strings.ToLower(strings.TrimSpace(c.Name))
		switch want {
		case "xray":
			if n == "xray" {
				return true
			}
		case "sing-box":
			if n == "sing-box" || n == "singbox" {
				return true
			}
		case "mita":
			if n == "mieru" || n == "mita" || n == "mbox" {
				return true
			}
		}
	}
	return false
}

func (s *Server) applyProxyInstance(nodeID int64, svc *db.ProxyService, inst *db.ProxyServiceInstance, shareHost string, port int, cfg json.RawMessage) applyOutcome {
	if s.Hub == nil || !s.Hub.IsOnline(nodeID) {
		return applyOutcome{OK: false, Error: "节点离线", DryRun: true}
	}
	ack, err := s.Hub.SendProxyServiceApply(nodeID, wsproto.ProxyServiceApply{
		InstanceID: inst.ID,
		ServiceID:  svc.ID,
		Protocol:   svc.Protocol,
		Core:       svc.Core,
		ListenPort: port,
		ShareHost:  shareHost,
		Name:       svc.Name,
		Config:     cfg,
	})
	if err != nil {
		return applyOutcome{OK: false, Error: err.Error(), DryRun: true}
	}
	if !ack.OK {
		return applyOutcome{OK: false, Error: ack.Error, DryRun: ack.DryRun}
	}
	return applyOutcome{OK: true, URI: ack.URI, CoreVersion: ack.CoreVersion, DryRun: ack.DryRun}
}

type proxySyncBody struct {
	InstanceIDs []int64 `json:"instance_ids"` // empty = all ready instances
	GroupName   string  `json:"group_name"`
}

// apiSyncProxyServiceToRepo copies ready instance URIs into node_repo.
func (s *Server) apiSyncProxyServiceToRepo(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	var body proxySyncBody
	_ = json.NewDecoder(r.Body).Decode(&body)

	inst, err := db.ListProxyInstances(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	want := map[int64]bool{}
	for _, iid := range body.InstanceIDs {
		want[iid] = true
	}
	filter := len(want) > 0
	synced := 0
	var outs []map[string]any
	for _, i := range inst {
		if filter && !want[i.ID] {
			continue
		}
		if i.URI == "" {
			outs = append(outs, map[string]any{"instance_id": i.ID, "ok": false, "error": "无 URI"})
			continue
		}
		host := i.ShareHost
		port := i.ListenPort
		// Prefer parsing URI if share fields incomplete.
		if host == "" || port <= 0 {
			if n, ok := parseProxyEndpoint(i.URI); ok {
				if host == "" {
					host = n.host
				}
				if port <= 0 {
					port = n.port
				}
			}
		}
		if host == "" || port <= 0 {
			outs = append(outs, map[string]any{"instance_id": i.ID, "ok": false, "error": "无法解析 host/port"})
			continue
		}
		name := svc.Name
		if i.NodeName != "" {
			name = svc.Name + " · " + i.NodeName
		}
		remark := "proxy-service:" + strconv.FormatInt(svc.ID, 10)
		group := strings.TrimSpace(body.GroupName)
		if group == "" {
			group = "代理服务"
		}
		if i.SyncedRepoID > 0 {
			if _, err := db.GetNodeRepoEntry(s.DB, i.SyncedRepoID); err == nil {
				err = db.UpdateNodeRepoEntry(s.DB, i.SyncedRepoID, name, svc.Protocol, host, port, i.URI, remark, 0, group, db.NodeRepoCFFields{})
				if err != nil {
					outs = append(outs, map[string]any{"instance_id": i.ID, "ok": false, "error": err.Error()})
					continue
				}
				synced++
				outs = append(outs, map[string]any{"instance_id": i.ID, "ok": true, "repo_id": i.SyncedRepoID, "updated": true})
				continue
			}
		}
		entry, err := db.CreateNodeRepoEntry(s.DB, name, svc.Protocol, host, port, i.URI, remark, 0, group, db.NodeRepoCFFields{})
		if err != nil {
			outs = append(outs, map[string]any{"instance_id": i.ID, "ok": false, "error": err.Error()})
			continue
		}
		_ = db.SetProxyInstanceSyncedRepo(s.DB, i.ID, entry.ID)
		synced++
		outs = append(outs, map[string]any{"instance_id": i.ID, "ok": true, "repo_id": entry.ID, "updated": false})
	}
	jsonOK(w, map[string]any{"synced": synced, "results": outs})
}

// apiProbeProxyServiceLatency checks each instance's listen port from the node
// (TCP dial 127.0.0.1:port) and reports latency. Offline nodes are skipped with error.
func (s *Server) apiProbeProxyServiceLatency(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	inst, err := db.ListProxyInstances(s.DB, id)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(inst) == 0 {
		jsonOK(w, map[string]any{
			"service_id": id,
			"name":       svc.Name,
			"results":    []any{},
			"ok_count":   0,
			"fail_count": 0,
			"summary":    "尚无部署实例",
		})
		return
	}

	type row struct {
		InstanceID int64  `json:"instance_id"`
		NodeID     int64  `json:"node_id"`
		NodeName   string `json:"node_name"`
		Target     string `json:"target"`
		OK         bool   `json:"ok"`
		Latency    int    `json:"latency_ms"`
		Error      string `json:"error,omitempty"`
		ShareHost  string `json:"share_host,omitempty"`
		ListenPort int    `json:"listen_port"`
	}
	results := make([]row, len(inst))
	var wg sync.WaitGroup
	for i, it := range inst {
		wg.Add(1)
		go func(i int, it *db.ProxyServiceInstance) {
			defer wg.Done()
			name := it.NodeName
			if name == "" {
				if n, err := db.GetNode(s.DB, it.NodeID); err == nil && n != nil {
					name = n.Name
				} else {
					name = fmt.Sprintf("#%d", it.NodeID)
				}
			}
			port := it.ListenPort
			if port <= 0 {
				results[i] = row{
					InstanceID: it.ID, NodeID: it.NodeID, NodeName: name,
					ListenPort: port, ShareHost: it.ShareHost,
					Error: "无监听端口",
				}
				return
			}
			// Probe from the node itself: is the core listening on this port?
			target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			// Prefer IPv6 loopback if listen is only on :: — still try 127.0.0.1 first.
			ack, err := s.Hub.SendProbe(it.NodeID, target)
			r := row{
				InstanceID: it.ID, NodeID: it.NodeID, NodeName: name,
				Target: target, ListenPort: port, ShareHost: it.ShareHost,
			}
			if err != nil {
				r.Error = err.Error()
			} else if !ack.OK {
				r.Error = ack.Error
				if r.Error == "" {
					r.Error = "端口不通"
				}
			} else {
				r.OK = true
				r.Latency = ack.Latency
			}
			results[i] = r
		}(i, it)
	}
	wg.Wait()

	okN, failN := 0, 0
	minL, maxL, sumL := -1, 0, 0
	for _, r := range results {
		if r.OK {
			okN++
			if minL < 0 || r.Latency < minL {
				minL = r.Latency
			}
			if r.Latency > maxL {
				maxL = r.Latency
			}
			sumL += r.Latency
		} else {
			failN++
		}
	}
	summary := fmt.Sprintf("%d/%d 端口可达", okN, len(results))
	if okN > 0 {
		avg := sumL / okN
		summary = fmt.Sprintf("%d/%d 可达 · 延迟 %d–%d ms（均 %d ms）", okN, len(results), minL, maxL, avg)
	} else if failN > 0 {
		summary = fmt.Sprintf("0/%d 可达 — 节点离线或核心未监听", len(results))
	}

	jsonOK(w, map[string]any{
		"service_id": id,
		"name":       svc.Name,
		"protocol":   svc.Protocol,
		"results":    results,
		"ok_count":   okN,
		"fail_count": failN,
		"summary":    summary,
		"probed_at":  time.Now().Unix(),
	})
}

// apiProxyServiceGenKeys generates REALITY key material for the wizard UI.
// kind=reality (default) | short_id | vlessenc
func (s *Server) apiProxyServiceGenKeys(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	switch kind {
	case "short_id":
		jsonOK(w, map[string]any{"short_id": proxysvc.GenerateShortID()})
	case "vlessenc", "mlkem", "encryption":
		enc, dec, ver, err := generateVlessEncPair()
		if err != nil {
			jsonErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		jsonOK(w, map[string]any{
			"encryption":   enc,
			"decryption":   dec,
			"xray_version": ver,
			"kind":         "vlessenc",
		})
	default:
		priv, pub := proxysvc.GenerateRealityKeyPair()
		jsonOK(w, map[string]any{"private_key": priv, "public_key": pub})
	}
}

type proxyEndpoint struct {
	host string
	port int
}

func parseProxyEndpoint(uri string) (proxyEndpoint, bool) {
	nodes := landing.ParseURIs([]string{uri})
	if len(nodes) == 0 {
		return proxyEndpoint{}, false
	}
	return proxyEndpoint{host: nodes[0].Host, port: nodes[0].Port}, true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func splitHostPortLoose(s string) (host, port string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", strconv.ErrSyntax
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.LastIndex(s, "]:"); i > 0 {
			return s[1:i], s[i+2:], nil
		}
		if strings.HasSuffix(s, "]") {
			return s[1 : len(s)-1], "", nil
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		if strings.Count(s, ":") == 1 {
			return s[:i], s[i+1:], nil
		}
	}
	return s, "", nil
}
