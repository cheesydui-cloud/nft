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

// protocolEntryProtocols are proxy-service protocols that can own the rule
// entry port as a real inbound (client speaks that protocol; core outbounds
// through exit_uri SOCKS as an open proxy).
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
// Product (链式 代理 tab + SK5 出口):
//
//	Client --(入口协议 VLESS/SS/…)--> entry core inbound
//	     --(SOCKS5 open proxy via exit_uri)--> Internet
//
// CONNECT 目标 (exit_host:exit_port) is NOT used as a fixed tunnel for the
// protocol plane: clients need full proxy semantics (DNS/SNI destinations).
// Fixed CONNECT remains only for the legacy L4 + ExitProxy path.
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
	return strings.EqualFold(strings.TrimSpace(r.ExitType), "socks5") &&
		strings.TrimSpace(r.ExitURI) != ""
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
		return fmt.Errorf("协议入口暂不支持 %s", svc.Protocol)
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
	forceCore := false
	if proto == "vless" && proxysvc.NeedsVLESSEnc(cfg) {
		forceCore = true
	}
	if err := s.ensureCoreOnNodeForce(node, svc.Protocol, forceCore); err != nil {
		return err
	}
	shareHost := proxysvc.ShareHostFromConfig(cfg)
	if shareHost == "" {
		shareHost = firstNonEmpty(node.RelayHost, node.Address)
		if h, _, e := splitHostPortLoose(shareHost); e == nil && h != "" {
			shareHost = h
		}
	}
	core := strings.TrimSpace(svc.Core)
	if core == "" {
		core = protocolEntryCore(proto)
	}
	instID := ruleCoreInstanceID(r.ID)
	if s.Hub == nil || !s.Hub.IsOnline(r.NodeID) {
		return fmt.Errorf("入口节点离线，无法部署协议入口")
	}
	// Open SOCKS outbound: do NOT set OutboundRedirect* — client destinations
	// must pass through the SK5 (otherwise "测通但没网").
	ack, err := s.Hub.SendProxyServiceApply(r.NodeID, wsproto.ProxyServiceApply{
		InstanceID:    instID,
		ServiceID:     svc.ID,
		Protocol:      svc.Protocol,
		Core:          core,
		ListenPort:    r.EntryListenPort,
		ShareHost:     shareHost,
		Name:          fmt.Sprintf("%s#r%d", svc.Name, r.ID),
		Config:        cfg,
		OutboundSocks: strings.TrimSpace(r.ExitURI),
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
func (s *Server) syncRuleProtocolPlane(r *db.Rule) {
	if r == nil {
		return
	}
	if !ruleUsesProtocolEntry(r) {
		s.stopRuleProtocolPlane(r)
		return
	}
	svc, err := db.GetProxyService(s.DB, r.ProxyServiceID)
	if err != nil || svc == nil || !protocolEntrySupported(svc.Protocol) {
		s.stopRuleProtocolPlane(r)
		return
	}
	if err := s.deployRuleProtocolPlane(r); err != nil {
		log.Printf("deploy rule protocol plane rule=%d: %v", r.ID, err)
	}
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
