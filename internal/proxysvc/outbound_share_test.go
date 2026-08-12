package proxysvc

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildXrayOutboundFromShareURI_SS(t *testing.T) {
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass123"))
	uri := "ss://" + userinfo + "@1.2.3.4:8388#gan1"
	ob, err := BuildXrayOutboundFromShareURI(uri, "sk5")
	if err != nil {
		t.Fatal(err)
	}
	if ob["protocol"] != "shadowsocks" {
		t.Fatalf("protocol=%v", ob["protocol"])
	}
	s := outboundToJSON(ob)
	if !strings.Contains(s, "1.2.3.4") || !strings.Contains(s, "aes-256-gcm") || !strings.Contains(s, "pass123") {
		t.Fatalf("outbound missing fields: %s", s)
	}
}

func TestBuildXrayOutboundFromShareURI_VLESS(t *testing.T) {
	uri := "vless://11111111-2222-3333-4444-555555555555@9.9.9.9:443?security=reality&sni=a.com&pbk=abc&sid=01&type=tcp#n"
	ob, err := BuildXrayOutboundFromShareURI(uri, "sk5")
	if err != nil {
		t.Fatal(err)
	}
	if ob["protocol"] != "vless" {
		t.Fatalf("protocol=%v", ob["protocol"])
	}
	s := outboundToJSON(ob)
	if !strings.Contains(s, "9.9.9.9") || !strings.Contains(s, "reality") {
		t.Fatalf("outbound: %s", s)
	}
}

func TestBuildXrayVLESSConfigShareOutbound(t *testing.T) {
	priv, _ := GenerateRealityKeyPair()
	cfgRaw := mustJSON(map[string]any{
		"uuid":        "11111111-2222-3333-4444-555555555555",
		"security":    "reality",
		"network":     "tcp",
		"server_name": "www.cloudflare.com",
		"private_key": priv,
	})
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:s3cret"))
	share := "ss://" + userinfo + "@10.0.0.5:8388#ss"
	cfg, err := BuildXrayVLESSConfigOpts(26915, cfgRaw, &OutboundSOCKS{ShareURI: share})
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, "shadowsocks") {
		t.Fatalf("expected ss outbound: %s", s[:600])
	}
	if !strings.Contains(s, "10.0.0.5") {
		t.Fatalf("expected ss host: %s", s[:600])
	}
	if strings.Contains(s, `"redirect"`) {
		t.Fatalf("share outbound should not use freedom redirect")
	}
}

func TestInjectSingBoxShareOutbound_SS(t *testing.T) {
	base, err := BuildSingBoxSSConfig(8388, mustJSON(map[string]any{
		"method": "aes-128-gcm", "password": "in",
	}))
	if err != nil {
		t.Fatal(err)
	}
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:outpass"))
	share := "ss://" + userinfo + "@8.8.8.8:1234"
	out, err := InjectSingBoxShareOutbound(base, share)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "share-out") || !strings.Contains(s, "8.8.8.8") {
		t.Fatalf("%s", s)
	}
}
