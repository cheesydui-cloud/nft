package server

import (
	"encoding/base64"
	"strings"
	"testing"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

// ruleUsesProtocolEntry for the user's form: proxy_service + single hop + direct exit.
func TestRuleUsesProtocolEntry_ProxyPlusDirectLanding(t *testing.T) {
	r := &db.Rule{
		ProxyServiceID: 1,
		ExitType:       "direct",
		ExitHost:       "203.0.113.55",
		ExitPort:       553,
	}
	if !ruleUsesProtocolEntry(r) {
		t.Fatal("proxy_service + single hop must use protocol entry even for direct exit")
	}
	// multi-hop still L4
	r.ViaNodeIDs = []int64{2}
	if ruleUsesProtocolEntry(r) {
		t.Fatal("multi-hop must stay L4")
	}
}

// classifyExit: entry protocol wins over landing SS tag for copy/QR.
func TestClassifyExit_ProtocolEntryDirectSSLanding(t *testing.T) {
	it := ruleListItem{
		Rule: &db.Rule{
			NodeID: 7, ProxyServiceID: 3,
			ExitHost: "203.0.113.55", ExitPort: 553,
			ExitType: "direct",
		},
		Entry: "nb.jp.example:26915",
		Exit:  "203.0.113.55:553",
	}
	share := "vless://uuid@1.2.3.4:443?security=reality&sni=a.com#测试1"
	it.classifyExitWithShare(nil, true, "vless", share, "测试1")
	if it.LandingProtocol != "vless" {
		t.Fatalf("landing_protocol=%q want vless (entry), not ss", it.LandingProtocol)
	}
	if !strings.HasPrefix(it.RelayURI, "vless://uuid@nb.jp.example:26915") {
		t.Fatalf("relay_uri=%q", it.RelayURI)
	}
}

// Deploy payload shape: when exit_uri is ss://, OutboundShareURI must be set
// (verified via BuildXray path used by agent).
func TestDeployOutbound_ShareFromExitURI(t *testing.T) {
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:secret"))
	share := "ss://" + userinfo + "@203.0.113.55:553#gan1"
	if !proxysvc.IsProxyShareURI(share) {
		t.Fatal("ss share not recognized")
	}
	ob, err := proxysvc.BuildXrayOutboundFromShareURI(share, "sk5")
	if err != nil {
		t.Fatal(err)
	}
	if ob["protocol"] != "shadowsocks" {
		t.Fatalf("protocol=%v", ob["protocol"])
	}
}
