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
