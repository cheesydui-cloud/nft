package proxysvc

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Simulates the panel rule from the user report:
//
//	entry: VLESS proxy service on port 26915
//	exit:  SS landing node (ss://method:pass@host:port)
//
// Expectation (3x-ui style): xray config has vless inbound + shadowsocks outbound,
// NOT freedom redirect to bare host:port.
func TestUserScenario_VLESSEntry_SSLanding(t *testing.T) {
	priv, _ := GenerateRealityKeyPair()
	entryCfg := mustJSON(map[string]any{
		"uuid":        "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"security":    "reality",
		"network":     "tcp",
		"server_name": "www.cloudflare.com",
		"private_key": priv,
		"short_id":    "abcd",
		"flow":        "xtls-rprx-vision",
	})
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:gan1-secret"))
	share := "ss://" + userinfo + "@203.0.113.55:553#gan1-553"

	cfg, err := BuildXrayVLESSConfigOpts(26915, entryCfg, &OutboundSOCKS{ShareURI: share})
	if err != nil {
		t.Fatalf("build with share: %v", err)
	}
	assertVLESSIn_SSOut(t, cfg, 26915, "203.0.113.55", "aes-256-gcm", "gan1-secret")

	cfgBad, err := BuildXrayVLESSConfigOpts(26915, entryCfg, &OutboundSOCKS{
		RedirectHost: "203.0.113.55",
		RedirectPort: 553,
	})
	if err != nil {
		t.Fatalf("build redirect: %v", err)
	}
	sBad := string(cfgBad)
	if !strings.Contains(sBad, "redirect") {
		t.Fatalf("control: bare redirect should contain redirect")
	}
	if strings.Contains(sBad, "shadowsocks") {
		t.Fatalf("bare redirect must not invent ss")
	}
	if strings.Contains(string(cfg), `"redirect"`) {
		t.Fatalf("share path must not use freedom redirect:\n%s", string(cfg)[:800])
	}
}

func assertVLESSIn_SSOut(t *testing.T, cfg []byte, listenPort int, ssHost, method, pass string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	inbounds, _ := m["inbounds"].([]any)
	if len(inbounds) < 1 {
		t.Fatal("no inbounds")
	}
	in := inbounds[0].(map[string]any)
	if in["protocol"] != "vless" {
		t.Fatalf("inbound protocol=%v want vless", in["protocol"])
	}
	if int(in["port"].(float64)) != listenPort {
		t.Fatalf("listen port=%v want %d", in["port"], listenPort)
	}
	outs, _ := m["outbounds"].([]any)
	var foundSS bool
	for _, o := range outs {
		om := o.(map[string]any)
		if om["protocol"] == "shadowsocks" {
			foundSS = true
			s := outboundToJSON(om)
			if !strings.Contains(s, ssHost) || !strings.Contains(s, method) || !strings.Contains(s, pass) {
				t.Fatalf("ss outbound incomplete: %s", s)
			}
			if om["tag"] != "sk5" {
				t.Fatalf("ss tag=%v want sk5", om["tag"])
			}
		}
	}
	if !foundSS {
		t.Fatalf("no shadowsocks outbound in:\n%s", string(cfg)[:1000])
	}
	routing, _ := m["routing"].(map[string]any)
	if routing == nil {
		t.Fatal("missing routing")
	}
	rules, _ := routing["rules"].([]any)
	if len(rules) < 1 {
		t.Fatal("missing routing rules")
	}
	r0 := rules[0].(map[string]any)
	if r0["outboundTag"] != "sk5" {
		t.Fatalf("routing outboundTag=%v", r0["outboundTag"])
	}
}

func TestIsProxyShareURI(t *testing.T) {
	if !IsProxyShareURI("ss://YWVz@1.1.1.1:1#x") {
		t.Fatal("ss")
	}
	if !IsProxyShareURI("vless://u@h:443?security=none") {
		t.Fatal("vless")
	}
	if IsProxyShareURI("203.0.113.55:553") {
		t.Fatal("bare host:port is not share")
	}
	if IsProxyShareURI("") {
		t.Fatal("empty")
	}
}

func TestParseSSShare_SIP002(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pwd"))
	uri := "ss://" + userinfo + "@10.1.2.3:12345#name"
	method, pass, host, port, err := parseSSShare(uri)
	if err != nil {
		t.Fatal(err)
	}
	if method != "chacha20-ietf-poly1305" || pass != "pwd" || host != "10.1.2.3" || port != 12345 {
		t.Fatalf("got %s %s %s %d", method, pass, host, port)
	}
}
