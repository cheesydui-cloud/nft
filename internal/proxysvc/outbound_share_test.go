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

func TestParseSSShare_Forms(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		meth string
		pass string
		host string
		port int
	}{
		{
			name: "sip002-std-b64",
			uri:  "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass123")) + "@1.2.3.4:8388#n",
			meth: "aes-256-gcm", pass: "pass123", host: "1.2.3.4", port: 8388,
		},
		{
			name: "sip002-rawurl",
			uri:  "ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pwd")) + "@10.1.2.3:12345",
			meth: "chacha20-ietf-poly1305", pass: "pwd", host: "10.1.2.3", port: 12345,
		},
		{
			name: "plain-userinfo",
			uri:  "ss://aes-256-gcm:s3cret@9.9.9.9:553#gan1",
			meth: "aes-256-gcm", pass: "s3cret", host: "9.9.9.9", port: 553,
		},
		{
			name: "percent-encoded-colon",
			uri:  "ss://aes-256-gcm%3Amypass@8.8.8.8:443",
			meth: "aes-256-gcm", pass: "mypass", host: "8.8.8.8", port: 443,
		},
		{
			name: "percent-encoded-password",
			uri:  "ss://aes-128-gcm:p%40ss%3Aw@7.7.7.7:1080",
			meth: "aes-128-gcm", pass: "p@ss:w", host: "7.7.7.7", port: 1080,
		},
		{
			name: "legacy-whole-b64",
			uri:  "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:leg@6.6.6.6:9999")) + "#L",
			meth: "aes-256-gcm", pass: "leg", host: "6.6.6.6", port: 9999,
		},
		{
			name: "trailing-slash",
			uri:  "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:x")) + "@5.5.5.5:1/",
			meth: "aes-256-gcm", pass: "x", host: "5.5.5.5", port: 1,
		},
		{
			name: "2022-blake3",
			uri:  "ss://2022-blake3-aes-128-gcm:base64pwd@4.4.4.4:20000",
			meth: "2022-blake3-aes-128-gcm", pass: "base64pwd", host: "4.4.4.4", port: 20000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, p, h, port, err := parseSSShare(tc.uri)
			if err != nil {
				t.Fatalf("parse: %v uri=%s", err, tc.uri)
			}
			if m != tc.meth || p != tc.pass || h != tc.host || port != tc.port {
				t.Fatalf("got %q %q %s:%d want %q %q %s:%d", m, p, h, port, tc.meth, tc.pass, tc.host, tc.port)
			}
		})
	}
}

func TestParseSSShare_RedactedRejected(t *testing.T) {
	_, _, _, _, err := parseSSShare("ss://***@1.2.3.4:8388")
	if err == nil {
		t.Fatal("expected error on redacted ss uri")
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
