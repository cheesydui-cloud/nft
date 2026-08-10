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
	// Attach a sample instance last_error so list UI can show why status is error
	// without an extra round-trip per row.
	type svcOut struct {
		*db.ProxyService
		LastErrorSample string `json:"last_error_sample,omitempty"`
	}
	out := make([]svcOut, 0, len(list))
	for _, svc := range list {
		row := svcOut{ProxyService: svc}
		if svc.Status == db.ProxyStatusError || svc.Status == "error" || svc.Status == db.ProxyStatusPartial || svc.Status == "partial" {
			if inst, err := db.ListProxyInstances(s.DB, svc.ID); err == nil {
				for _, i := range inst {
					if strings.TrimSpace(i.LastError) != "" {
						msg := strings.TrimSpace(i.LastError)
						if len(msg) > 120 {
							msg = msg[:120] + "…"
						}
						row.LastErrorSample = msg
						break
					}
				}
			}
		}
		out = append(out, row)
	}
	jsonOK(w, map[string]any{"services": out})
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
	ForceCore bool    `json:"force_core"` // re-push core from panel cache even if node reports installed
	NodeIDs   []int64 `json:"node_ids"`
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
		if err := s.ensureCoreOnNodeForce(node, svc.Protocol, body.ForceCore); err != nil {
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
	return s.ensureCoreOnNodeForce(node, protocol, false)
}

func (s *Server) ensureCoreOnNodeForce(node *db.Node, protocol string, force bool) error {
	need := coreNeededForProtocol(protocol)
	if need == "" {
		return nil
	}
	cores, _ := db.GetNodeCores(s.DB, node.ID)
	if !force && nodeCorePresent(cores, need) {
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

// apiProbeProxyServiceLatency measures TCP reachability / RTT for each instance.
//
// Default mode=public: panel process dials share_host:listen_port (client path RTT).
// mode=local: agent on the node dials loopback (core process alive).
//
// Query/body: mode=public|local (default public).
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

	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		// optional JSON body { "mode": "public"|"local" }
		var body struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mode = strings.ToLower(strings.TrimSpace(body.Mode))
	}
	if mode != "local" {
		mode = "public"
	}

	if len(inst) == 0 {
		jsonOK(w, map[string]any{
			"service_id": id,
			"name":       svc.Name,
			"mode":       mode,
			"results":    []any{},
			"ok_count":   0,
			"fail_count": 0,
			"summary":    "尚无部署实例",
		})
		return
	}

	type row struct {
		InstanceID   int64  `json:"instance_id"`
		NodeID       int64  `json:"node_id"`
		NodeName     string `json:"node_name"`
		Target       string `json:"target"`
		OK           bool   `json:"ok"`
		Latency      int    `json:"latency_ms"`
		Error        string `json:"error,omitempty"`
		ShareHost    string `json:"share_host,omitempty"`
		ListenPort   int    `json:"listen_port"`
		DeployStatus string `json:"deploy_status,omitempty"`
		Mode         string `json:"mode,omitempty"`
	}
	results := make([]row, len(inst))
	var wg sync.WaitGroup
	for i, it := range inst {
		wg.Add(1)
		go func(i int, it *db.ProxyServiceInstance) {
			defer wg.Done()
			name := it.NodeName
			online := it.NodeOnline
			if name == "" || online == 0 {
				if n, err := db.GetNode(s.DB, it.NodeID); err == nil && n != nil {
					if name == "" {
						name = n.Name
					}
					online = n.Online
				}
			}
			if name == "" {
				name = fmt.Sprintf("#%d", it.NodeID)
			}
			port := it.ListenPort
			r := row{
				InstanceID: it.ID, NodeID: it.NodeID, NodeName: name,
				ListenPort: port, ShareHost: it.ShareHost, DeployStatus: it.DeployStatus,
				Mode: mode,
			}
			if port <= 0 {
				r.Error = "无监听端口"
				results[i] = r
				return
			}

			if mode == "public" {
				// Real path: panel → share_host:port (what clients hit).
				sh := strings.TrimSpace(it.ShareHost)
				if sh == "" {
					// fall back to node relay host
					if n, err := db.GetNode(s.DB, it.NodeID); err == nil && n != nil {
						sh = firstNonEmpty(n.RelayHost, n.Address)
					}
				}
				if sh != "" {
					if h, _, err := splitHostPortLoose(sh); err == nil && h != "" {
						sh = h
					}
				}
				if sh == "" {
					r.Error = "无分享地址（无法测公网延迟）"
					results[i] = r
					return
				}
				target := net.JoinHostPort(sh, strconv.Itoa(port))
				r.Target = target
				lat, err := dialTCPLatency(target, 5*time.Second)
				if err != nil {
					r.Error = classifyProbeFail(err.Error(), it.DeployStatus, port)
					results[i] = r
					return
				}
				r.OK = true
				r.Latency = lat
				results[i] = r
				return
			}

			// mode=local: agent dials loopback (core process up on node).
			if !s.Hub.IsConnected(it.NodeID) {
				r.Error = "节点离线（agent 未连接面板）"
				results[i] = r
				return
			}
			var targets []string
			targets = append(targets,
				net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
				net.JoinHostPort("::1", strconv.Itoa(port)),
			)
			if sh := strings.TrimSpace(it.ShareHost); sh != "" {
				if h, _, err := splitHostPortLoose(sh); err == nil && h != "" {
					sh = h
				}
				if sh != "127.0.0.1" && sh != "::1" && sh != "localhost" {
					targets = append(targets, net.JoinHostPort(sh, strconv.Itoa(port)))
				}
			}
			var lastErr string
			for _, target := range targets {
				ack, err := s.Hub.SendProbe(it.NodeID, target)
				if err != nil {
					lastErr = err.Error()
					continue
				}
				if ack.OK {
					r.OK = true
					r.Latency = ack.Latency
					r.Target = target
					results[i] = r
					return
				}
				if ack.Error != "" {
					lastErr = ack.Error
				} else {
					lastErr = "连接失败"
				}
			}
			r.Target = targets[0]
			r.Error = classifyProbeFail(lastErr, it.DeployStatus, port)
			results[i] = r
		}(i, it)
	}
	wg.Wait()

	okN, failN := 0, 0
	minL, maxL, sumL := -1, 0, 0
	var firstErr string
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
			if firstErr == "" && r.Error != "" {
				firstErr = r.Error
			}
		}
	}
	modeLabel := "公网"
	if mode == "local" {
		modeLabel = "节点本机"
	}
	summary := fmt.Sprintf("%d/%d 端口可达（%s）", okN, len(results), modeLabel)
	if okN > 0 {
		avg := sumL / okN
		summary = fmt.Sprintf("%d/%d 可达 · %s延迟 %d–%d ms（均 %d ms）", okN, len(results), modeLabel, minL, maxL, avg)
		if failN > 0 && firstErr != "" {
			summary += "；失败: " + firstErr
		}
	} else if firstErr != "" {
		summary = fmt.Sprintf("0/%d 可达（%s） · %s", len(results), modeLabel, firstErr)
	} else {
		summary = fmt.Sprintf("0/%d 可达（%s）", len(results), modeLabel)
	}

	jsonOK(w, map[string]any{
		"service_id": id,
		"name":       svc.Name,
		"protocol":   svc.Protocol,
		"mode":       mode,
		"results":    results,
		"ok_count":   okN,
		"fail_count": failN,
		"summary":    summary,
		"probed_at":  time.Now().Unix(),
	})
}

// dialTCPLatency opens a TCP connection to target and returns RTT in ms.
func dialTCPLatency(target string, timeout time.Duration) (int, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, timeout)
	elapsed := time.Since(start)
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	ms := int(elapsed.Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return ms, nil
}

// classifyProbeFail turns raw dial/hub errors into operator-facing Chinese hints.
func classifyProbeFail(raw, deployStatus string, port int) string {
	s := strings.ToLower(raw)
	switch {
	case strings.Contains(s, "not connected"):
		return "节点离线（agent 未连接）"
	case strings.Contains(s, "probe timeout") || strings.Contains(s, "timeout"):
		return "探测超时"
	case strings.Contains(s, "connection refused") || strings.Contains(s, "refused") || strings.Contains(s, "actively refused"):
		hint := fmt.Sprintf("端口 %d 无监听", port)
		if deployStatus == db.ProxyDeployError || deployStatus == "error" {
			return hint + " · 部署失败，看详情 last_error 后重新发布"
		}
		if deployStatus == db.ProxyDeployReady || deployStatus == "ready" {
			return hint + " · 进程已退出，请重新发布"
		}
		return hint + " · 请重新发布"
	case strings.Contains(s, "no route") || strings.Contains(s, "network is unreachable"):
		return "网络不可达"
	case strings.TrimSpace(raw) == "":
		return "端口不通"
	default:
		// keep short for list UI
		if len(raw) > 80 {
			return raw[:80] + "…"
		}
		return raw
	}
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
