package server

import (
	"fmt"
	"log"
	"strings"

	"nft/internal/db"
	"nft/internal/proxysvc"
	"nft/internal/wsproto"
)

// ruleCoreInstanceID maps a panel rule id onto the agent core instance id space.
// Real proxy_service_instances use ordinary SQLite rowids (small); rule-scoped
// planes live in a high band so they never collide.
func ruleCoreInstanceID(ruleID int64) int64 {
	if ruleID <= 0 {
		return 0
	}
	return 2_000_000_000 + ruleID
}

// ruleUsesProtocolEntry reports whether the rule should run a real protocol
// inbound (VLESS/SS/…) on the entry port instead of raw L4 nft/userspace.
// Product: 代理 tab pick + SOCKS5 exit = client speaks the proxy-service
// protocol; core outbound CONNECTs through exit_uri to exit_host:exit_port.
func ruleUsesProtocolEntry(r *db.Rule) bool {
	if r == nil {
		return false
	}
	if r.ProxyServiceID <= 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.ExitType), "socks5") &&
		strings.TrimSpace(r.ExitURI) != ""
}

// deployRuleProtocolPlane pushes a rule-scoped core config to the entry node.
// Currently VLESS (xray) only; other protocols fall back to L4+ExitProxy.
func (s *Server) deployRuleProtocolPlane(r *db.Rule) error {
	if s == nil || r == nil || !ruleUsesProtocolEntry(r) {
		return nil
	}
	svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
	if err != nil || svc == nil {
		return fmt.Errorf("代理服务 #%d 不存在", r.ProxyServiceID)
	}
	proto := strings.ToLower(strings.TrimSpace(svc.Protocol))
	if proto != "vless" {
		// Non-VLESS: keep legacy L4 + ExitProxy; caller should not skip L4.
		return fmt.Errorf("协议入口暂仅支持 VLESS（当前 %s）", svc.Protocol)
	}
	if r.EntryListenPort <= 0 {
		return fmt.Errorf("规则入口端口未分配")
	}
	// Multi-hop L4 chains are not protocol-entry end-to-end; still allow deploy
	// on entry when vias empty. When vias present, protocol plane would bypass
	// intermediate nodes — refuse and leave L4+ExitProxy.
	if len(r.ViaNodeIDs) > 0 {
		return fmt.Errorf("VLESS 协议入口暂不支持途经节点，请用单跳")
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
	if err := s.ensureCoreOnNodeForce(node, svc.Protocol, proxysvc.NeedsVLESSEnc(cfg)); err != nil {
		return err
	}
	shareHost := proxysvc.ShareHostFromConfig(cfg)
	if shareHost == "" {
		shareHost = firstNonEmpty(node.RelayHost, node.Address)
		if h, _, e := splitHostPortLoose(shareHost); e == nil && h != "" {
			shareHost = h
		}
	}
	instID := ruleCoreInstanceID(r.ID)
	if s.Hub == nil || !s.Hub.IsOnline(r.NodeID) {
		return fmt.Errorf("入口节点离线，无法部署协议入口")
	}
	ack, err := s.Hub.SendProxyServiceApply(r.NodeID, wsproto.ProxyServiceApply{
		InstanceID:           instID,
		ServiceID:            svc.ID,
		Protocol:             svc.Protocol,
		Core:                 svc.Core,
		ListenPort:           r.EntryListenPort,
		ShareHost:            shareHost,
		Name:                 fmt.Sprintf("%s#r%d", svc.Name, r.ID),
		Config:               cfg,
		OutboundSocks:        strings.TrimSpace(r.ExitURI),
		OutboundRedirectHost: strings.TrimSpace(r.ExitHost),
		OutboundRedirectPort: r.ExitPort,
	})
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
	// Always try stop when proxy_service_id was set (may have been protocol plane).
	if r.ProxyServiceID <= 0 && !strings.EqualFold(r.ExitType, "socks5") {
		return
	}
	proto := "vless"
	core := "xray"
	if svc, err := db.GetProxyService(s.DB, r.ProxyServiceID); err == nil && svc != nil {
		proto = svc.Protocol
		core = svc.Core
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
func (s *Server) syncRuleProtocolPlane(r *db.Rule) {
	if r == nil {
		return
	}
	if !ruleUsesProtocolEntry(r) {
		s.stopRuleProtocolPlane(r)
		return
	}
	if len(r.ViaNodeIDs) > 0 {
		// Fall back to L4 + ExitProxy for multi-hop.
		s.stopRuleProtocolPlane(r)
		return
	}
	// Only VLESS is implemented; if service is not VLESS, leave L4 path alone.
	svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
	if err != nil || svc == nil || !strings.EqualFold(svc.Protocol, "vless") {
		return
	}
	if err := s.deployRuleProtocolPlane(r); err != nil {
		log.Printf("deploy rule protocol plane rule=%d: %v", r.ID, err)
	}
}

// protocolEntrySupported is true when the proxy service can own the entry port.
func protocolEntrySupported(svc *db.ProxyService) bool {
	if svc == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(svc.Protocol), "vless")
}

// resyncProtocolPlanesOnNode re-deploys VLESS+SK5 protocol planes whose entry
// node is nodeID (e.g. after agent reconnect / ruleset push).
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
		if err != nil || svc == nil || !strings.EqualFold(svc.Protocol, "vless") {
			continue
		}
		if err := s.deployRuleProtocolPlane(r); err != nil {
			log.Printf("resync protocol plane rule=%d node=%d: %v", r.ID, nodeID, err)
		}
	}
}
