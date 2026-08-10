package proxysvc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateRealityKeyPairValidLength(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	if priv == "" || pub == "" {
		t.Fatal("empty keys")
	}
	if priv == pub {
		t.Fatal("priv==pub")
	}
	// Raw base64url of 32 bytes is 43 chars.
	if len(priv) < 40 || len(pub) < 40 {
		t.Fatalf("unexpected key length priv=%d pub=%d", len(priv), len(pub))
	}
}

func TestBuildXrayVLESSConfig(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, err := json.Marshal(VLESSConfig{
		UUID:       "11111111-2222-3333-4444-555555555555",
		ServerName: "www.cloudflare.com",
		ServerPort: 443,
		PrivateKey: priv,
		PublicKey:  pub,
		ShortID:    "abcd1234",
		Flow:       "xtls-rprx-vision",
		Security:   "reality",
		Network:    "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildXrayVLESSConfig(8443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{
		`"protocol": "vless"`,
		`"port": 8443`,
		`"security": "reality"`,
		priv,
		"www.cloudflare.com",
		"11111111-2222-3333-4444-555555555555",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("config missing %q\n%s", want, s)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
}

func TestBuildXrayVLESSConfigRequiresSNI(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{UUID: "u", PrivateKey: "k"})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected error without server_name")
	}
}

func TestBuildSingBoxSSConfig(t *testing.T) {
	raw, err := json.Marshal(SSConfig{
		Method:   "2022-blake3-aes-128-gcm",
		Password: "AAAAAAAAAAAAAAAAAAAAAA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildSingBoxSSConfig(8388, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{
		`"type": "shadowsocks"`,
		`"listen_port": 8388`,
		`2022-blake3-aes-128-gcm`,
		`AAAAAAAAAAAAAAAAAAAAAA==`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("config missing %q\n%s", want, s)
		}
	}
}

func TestEnsureSecretsRealityRealKeys(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{"server_name":"a.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.PrivateKey == "" || c.PublicKey == "" || c.UUID == "" {
		t.Fatalf("missing secrets: %+v", c)
	}
	// Round-trip into xray config builder.
	if _, err := BuildXrayVLESSConfig(443, raw); err != nil {
		t.Fatal(err)
	}
}

func TestBuildXrayVLESSConfigShortIDsStrict(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd1234", Security: "reality", Network: "tcp",
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	in := m["inbounds"].([]any)[0].(map[string]any)
	ss := in["streamSettings"].(map[string]any)
	rs := ss["realitySettings"].(map[string]any)
	ids := rs["shortIds"].([]any)
	if len(ids) != 1 || ids[0] != "abcd1234" {
		t.Fatalf("strict shortIds=%v", ids)
	}
}

func TestBuildXrayVLESSConfigRejectsWSWithREALITY(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "ab", Security: "reality",
		Network: "ws", Path: "/ray", Host: "www.example.com", Flow: "xtls-rprx-vision",
	})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected error for ws+REALITY")
	} else if !strings.Contains(err.Error(), "tcp") {
		t.Fatalf("error should mention tcp: %v", err)
	}
}

func TestBuildXrayVLESSConfigXHTTP(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "ab", Security: "reality",
		Network: "xhttp", Path: "/ray", Host: "www.example.com", XHTTPMode: "auto",
		Flow: "xtls-rprx-vision",
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, `"xhttpSettings"`) || !strings.Contains(s, "/ray") {
		t.Fatalf("missing xhttp settings: %s", s)
	}
	// flow must be stripped on non-tcp
	if strings.Contains(s, "xtls-rprx-vision") {
		t.Fatal("vision should not appear on xhttp")
	}
}

func TestBuildXrayVLESSConfigInvalidPrivateKey(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: "not-a-key", ShortID: "ab", Security: "reality", Network: "tcp",
	})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected invalid private_key error")
	}
}

func TestBuildXrayVLESSConfigInvalidDecryption(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd", Security: "reality", Network: "tcp",
		Decryption: "mlkem-client-encryption-string-pasted-wrong",
	})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected invalid decryption error")
	}
}

func TestEnsureSecretsKeyPairAlwaysMatched(t *testing.T) {
	// Only private provided → both replaced with a matched pair
	raw, err := EnsureSecrets("vless", json.RawMessage(`{"server_name":"a.com","private_key":"only-priv"}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	_ = json.Unmarshal(raw, &c)
	if c.PrivateKey == "only-priv" || c.PublicKey == "" {
		t.Fatalf("expected full pair regen, got priv=%q pub=%q", c.PrivateKey, c.PublicKey)
	}
}

func TestEnsureSecretsFlowNonePreserved(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{"server_name":"a.com","flow":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	_ = json.Unmarshal(raw, &c)
	if c.Flow != "" {
		t.Fatalf("flow none should clear, got %q", c.Flow)
	}
}
