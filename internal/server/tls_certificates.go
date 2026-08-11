package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

// redactTLSCertificate clears PEMs for list/detail API responses.
func redactTLSCertificate(c *db.TLSCertificate) *db.TLSCertificate {
	if c == nil {
		return nil
	}
	out := *c
	out.CertConfigured = strings.TrimSpace(c.CertPEM) != ""
	out.KeyConfigured = strings.TrimSpace(c.KeyPEM) != ""
	out.CertPEM = ""
	out.KeyPEM = ""
	return &out
}

// apiListTLSCertificates GET /api/tls-certificates
func (s *Server) apiListTLSCertificates(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListTLSCertificates(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []*db.TLSCertificate{}
	}
	out := make([]*db.TLSCertificate, 0, len(list))
	for _, c := range list {
		if c == nil {
			continue
		}
		rc := redactTLSCertificate(c)
		if n, err := db.CountProxyServicesByCertID(s.DB, c.ID); err == nil {
			rc.RefCount = n
		}
		out = append(out, rc)
	}
	jsonOK(w, map[string]any{"certificates": out})
}

// apiGetTLSCertificate GET /api/tls-certificates/{id}
func (s *Server) apiGetTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	c, err := db.GetTLSCertificate(s.DB, id)
	if err != nil || c == nil {
		jsonErr(w, http.StatusNotFound, "证书不存在")
		return
	}
	rc := redactTLSCertificate(c)
	if n, err := db.CountProxyServicesByCertID(s.DB, c.ID); err == nil {
		rc.RefCount = n
	}
	info := proxysvc.InspectTLSCert(c.CertPEM, c.KeyPEM, c.Domain)
	jsonOK(w, map[string]any{"certificate": rc, "cert_info": info})
}

// apiCreateTLSCertificate POST /api/tls-certificates
// body modes:
//   { "mode": "upload", "name"?, "domain", "cert_pem", "key_pem", "note"? }
//   { "mode": "selfsigned", "name"?, "domain", "days"?, "note"? }
//   { "mode": "acme", "name"?, "domain", "email"?, "staging"?, "note"? }
func (s *Server) apiCreateTLSCertificate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode    string `json:"mode"` // upload | selfsigned | acme
		Name    string `json:"name"`
		Domain  string `json:"domain"`
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
		Note    string `json:"note"`
		Days    int    `json:"days"`
		Email   string `json:"email"`
		Staging bool   `json:"staging"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "upload"
	}
	domain := strings.TrimSpace(body.Domain)
	name := strings.TrimSpace(body.Name)
	note := strings.TrimSpace(body.Note)

	var (
		certPEM, keyPEM string
		source          string
		acmeEnabled     bool
		provider        string
		issuer          string
		notBefore       string
		notAfter        string
		fingerprint     string
		lastRenew       string
	)

	switch mode {
	case "upload":
		certPEM = strings.TrimSpace(body.CertPEM)
		keyPEM = strings.TrimSpace(body.KeyPEM)
		if certPEM == "" || keyPEM == "" {
			jsonErr(w, http.StatusBadRequest, "请粘贴证书与私钥 PEM")
			return
		}
		if err := proxysvc.ValidateTLSCertPair(certPEM, keyPEM, domain); err != nil {
			// Domain may be empty for multi-SAN upload; still require parse + key match.
			if domain != "" {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if err2 := proxysvc.ValidateTLSCertPair(certPEM, keyPEM, ""); err2 != nil {
				jsonErr(w, http.StatusBadRequest, err2.Error())
				return
			}
		}
		info := proxysvc.InspectTLSCert(certPEM, keyPEM, domain)
		if domain == "" && info.CN != "" {
			domain = info.CN
		}
		source = db.TLSCertSourceUpload
		issuer = firstNonEmpty(info.CN, "")
		notBefore = info.NotBefore
		notAfter = info.NotAfter
		fingerprint = info.Fingerprint

	case "selfsigned":
		if domain == "" {
			jsonErr(w, http.StatusBadRequest, "请填写域名（CN/SAN）")
			return
		}
		days := body.Days
		if days <= 0 {
			days = 365
		}
		if days > 825 {
			days = 825
		}
		cPEM, kPEM, info, err := proxysvc.GenerateSelfSignedTLS(domain, days)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		certPEM, keyPEM = cPEM, kPEM
		source = db.TLSCertSourceSelfSigned
		issuer = "self-signed"
		notBefore = info.NotBefore
		notAfter = info.NotAfter
		fingerprint = info.Fingerprint

	case "acme":
		if domain == "" {
			jsonErr(w, http.StatusBadRequest, "请填写域名")
			return
		}
		res, err := s.issueACMEForDomain(r.Context(), domain, body.Email, body.Staging)
		if err != nil {
			jsonErr(w, http.StatusBadGateway, err.Error())
			return
		}
		certPEM, keyPEM = res.CertPEM, res.KeyPEM
		source = db.TLSCertSourceACME
		acmeEnabled = true
		provider = "cloudflare-dns01"
		issuer = res.Issuer
		notBefore = res.NotBefore.UTC().Format(time.RFC3339)
		notAfter = res.NotAfter.UTC().Format(time.RFC3339)
		lastRenew = time.Now().UTC().Format(time.RFC3339)
		info := proxysvc.InspectTLSCert(certPEM, keyPEM, domain)
		fingerprint = info.Fingerprint

	default:
		jsonErr(w, http.StatusBadRequest, "mode 须为 upload / selfsigned / acme")
		return
	}

	if name == "" {
		name = domain
	}
	row, err := db.CreateTLSCertificate(s.DB, &db.TLSCertificate{
		Name:         name,
		Domain:       domain,
		CertPEM:      certPEM,
		KeyPEM:       keyPEM,
		Source:       source,
		ACMEEnabled:  acmeEnabled,
		ACMEProvider: provider,
		ACMEIssuer:   issuer,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		Fingerprint:  fingerprint,
		LastRenewAt:  lastRenew,
		Note:         note,
	})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u := userFromCtx(r.Context())
	if u != nil {
		db.WriteAudit(s.DB, u.ID, "tls_cert.create", fmt.Sprintf("%d", row.ID), domain+"/"+source)
	}
	info := proxysvc.InspectTLSCert(certPEM, keyPEM, domain)
	jsonOK(w, map[string]any{
		"ok":          true,
		"certificate": redactTLSCertificate(row),
		"cert_info":   info,
		"warning":     selfSignWarning(source),
	})
}

func selfSignWarning(source string) string {
	if source == db.TLSCertSourceSelfSigned {
		return "自签证书仅供调试；客户端需 allowInsecure / skip-verify"
	}
	return ""
}

// apiUpdateTLSCertificate PATCH /api/tls-certificates/{id}
// body: { "name"?, "domain"?, "note"?, "cert_pem"?, "key_pem"?, "acme_enabled"? }
func (s *Server) apiUpdateTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	c, err := db.GetTLSCertificate(s.DB, id)
	if err != nil || c == nil {
		jsonErr(w, http.StatusNotFound, "证书不存在")
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Domain      *string `json:"domain"`
		Note        *string `json:"note"`
		CertPEM     *string `json:"cert_pem"`
		KeyPEM      *string `json:"key_pem"`
		ACMEEnabled *bool   `json:"acme_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	name, domain, note := c.Name, c.Domain, c.Note
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
	}
	if body.Domain != nil {
		domain = strings.TrimSpace(*body.Domain)
	}
	if body.Note != nil {
		note = strings.TrimSpace(*body.Note)
	}
	if err := db.UpdateTLSCertificateMeta(s.DB, id, name, domain, note); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Optional PEM replace (upload overwrite).
	if body.CertPEM != nil || body.KeyPEM != nil {
		certPEM := c.CertPEM
		keyPEM := c.KeyPEM
		if body.CertPEM != nil && strings.TrimSpace(*body.CertPEM) != "" {
			certPEM = strings.TrimSpace(*body.CertPEM)
		}
		if body.KeyPEM != nil && strings.TrimSpace(*body.KeyPEM) != "" {
			keyPEM = strings.TrimSpace(*body.KeyPEM)
		}
		if err := proxysvc.ValidateTLSCertPair(certPEM, keyPEM, domain); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		info := proxysvc.InspectTLSCert(certPEM, keyPEM, domain)
		acme := c.ACMEEnabled
		if body.ACMEEnabled != nil {
			acme = *body.ACMEEnabled
		}
		source := c.Source
		if source == db.TLSCertSourceACME && (body.CertPEM != nil || body.KeyPEM != nil) {
			// Manual PEM replace → stop treating as pure ACME material unless still enabled.
			if !acme {
				source = db.TLSCertSourceUpload
			}
		}
		if err := db.UpdateTLSCertificateMaterial(s.DB, id, certPEM, keyPEM, source, acme,
			c.ACMEProvider, firstNonEmpty(c.ACMEIssuer, info.CN), info.NotBefore, info.NotAfter, info.Fingerprint,
			"", c.LastRenewAt); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else if body.ACMEEnabled != nil {
		// Toggle ACME flag only.
		if err := db.UpdateTLSCertificateMaterial(s.DB, id, c.CertPEM, c.KeyPEM, c.Source, *body.ACMEEnabled,
			c.ACMEProvider, c.ACMEIssuer, c.NotBefore, c.NotAfter, c.Fingerprint, c.LastError, c.LastRenewAt); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	c2, _ := db.GetTLSCertificate(s.DB, id)
	jsonOK(w, map[string]any{"ok": true, "certificate": redactTLSCertificate(c2)})
}

// apiDeleteTLSCertificate DELETE /api/tls-certificates/{id}?force=1
func (s *Server) apiDeleteTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	n, _ := db.CountProxyServicesByCertID(s.DB, id)
	if n > 0 && !force {
		jsonErr(w, http.StatusConflict, fmt.Sprintf("仍有 %d 个代理服务引用此证书，请先解绑或加 force=1", n))
		return
	}
	if err := db.DeleteTLSCertificate(s.DB, id); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u := userFromCtx(r.Context())
	if u != nil {
		db.WriteAudit(s.DB, u.ID, "tls_cert.delete", fmt.Sprintf("%d", id), "")
	}
	jsonOK(w, map[string]any{"ok": true, "unbound_refs": n})
}

// apiRenewTLSCertificate POST /api/tls-certificates/{id}/renew
// body: { "staging"?: bool, "republish"?: bool }
func (s *Server) apiRenewTLSCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "bad id")
		return
	}
	c, err := db.GetTLSCertificate(s.DB, id)
	if err != nil || c == nil {
		jsonErr(w, http.StatusNotFound, "证书不存在")
		return
	}
	var body struct {
		Staging   bool  `json:"staging"`
		Republish *bool `json:"republish"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	domain := strings.TrimSpace(c.Domain)
	if domain == "" {
		jsonErr(w, http.StatusBadRequest, "证书域名为空，无法续期")
		return
	}
	res, err := s.issueACMEForDomain(r.Context(), domain, "", body.Staging)
	if err != nil {
		_ = db.PatchTLSCertificateError(s.DB, id, err.Error())
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	info := proxysvc.InspectTLSCert(res.CertPEM, res.KeyPEM, domain)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := db.UpdateTLSCertificateMaterial(s.DB, id, res.CertPEM, res.KeyPEM, db.TLSCertSourceACME, true,
		"cloudflare-dns01", res.Issuer,
		res.NotBefore.UTC().Format(time.RFC3339),
		res.NotAfter.UTC().Format(time.RFC3339),
		info.Fingerprint, "", now); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Also push PEMs into any service that references this cert (keeps inline fallback in sync).
	republish := true
	if body.Republish != nil {
		republish = *body.Republish
	}
	svcIDs, _ := db.ListProxyServiceIDsByCertID(s.DB, id)
	pubOK, pubFail := 0, 0
	for _, sid := range svcIDs {
		if err := s.syncCertVaultIntoService(sid, id, res.CertPEM, res.KeyPEM, domain, res.Issuer,
			res.NotBefore.UTC().Format(time.RFC3339), res.NotAfter.UTC().Format(time.RFC3339)); err != nil {
			continue
		}
		if republish {
			ok, fail := s.publishProxyToNodes(sid, proxyInstanceNodeIDs(s.DB, sid), false)
			pubOK += ok
			pubFail += fail
		}
	}
	u := userFromCtx(r.Context())
	if u != nil {
		db.WriteAudit(s.DB, u.ID, "tls_cert.renew", fmt.Sprintf("%d", id), domain)
	}
	c2, _ := db.GetTLSCertificate(s.DB, id)
	jsonOK(w, map[string]any{
		"ok":            true,
		"certificate":   redactTLSCertificate(c2),
		"cert_info":     info,
		"services":      len(svcIDs),
		"publish_ok":    pubOK,
		"publish_fail":  pubFail,
		"not_after":     res.NotAfter.UTC().Format(time.RFC3339),
		"issuer":        res.Issuer,
	})
}

// syncCertVaultIntoService writes vault PEMs into a service config that references cert_id.
func (s *Server) syncCertVaultIntoService(serviceID, certID int64, certPEM, keyPEM, domain, issuer, notBefore, notAfter string) error {
	svc, err := db.GetProxyService(s.DB, serviceID)
	if err != nil || svc == nil {
		return fmt.Errorf("service %d not found", serviceID)
	}
	var m map[string]any
	if err := json.Unmarshal(nonzeroRaw(svc.ConfigJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	// Only touch services still pointing at this cert.
	if db.CertIDFromConfigJSON(svc.ConfigJSON) != certID {
		return nil
	}
	m["cert_id"] = certID
	m["cert_pem"] = certPEM
	m["key_pem"] = keyPEM
	if strings.TrimSpace(domain) != "" {
		if sn, _ := m["server_name"].(string); strings.TrimSpace(sn) == "" {
			m["server_name"] = domain
		}
	}
	// ACME metadata for UI (service-level).
	m["acme_enabled"] = true
	m["acme_provider"] = "cloudflare-dns01"
	m["acme_domain"] = domain
	m["acme_issuer"] = issuer
	m["acme_not_before"] = notBefore
	m["acme_not_after"] = notAfter
	m["acme_last_renew_at"] = time.Now().UTC().Format(time.RFC3339)
	m["acme_last_error"] = ""
	proto := strings.ToLower(strings.TrimSpace(svc.Protocol))
	if proto == "vless" {
		m["security"] = "tls"
	}
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return db.UpdateProxyService(s.DB, serviceID, svc.Name, out, svc.SubVisible)
}

// resolveProxyConfigCertID expands config_json.cert_id into cert_pem/key_pem for deploy.
// If both vault and inline PEMs exist, vault wins when cert_id > 0.
// Returns (resolvedConfig, error).
func (s *Server) resolveProxyConfigCertID(cfg json.RawMessage) (json.RawMessage, error) {
	certID := db.CertIDFromConfigJSON(cfg)
	if certID <= 0 {
		return cfg, nil
	}
	c, err := db.GetTLSCertificate(s.DB, certID)
	if err != nil || c == nil {
		return nil, fmt.Errorf("证书库 id=%d 不存在或已删除", certID)
	}
	if strings.TrimSpace(c.CertPEM) == "" || strings.TrimSpace(c.KeyPEM) == "" {
		return nil, fmt.Errorf("证书库 id=%d 缺少 PEM 材料", certID)
	}
	var m map[string]any
	if err := json.Unmarshal(nonzeroRaw(cfg), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	m["cert_id"] = certID
	m["cert_pem"] = c.CertPEM
	m["key_pem"] = c.KeyPEM
	// Fill server_name from vault domain when empty.
	if sn, _ := m["server_name"].(string); strings.TrimSpace(sn) == "" && strings.TrimSpace(c.Domain) != "" {
		m["server_name"] = c.Domain
	}
	// Surface vault ACME metadata for agents/UI that read service config.
	if c.NotAfter != "" {
		m["acme_not_after"] = c.NotAfter
	}
	if c.NotBefore != "" {
		m["acme_not_before"] = c.NotBefore
	}
	if c.ACMEIssuer != "" {
		m["acme_issuer"] = c.ACMEIssuer
	}
	if c.ACMEEnabled {
		m["acme_enabled"] = true
		m["acme_provider"] = c.ACMEProvider
		if c.Domain != "" {
			m["acme_domain"] = c.Domain
		}
	}
	return json.Marshal(m)
}

