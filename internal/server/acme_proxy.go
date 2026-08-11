package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"nft/internal/acmeclient"
	"nft/internal/cloudflare"
	"nft/internal/db"
	"nft/internal/proxysvc"
)

// apiIssueProxyServiceACME issues (or renews) a Let's Encrypt cert via CF DNS-01
// and writes cert_pem/key_pem into the service config_json.
// POST /api/proxy-services/{id}/acme
// body: { "domain"?: string, "email"?: string, "staging"?: bool, "republish"?: bool }
func (s *Server) apiIssueProxyServiceACME(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	svc, err := db.GetProxyService(s.DB, id)
	if err != nil || svc == nil {
		jsonErr(w, http.StatusNotFound, "服务不存在")
		return
	}
	if !strings.EqualFold(svc.Protocol, "vless") {
		jsonErr(w, http.StatusBadRequest, "仅 VLESS 支持 ACME 证书")
		return
	}
	var body struct {
		Domain    string `json:"domain"`
		Email     string `json:"email"`
		Staging   bool   `json:"staging"`
		Republish *bool  `json:"republish"` // default true when instances exist
	}
	if r.Body != nil && r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	var vc proxysvc.VLESSConfig
	if err := json.Unmarshal(nonzeroRaw(svc.ConfigJSON), &vc); err != nil {
		jsonErr(w, http.StatusBadRequest, "配置无效: "+err.Error())
		return
	}
	domain := strings.TrimSpace(body.Domain)
	if domain == "" {
		domain = strings.TrimSpace(vc.ServerName)
	}
	if domain == "" {
		jsonErr(w, http.StatusBadRequest, "请填写域名（server_name 或 body.domain）")
		return
	}

	res, err := s.issueACMEForDomain(r.Context(), domain, body.Email, body.Staging)
	if err != nil {
		// Persist last error into config for UI.
		_ = s.patchProxyConfigACMEMeta(svc, domain, "", nil, err.Error())
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}

	// Merge PEM + metadata into config.
	vc.Security = "tls"
	vc.ServerName = domain
	vc.CertPEM = res.CertPEM
	vc.KeyPEM = res.KeyPEM
	cfgBytes, err := mergeACMEIntoConfig(svc.ConfigJSON, &vc, res)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := db.UpdateProxyService(s.DB, id, svc.Name, cfgBytes, svc.SubVisible); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u := userFromCtx(r.Context())
	if u != nil {
		db.WriteAudit(s.DB, u.ID, "proxy.acme_issue", fmt.Sprintf("%d", id), domain)
	}

	// Rebuild URIs (security may have flipped to tls).
	if inst, err := db.ListProxyInstances(s.DB, id); err == nil {
		for _, it := range inst {
			if it == nil || it.ShareHost == "" || it.ListenPort <= 0 {
				continue
			}
			uri, uriErr := proxysvc.BuildShareURI(svc.Protocol, svc.Name, it.ShareHost, it.ListenPort, cfgBytes)
			if uriErr == nil {
				_ = db.UpdateProxyInstanceURI(s.DB, it.ID, uri)
			}
		}
	}

	republish := true
	if body.Republish != nil {
		republish = *body.Republish
	}
	publishNote := ""
	if republish {
		if note, perr := s.republishProxyServiceAll(id); perr != nil {
			publishNote = perr.Error()
		} else {
			publishNote = note
		}
	}

	svc2, _ := db.GetProxyService(s.DB, id)
	if svc2 != nil {
		if inst, err := db.ListProxyInstances(s.DB, id); err == nil {
			svc2.Instances = inst
		}
		svc2.ConfigJSON = proxysvc.RedactProxyConfigJSON(svc2.Protocol, svc2.ConfigJSON)
	}
	info := proxysvc.InspectTLSCert(res.CertPEM, res.KeyPEM, domain)
	jsonOK(w, map[string]any{
		"ok":           true,
		"domain":       domain,
		"issuer":       res.Issuer,
		"not_before":   res.NotBefore.UTC().Format(time.RFC3339),
		"not_after":    res.NotAfter.UTC().Format(time.RFC3339),
		"cert_info":    info,
		"staging":      body.Staging,
		"publish_note": publishNote,
		"service":      svc2,
	})
}

// issueACMEForDomain runs DNS-01 with settings CF token + optional account key.
func (s *Server) issueACMEForDomain(ctx context.Context, domain, email string, staging bool) (*acmeclient.Result, error) {
	token, _ := db.GetSetting(s.DB, "cf_api_token")
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("未配置 Cloudflare API Token。请到「系统设置 → Cloudflare DNS」填写具备 Zone DNS Edit 的 Token")
	}
	zoneName, _ := db.GetSetting(s.DB, "cf_zone_name")
	if email == "" {
		email, _ = db.GetSetting(s.DB, "acme_email")
	}
	acctPEM, _ := db.GetSetting(s.DB, "acme_account_key")
	acctKey, acctOut, err := acmeclient.EnsureAccountKey(acctPEM)
	if err != nil {
		return nil, fmt.Errorf("ACME 账户密钥: %w", err)
	}
	// Persist account key if newly generated or was empty.
	if strings.TrimSpace(acctPEM) == "" || acctOut != acctPEM {
		_ = db.SetSetting(s.DB, "acme_account_key", acctOut)
	}

	dir := acmeclient.LetsEncryptProduction
	if staging {
		dir = acmeclient.LetsEncryptStaging
	} else if d, _ := db.GetSetting(s.DB, "acme_directory"); strings.TrimSpace(d) != "" {
		dir = strings.TrimSpace(d)
	}

	// Bound overall issue time (DNS prop + LE).
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
	}

	cfBase, _ := db.GetSetting(s.DB, "cf_api_base")
	cli := &cloudflare.Client{Token: token, BaseURL: strings.TrimSpace(cfBase)}
	return acmeclient.IssueDNS01(ctx, acmeclient.IssueRequest{
		Domain:     domain,
		Email:      email,
		Directory:  dir,
		AccountKey: acctKey,
		CF:         cli,
		ZoneName:   zoneName,
	})
}

func mergeACMEIntoConfig(raw json.RawMessage, vc *proxysvc.VLESSConfig, res *acmeclient.Result) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(nonzeroRaw(raw), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	// Marshal VLESS core fields we care about.
	m["security"] = "tls"
	m["server_name"] = vc.ServerName
	m["cert_pem"] = res.CertPEM
	m["key_pem"] = res.KeyPEM
	m["acme_enabled"] = true
	m["acme_provider"] = "cloudflare-dns01"
	m["acme_domain"] = res.Domain
	m["acme_issuer"] = res.Issuer
	m["acme_not_before"] = res.NotBefore.UTC().Format(time.RFC3339)
	m["acme_not_after"] = res.NotAfter.UTC().Format(time.RFC3339)
	m["acme_last_renew_at"] = time.Now().UTC().Format(time.RFC3339)
	m["acme_last_error"] = ""
	return json.Marshal(m)
}

func (s *Server) patchProxyConfigACMEMeta(svc *db.ProxyService, domain, issuer string, notAfter *time.Time, lastErr string) error {
	if svc == nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(nonzeroRaw(svc.ConfigJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	if domain != "" {
		m["acme_domain"] = domain
	}
	if issuer != "" {
		m["acme_issuer"] = issuer
	}
	if notAfter != nil {
		m["acme_not_after"] = notAfter.UTC().Format(time.RFC3339)
	}
	m["acme_last_error"] = lastErr
	m["acme_last_attempt_at"] = time.Now().UTC().Format(time.RFC3339)
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return db.UpdateProxyService(s.DB, svc.ID, svc.Name, out, svc.SubVisible)
}

// republishProxyServiceAll re-publishes to all existing instance nodes (force core false).
func (s *Server) republishProxyServiceAll(serviceID int64) (string, error) {
	inst, err := db.ListProxyInstances(s.DB, serviceID)
	if err != nil {
		return "", err
	}
	var nodeIDs []int64
	for _, it := range inst {
		if it != nil && it.NodeID > 0 {
			nodeIDs = append(nodeIDs, it.NodeID)
		}
	}
	if len(nodeIDs) == 0 {
		return "无部署节点，证书已写入配置，请稍后发布", nil
	}
	ok, fail := s.publishProxyToNodes(serviceID, nodeIDs, false)
	return fmt.Sprintf("重新发布 %d 成功 / %d 失败", ok, fail), nil
}

// publishProxyToNodes re-applies a service onto the given nodes (used by ACME issue/renew).
func (s *Server) publishProxyToNodes(serviceID int64, nodeIDs []int64, forceCore bool) (ok, fail int) {
	svc, err := db.GetProxyService(s.DB, serviceID)
	if err != nil || svc == nil {
		return 0, len(nodeIDs)
	}
	cfg, err := proxysvc.EnsureSecrets(svc.Protocol, svc.ConfigJSON)
	if err != nil {
		return 0, len(nodeIDs)
	}
	if strings.EqualFold(svc.Protocol, "vless") {
		var vc proxysvc.VLESSConfig
		if err := json.Unmarshal(cfg, &vc); err == nil {
			if err := proxysvc.ValidateVLESSDeploy(&vc); err != nil {
				return 0, len(nodeIDs)
			}
		}
	}
	_ = db.UpdateProxyService(s.DB, serviceID, svc.Name, cfg, svc.SubVisible)
	svc.ConfigJSON = cfg
	defaultPort := proxysvc.ListenPortFromConfig(cfg)
	cfgShare := proxysvc.ShareHostFromConfig(cfg)

	// Index existing instances once.
	instByNode := map[int64]*db.ProxyServiceInstance{}
	if insts, e := db.ListProxyInstances(s.DB, serviceID); e == nil {
		for _, it := range insts {
			if it != nil && it.NodeID > 0 {
				instByNode[it.NodeID] = it
			}
		}
	}

	for _, nodeID := range nodeIDs {
		node, err := db.GetNode(s.DB, nodeID)
		if err != nil || node == nil {
			fail++
			continue
		}
		port := defaultPort
		if it := instByNode[nodeID]; it != nil && it.ListenPort > 0 {
			port = it.ListenPort
		}
		shareHost := cfgShare
		if shareHost == "" {
			shareHost = firstNonEmpty(node.RelayHost, node.Address)
			if h, _, err := splitHostPortLoose(shareHost); err == nil && h != "" {
				shareHost = h
			}
		}
		inst, err := db.UpsertProxyInstance(s.DB, serviceID, nodeID, port, shareHost)
		if err != nil {
			fail++
			continue
		}
		uri, uriErr := proxysvc.BuildShareURI(svc.Protocol, svc.Name, shareHost, port, cfg)
		if uriErr != nil {
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, "", uriErr.Error(), "")
			fail++
			continue
		}
		// Ensure core (same as publish API).
		fc := forceCore
		if !fc && strings.EqualFold(svc.Protocol, "vless") && proxysvc.NeedsVLESSEnc(cfg) {
			fc = true
		}
		if err := s.ensureCoreOnNodeForce(node, svc.Protocol, fc); err != nil {
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, uri, err.Error(), "")
			fail++
			continue
		}
		applyRes := s.applyProxyInstance(nodeID, svc, inst, shareHost, port, cfg)
		if applyRes.OK {
			status := db.ProxyDeployReady
			finalURI := uri
			if applyRes.URI != "" {
				finalURI = applyRes.URI
			}
			note := ""
			if applyRes.DryRun {
				note = applyRes.Error
				if note == "" {
					note = "dry-run：仅生成链接，节点未启动核心进程"
				}
			}
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, status, finalURI, note, applyRes.CoreVersion)
			ok++
		} else {
			msg := applyRes.Error
			if msg == "" {
				msg = "agent 部署失败"
			}
			_ = db.UpdateProxyInstanceDeploy(s.DB, inst.ID, db.ProxyDeployError, uri, msg, applyRes.CoreVersion)
			fail++
		}
	}
	_ = db.RecomputeProxyServiceStatus(s.DB, serviceID)
	return ok, fail
}

// acmeRenewEnforcer renews TLS certs for services with acme_enabled when
// notAfter is within 30 days. Interval: 12h.
func (s *Server) acmeRenewEnforcer() {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	// First pass after short delay so panel finishes boot.
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-s.stopAll:
			return
		case <-s.stopACME:
			return
		case <-timer.C:
			s.runACMERenewPass()
		case <-ticker.C:
			s.runACMERenewPass()
		}
	}
}

func (s *Server) runACMERenewPass() {
	list, err := db.ListProxyServices(s.DB)
	if err != nil {
		return
	}
	for _, svc := range list {
		if svc == nil || !strings.EqualFold(svc.Protocol, "vless") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(nonzeroRaw(svc.ConfigJSON), &m); err != nil {
			continue
		}
		enabled, _ := m["acme_enabled"].(bool)
		if !enabled {
			if s, ok := m["acme_enabled"].(string); !ok || !strings.EqualFold(s, "true") {
				continue
			}
		}
		domain, _ := m["acme_domain"].(string)
		if domain == "" {
			domain, _ = m["server_name"].(string)
		}
		if domain == "" {
			continue
		}
		// Skip if still valid for > 30 days.
		if na, _ := m["acme_not_after"].(string); na != "" {
			if t, err := time.Parse(time.RFC3339, na); err == nil {
				if time.Until(t) > 30*24*time.Hour {
					continue
				}
			}
		} else if certPEM, _ := m["cert_pem"].(string); certPEM != "" {
			info := proxysvc.InspectTLSCert(certPEM, "", domain)
			if info.DaysLeft > 30 && !info.Expired {
				continue
			}
		}
		log.Printf("acme: renewing cert for service %d domain %s", svc.ID, domain)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		res, err := s.issueACMEForDomain(ctx, domain, "", false)
		cancel()
		if err != nil {
			log.Printf("acme: renew failed service=%d: %v", svc.ID, err)
			_ = s.patchProxyConfigACMEMeta(svc, domain, "", nil, err.Error())
			continue
		}
		var vc proxysvc.VLESSConfig
		_ = json.Unmarshal(nonzeroRaw(svc.ConfigJSON), &vc)
		vc.Security = "tls"
		vc.ServerName = domain
		cfgBytes, err := mergeACMEIntoConfig(svc.ConfigJSON, &vc, res)
		if err != nil {
			continue
		}
		if err := db.UpdateProxyService(s.DB, svc.ID, svc.Name, cfgBytes, svc.SubVisible); err != nil {
			log.Printf("acme: save failed service=%d: %v", svc.ID, err)
			continue
		}
		// Republish instances so agents get new cert.
		nodeIDs := proxyInstanceNodeIDs(s.DB, svc.ID)
		ok, fail := s.publishProxyToNodes(svc.ID, nodeIDs, false)
		log.Printf("acme: renewed service=%d domain=%s publish ok=%d fail=%d", svc.ID, domain, ok, fail)
	}
}

func proxyInstanceNodeIDs(d *sql.DB, serviceID int64) []int64 {
	inst, err := db.ListProxyInstances(d, serviceID)
	if err != nil {
		return nil
	}
	var out []int64
	seen := map[int64]bool{}
	for _, it := range inst {
		if it == nil || it.NodeID <= 0 || seen[it.NodeID] {
			continue
		}
		seen[it.NodeID] = true
		out = append(out, it.NodeID)
	}
	return out
}
