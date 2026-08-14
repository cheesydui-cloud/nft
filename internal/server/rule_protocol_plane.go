package server

import (
	"fmt"
	"log"
	"strings"

	"nft/internal/db"
	"nft/internal/proxysvc"
	"nft/internal/wsproto"
)

// ruleCoreInstanceBase is the agent instance-id band for rule-scoped protocol
// planes. Real proxy_service_instances use ordinary SQLite rowids (small).
const ruleCoreInstanceBase int64 = 2_000_000_000

// ruleCoreInstanceID maps a panel rule id onto the agent core instance id space.
func ruleCoreInstanceID(ruleID int64) int64 {
	if ruleID <= 0 {
		return 0
	}
	return ruleCoreInstanceBase + ruleID
}

// protocolEntryProtocols are proxy-service protocols that can own the rule
// entry port as a real inbound (client speaks that protocol; core outbounds
// according to exit_type — open SOCKS or freedom tunnel).
//
// Model mirrors 3x-ui outbound chaining:
//
//	Client --(entry protocol)--> core inbound
//	     --(routing)--> socks|freedom outbound
func protocolEntryProtocols() map[string]bool {
	return map[string]bool{
		"vless":       true,
		"shadowsocks": true,
		"ss":          true,
		"socks5":      true,
		"socks":       true,
		"anytls":      true,
		"naive":       true,
		"naiveproxy":  true,
	}
}

// ruleUsesProtocolEntry reports whether the rule should run a real protocol
// inbound on the entry port instead of raw L4 nft/userspace.
//
// 3x-ui style (链式 + 代理 tab, single hop):
//
//	Client --(入口协议 VLESS/SS/…)--> entry core inbound
//	  exit_type=socks5  → open SOCKS outbound via exit_uri (client destinations pass through)
//	  exit_type=direct  → freedom redirect to exit_host:exit_port (fixed tunnel)
//
// Multi-hop stays L4 (via chain between physical nodes).
// Plain SK5 without proxy_service_id stays L4 + ExitProxy CONNECT.
func ruleUsesProtocolEntry(r *db.Rule) bool {
	if r == nil {
		return false
	}
	if r.ProxyServiceID <= 0 {
		return false
	}
	if len(r.ViaNodeIDs) > 0 {
		return false // multi-hop stays L4
	}
	return true
}

func protocolEntryCore(proto string) string {
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case "vless":
		return "xray"
	case "mieru":
		return "mieru"
	default:
		return "sing-box"
	}
}

func protocolEntrySupported(proto string) bool {
	return protocolEntryProtocols()[strings.ToLower(strings.TrimSpace(proto))]
}

// deployRuleProtocolPlane pushes a rule-scoped core config to the entry node.
// Returns a user-facing error when deploy cannot complete (caller should surface it).
func (s *Server) deployRuleProtocolPlane(r *db.Rule) error {
	if s == nil || r == nil || !ruleUsesProtocolEntry(r) {
		return nil
	}
	svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
	if err != nil || svc == nil {
		return fmt.Errorf("代理服务 #%d 不存在", r.ProxyServiceID)
	}
	proto := strings.ToLower(strings.TrimSpace(svc.Protocol))
	if !protocolEntrySupported(proto) {
		return fmt.Errorf("协议入口暂不支持 %s（请用 VLESS / SS / SOCKS5 / AnyTLS / Naive）", svc.Protocol)
	}
	if r.EntryListenPort <= 0 {
		return fmt.Errorf("规则入口端口未分配")
	}
	node, err := db.GetNode(s.DB, r.NodeID)
	if err != nil || node == nil {
		return fmt.Errorf("入口节点不存在")
	}
	cfg := svc.ConfigJSON
	cfg, err = proxysvc.EnsureSecrets(svc.Protocol, cfg)
	if err != nil {
		return err
	}
	cfg, err = s.resolveProxyConfigCertID(cfg)
	if err != nil {
		return err
	}
	if err := validateProxyConfigForPublish(svc.Protocol, cfg); err != nil {
		return err
	}
	if r.OwnerID.Valid && r.OwnerID.Int64 > 0 {
		if overlaid, oerr := s.overlayProxyConfigForUser(svc, cfg, r.OwnerID.Int64); oerr == nil {
			cfg = overlaid
		} else {
			return oerr
		}
	}
	forceCore := false
	if proto == "vless" && proxysvc.NeedsVLESSEnc(cfg) {
		forceCore = true
	}
	if err := s.ensureCoreOnNodeForce(node, svc.Protocol, forceCore); err != nil {
		return err
	}
	shareHost := proxysvc.ShareHostFromConfig(cfg)
	if shareHost == "" {
		shareHost = defaultProxyShareHost(node)
	}
	core := strings.TrimSpace(svc.Core)
	if core == "" {
		core = protocolEntryCore(proto)
	}
	instID := ruleCoreInstanceID(r.ID)
	if s.Hub == nil || !s.Hub.IsOnline(r.NodeID) {
		return fmt.Errorf("入口节点离线，无法部署协议入口")
	}

	// 3x-ui-style outbound selection:
	//   socks5 → open SOCKS (client destinations pass through exit_uri)
	//   direct + landing share (ss/vless/…) → real protocol outbound
	//   direct + bare host:port only → freedom redirect (L4-like tunnel)
	apply := wsproto.ProxyServiceApply{
		InstanceID:    instID,
		ServiceID:     svc.ID,
		Protocol:      svc.Protocol,
		Core:          core,
		ListenPort:    r.EntryListenPort,
		ShareHost:     shareHost,
		Name:          fmt.Sprintf("%s#r%d", svc.Name, r.ID),
		Config:        cfg,
		BlockEgressV4: node.RelayV4Disabled,
		BlockEgressV6: node.RelayV6Disabled,
	}
	exitType := strings.ToLower(strings.TrimSpace(r.ExitType))
	if exitType == "socks5" {
		uri := strings.TrimSpace(r.ExitURI)
		if uri == "" {
			return fmt.Errorf("协议入口 + SK5 出口需要填写 exit_uri（socks5://…）")
		}
		// Open SOCKS: do NOT set OutboundRedirect* (that forced CONNECT to a
		// single host and caused "测通但没网").
		apply.OutboundSocks = uri
	} else {
		host := strings.TrimSpace(r.ExitHost)
		if host == "" || r.ExitPort <= 0 {
			return fmt.Errorf("协议入口 + 直连出口需要填写落地 host:port")
		}
		// Prefer stored exit_uri when it is a usable proxy share (ss:// / vless:// …).
		// List/detail redacts secrets as ss://***@host:port — never deploy that.
		// Else resolve full credentials from node_repo / user landing ledger.
		shareURI := strings.TrimSpace(r.ExitURI)
		if shareURI != "" && (isRedactedExitURI(shareURI) || !proxysvc.IsProxyShareURI(shareURI)) {
			shareURI = ""
		}
		if shareURI != "" {
			if _, err := proxysvc.BuildXrayOutboundFromShareURI(shareURI, "sk5"); err != nil {
				if _, err2 := proxysvc.BuildSingBoxOutboundFromShareURI(shareURI, "share-out"); err2 != nil {
					// Stored URI unusable (corrupt / partial) — fall back to warehouse.
					log.Printf("rule %d exit_uri share unusable (%v), resolve landing", r.ID, err)
					shareURI = ""
				}
			}
		}
		if shareURI == "" {
			shareURI = s.resolveLandingShareURI(r)
		}
		if shareURI != "" {
			// Real protocol outbound (VLESS entry → SS exit, etc.).
			if _, err := proxysvc.BuildXrayOutboundFromShareURI(shareURI, "sk5"); err != nil {
				// Still try sing-box path for ss/socks; validate loosely.
				if _, err2 := proxysvc.BuildSingBoxOutboundFromShareURI(shareURI, "share-out"); err2 != nil {
					return fmt.Errorf("落地分享链无法作为出站: %v", err)
				}
			}
			apply.OutboundShareURI = shareURI
		} else {
			// No share credentials: bare TCP tunnel (only works if exit speaks raw TCP).
			// SS/VLESS landings will be dead without warehouse URI — warn in log.
			log.Printf("rule %d protocol plane: no share URI for %s:%d, freedom redirect only", r.ID, host, r.ExitPort)
			apply.OutboundRedirectHost = host
			apply.OutboundRedirectPort = r.ExitPort
		}
	}

	ack, err := s.Hub.SendProxyServiceApply(r.NodeID, apply)
	if err != nil {
		return err
	}
	if !ack.OK {
		msg := ack.Error
		if msg == "" {
			msg = "agent 部署失败"
		}
		return fmt.Errorf("%s", msg)
	}
	if ack.DryRun {
		return fmt.Errorf("节点未启动核心进程: %s", ack.Error)
	}
	return nil
}

// stopRuleProtocolPlane tears down a rule-scoped core on the entry node.
func (s *Server) stopRuleProtocolPlane(r *db.Rule) {
	if s == nil || r == nil || r.ID <= 0 || r.NodeID <= 0 {
		return
	}
	if r.ProxyServiceID <= 0 {
		return
	}
	proto := "vless"
	core := "xray"
	if svc, err := db.GetProxyService(s.DB, r.ProxyServiceID); err == nil && svc != nil {
		proto = svc.Protocol
		core = strings.TrimSpace(svc.Core)
		if core == "" {
			core = protocolEntryCore(proto)
		}
	}
	if s.Hub == nil || !s.Hub.IsOnline(r.NodeID) {
		return
	}
	_, err := s.Hub.SendProxyServiceStop(r.NodeID, wsproto.ProxyServiceStop{
		InstanceID: ruleCoreInstanceID(r.ID),
		Protocol:   proto,
		Core:       core,
	})
	if err != nil {
		log.Printf("stop rule protocol plane rule=%d node=%d: %v", r.ID, r.NodeID, err)
	}
}

// syncRuleProtocolPlane deploys or stops based on current rule fields.
// Returns deploy error so API can surface it (previously only logged).
func (s *Server) syncRuleProtocolPlane(r *db.Rule) error {
	if r == nil {
		return nil
	}
	if !ruleUsesProtocolEntry(r) {
		s.stopRuleProtocolPlane(r)
		return nil
	}
	svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
	if err != nil || svc == nil || !protocolEntrySupported(svc.Protocol) {
		s.stopRuleProtocolPlane(r)
		if err != nil {
			return fmt.Errorf("代理服务不可用: %w", err)
		}
		if svc == nil {
			return fmt.Errorf("代理服务不存在")
		}
		return fmt.Errorf("协议入口暂不支持 %s", svc.Protocol)
	}
	if err := s.deployRuleProtocolPlane(r); err != nil {
		log.Printf("deploy rule protocol plane rule=%d: %v", r.ID, err)
		return err
	}
	return nil
}

// resyncProtocolPlanesOnNode re-deploys protocol planes whose entry node is nodeID.
func (s *Server) resyncProtocolPlanesOnNode(nodeID int64) {
	if s == nil || nodeID <= 0 {
		return
	}
	rules, err := db.ListAllRules(s.DB)
	if err != nil {
		return
	}
	for _, r := range rules {
		if r == nil || r.NodeID != nodeID || r.Disabled {
			continue
		}
		if !ruleUsesProtocolEntry(r) {
			continue
		}
		svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
		if err != nil || svc == nil || !protocolEntrySupported(svc.Protocol) {
			continue
		}
		if err := s.deployRuleProtocolPlane(r); err != nil {
			log.Printf("resync protocol plane rule=%d node=%d: %v", r.ID, nodeID, err)
		}
	}
}

// resolveLandingShareURI looks up ss:// / vless:// credentials for the rule's
// exit host:port from node_repo, then any user's landing ledger, then the
// rule owner's landing index.
func (s *Server) resolveLandingShareURI(r *db.Rule) string {
	if s == nil || r == nil {
		return ""
	}
	host := strings.TrimSpace(r.ExitHost)
	if host == "" || r.ExitPort <= 0 {
		return ""
	}
	if e, err := db.FindNodeRepoByHostPort(s.DB, host, r.ExitPort); err == nil {
		if u := strings.TrimSpace(e.URI); proxysvc.IsProxyShareURI(u) {
			return u
		}
	}
	if uri, _, _, err := db.FindAnyLandingURIByHostPort(s.DB, host, r.ExitPort); err == nil {
		if proxysvc.IsProxyShareURI(uri) {
			return uri
		}
	}
	if r.OwnerID.Valid && r.OwnerID.Int64 > 0 {
		idx := s.landingIndexFromDB(r.OwnerID.Int64)
		for _, n := range idx {
			if n.Host == host && n.Port == r.ExitPort && proxysvc.IsProxyShareURI(n.URI) {
				return n.URI
			}
		}
	}
	return ""
}
