package proxysvc

import (
	"encoding/base64"
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
		// 16 zero bytes → valid SS2022-128 key
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
			`"listen": "::"`,
			`2022-blake3-aes-128-gcm`,
			`AAAAAAAAAAAAAAAAAAAAAA==`,
			`"ntp"`,
			`time.apple.com`,
			`"sniff": true`,
			`"tag": "direct-out"`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("config missing %q\n%s", want, s)
			}
		}
	}

	func TestBuildSingBoxSSConfigMultiplexAndIPv4(t *testing.T) {
		f := false
		raw, err := json.Marshal(SSConfig{
			Method:      "2022-blake3-aes-128-gcm",
			Password:    "AAAAAAAAAAAAAAAAAAAAAA==",
			Listen:      "0.0.0.0",
			Multiplex:   true,
			TCPFastOpen: true,
			NTP:         &f,
			Sniffing:    &f,
		})
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := BuildSingBoxSSConfig(9000, raw)
		if err != nil {
			t.Fatal(err)
		}
		s := string(cfg)
		for _, want := range []string{
			`"listen": "0.0.0.0"`,
			`"tcp_fast_open": true`,
			`"multiplex"`,
			`"smux"`,
		} {
			if !strings.Contains(s, want) {
				t.Fatalf("missing %q\n%s", want, s)
			}
		}
		if strings.Contains(s, `"ntp"`) {
			t.Fatalf("ntp should be off:\n%s", s)
		}
		if strings.Contains(s, `"sniff": true`) {
			t.Fatalf("sniff should be off:\n%s", s)
		}
	}

	func TestValidateSSDeploy(t *testing.T) {
		if err := ValidateSSDeploy(&SSConfig{Method: "2022-blake3-aes-128-gcm", Password: "short"}); err == nil {
			t.Fatal("expected base64 error")
		}
		// wrong length for 128
		bad := base64.StdEncoding.EncodeToString(make([]byte, 32))
		if err := ValidateSSDeploy(&SSConfig{Method: "2022-blake3-aes-128-gcm", Password: bad}); err == nil {
			t.Fatal("expected length error")
		}
		ok := "AAAAAAAAAAAAAAAAAAAAAA=="
		if err := ValidateSSDeploy(&SSConfig{Method: "2022-blake3-aes-128-gcm", Password: ok}); err != nil {
			t.Fatal(err)
		}
		// legacy AEAD: any non-empty password
		if err := ValidateSSDeploy(&SSConfig{Method: "aes-128-gcm", Password: "hello"}); err != nil {
			t.Fatal(err)
		}
	}

	func TestEnsureSecretsSSDefaults(t *testing.T) {
		raw, err := EnsureSecrets("shadowsocks", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var c SSConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		if c.Method != "2022-blake3-aes-128-gcm" || c.Password == "" || c.Listen != "::" {
			t.Fatalf("got %+v", c)
		}
		if err := ValidateSSDeploy(&c); err != nil {
			t.Fatal(err)
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

func TestBuildXrayVLESSConfigQuotedDecryption(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	// xray / paste often wraps material in quotes — must accept after strip.
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd", Security: "reality", Network: "tcp",
		Decryption: `"mlkem768x25519plus.native.600s.4MioaxQX0R2H6A1xAukJgb"`,
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"decryption": "mlkem768x25519plus.native.600s.4MioaxQX0R2H6A1xAukJgb"`) {
		t.Fatalf("expected stripped decryption in config:\n%s", cfg)
	}
}

func TestBuildXrayVLESSConfigRejectsClientEncInDecryption(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	// Only client string in decryption (no pair to auto-swap) → reject.
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd", Security: "reality", Network: "tcp",
		Decryption: "mlkem768x25519plus.native.0rtt.ySi246clientOnly",
	})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected reject client encryption pasted as decryption")
	}
}

// Swapped encryption/decryption fields must auto-correct so server gets 600s.
func TestBuildXrayVLESSConfigSwappedPairAutoAlign(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd", Security: "reality", Network: "tcp",
		// Intentionally swapped (broken paste / old parser).
		Encryption: "mlkem768x25519plus.native.600s.ServerKeyMaterialAAAAAAA",
		Decryption: "mlkem768x25519plus.native.0rtt.ClientKeyMaterialBBBBBBB",
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"decryption": "mlkem768x25519plus.native.600s.ServerKeyMaterialAAAAAAA"`) {
		t.Fatalf("expected server 600s as decryption after align:\n%s", cfg)
	}
	if strings.Contains(string(cfg), "0rtt") {
		t.Fatalf("server config must not contain client 0rtt:\n%s", cfg)
	}
}

func TestBuildXrayVLESSConfigTLS(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "vpn.example.com",
		Security: "tls", Network: "ws", Path: "/ray", Host: "vpn.example.com",
		CertFile: "/var/lib/nft/cores/xray/instance-1.crt",
		KeyFile:  "/var/lib/nft/cores/xray/instance-1.key",
		ALPN:     "h2,http/1.1",
		Flow:     "xtls-rprx-vision", // must be stripped on ws
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{
		`"security": "tls"`,
		`"wsSettings"`,
		`/ray`,
		`certificateFile`,
		`instance-1.crt`,
		`"h2"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "xtls-rprx-vision") {
		t.Fatal("vision must not appear on ws")
	}
}

func TestBuildXrayVLESSConfigNone(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555",
		Security: "none", Network: "tcp",
	})
	cfg, err := BuildXrayVLESSConfig(8443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, `"security": "none"`) {
		t.Fatalf("expected none: %s", s)
	}
	if strings.Contains(s, "realitySettings") || strings.Contains(s, "tlsSettings") {
		t.Fatalf("none must not include tls/reality settings: %s", s)
	}
}

func TestBuildXrayVLESSConfigRealityGRPC(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd", Security: "reality",
		Network: "grpc", ServiceName: "GunService",
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if !strings.Contains(s, `"grpcSettings"`) || !strings.Contains(s, "GunService") {
		t.Fatalf("missing grpc: %s", s)
	}
	if strings.Contains(s, "xtls-rprx-vision") {
		t.Fatal("vision must not appear on grpc")
	}
}

func TestBuildXrayVLESSConfigTLSRequiresCert(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "vpn.example.com",
		Security: "tls", Network: "tcp",
	})
	if _, err := BuildXrayVLESSConfig(443, raw); err == nil {
		t.Fatal("expected error without cert")
	}
}

func TestNetworkMatrix(t *testing.T) {
	if !NetworkAllowed("reality", "grpc") {
		t.Fatal("reality should allow grpc")
	}
	if NetworkAllowed("reality", "ws") {
		t.Fatal("reality must reject ws")
	}
	if !NetworkAllowed("tls", "ws") {
		t.Fatal("tls should allow ws")
	}
	if !VisionAllowed("tls", "tcp") {
		t.Fatal("tls+tcp vision ok")
	}
	if VisionAllowed("none", "tcp") {
		t.Fatal("none+tcp vision not allowed")
	}
}

func TestEnsureSecretsSoftCorrectsIllegalNetwork(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{"security":"reality","network":"ws","server_name":"a.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Network != "tcp" {
		t.Fatalf("expected soft-correct to tcp, got %q", c.Network)
	}
}

func TestBuildShareURITLSAndNone(t *testing.T) {
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555",
		Security: "tls", Network: "ws", ServerName: "vpn.example.com",
		Path: "/v", Host: "vpn.example.com", Fingerprint: "chrome", ALPN: "h2",
	})
	uri, err := BuildShareURI("vless", "t1", "1.2.3.4", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"security=tls", "type=ws", "sni=vpn.example.com", "path=%2Fv", "alpn=h2"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("uri missing %q: %s", want, uri)
		}
	}
	raw2, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555",
		Security: "none", Network: "tcp",
	})
	uri2, err := BuildShareURI("vless", "t2", "1.2.3.4", 8443, raw2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri2, "security=none") {
		t.Fatalf("uri2=%s", uri2)
	}
}

func TestGenerateVlessEncX25519Shape(t *testing.T) {
	enc, dec := GenerateVlessEncX25519()
	if !strings.HasPrefix(enc, "mlkem768x25519plus.native.0rtt.") {
		t.Fatalf("enc=%q", enc)
	}
	if !strings.HasPrefix(dec, "mlkem768x25519plus.native.600s.") {
		t.Fatalf("dec=%q", dec)
	}
	if len(enc) > 100 || len(dec) > 100 {
		t.Fatalf("expected short X25519 keys enc=%d dec=%d", len(enc), len(dec))
	}
	// Must be a matched pair that xray accepts as server decryption.
	priv, pub := GenerateRealityKeyPair()
	raw, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.example.com",
		PrivateKey: priv, PublicKey: pub, ShortID: "abcd1234", Security: "reality", Network: "tcp",
		Encryption: enc, Decryption: dec, Flow: "xtls-rprx-vision",
	})
	cfg, err := BuildXrayVLESSConfig(443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), `"decryption": "`+dec+`"`) {
		t.Fatalf("config missing decryption:\n%s", cfg)
	}
	uri, err := BuildShareURI("vless", "t", "1.2.3.4", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "encryption="+enc) && !strings.Contains(uri, "0rtt") {
		t.Fatalf("uri missing encryption: %s", uri)
	}
}

func TestEnsureSecretsAlignsSwappedVlessEnc(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{
		"server_name":"www.example.com",
		"encryption":"mlkem768x25519plus.native.600s.ServerKeyMaterialAAAAAAA",
		"decryption":"mlkem768x25519plus.native.0rtt.ClientKeyMaterialBBBBBBB"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Encryption, "0rtt") {
		t.Fatalf("encryption should be client 0rtt, got %q", c.Encryption)
	}
	if !strings.Contains(c.Decryption, "600s") {
		t.Fatalf("decryption should be server 600s, got %q", c.Decryption)
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

func TestNeedsVLESSEnc(t *testing.T) {
	if NeedsVLESSEnc(json.RawMessage(`{"decryption":"none"}`)) {
		t.Fatal("none should be false")
	}
	if NeedsVLESSEnc(json.RawMessage(`{}`)) {
		t.Fatal("empty should be false")
	}
	enc, dec := GenerateVlessEncX25519()
	raw, _ := json.Marshal(VLESSConfig{Encryption: enc, Decryption: dec})
	if !NeedsVLESSEnc(raw) {
		t.Fatal("paired vlessenc should be true")
	}
	if !NeedsVLESSEnc(json.RawMessage(`{"encryption":"` + enc + `"}`)) {
		t.Fatal("client-only encryption should still force modern core")
	}
}

func TestBuildXrayVLESSConfigSniffingAndTFO(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	off := false
	raw, err := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.cloudflare.com",
		ServerPort: 443, PrivateKey: priv, PublicKey: pub, ShortID: "abcd1234",
		Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
		Sniffing: &off, TcpFastOpen: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildXrayVLESSConfig(8443, raw)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if strings.Contains(s, `"sniffing"`) {
		t.Fatalf("sniffing should be omitted when false:\n%s", s)
	}
	if !strings.Contains(s, `"tcpFastOpen": true`) {
		t.Fatalf("want tcpFastOpen true:\n%s", s)
	}

	// Default (nil sniffing) keeps sniffing on, no TFO.
	raw2, _ := json.Marshal(VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.cloudflare.com",
		ServerPort: 443, PrivateKey: priv, PublicKey: pub, ShortID: "abcd1234",
		Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
	})
	cfg2, err := BuildXrayVLESSConfig(8443, raw2)
	if err != nil {
		t.Fatal(err)
	}
	s2 := string(cfg2)
	if !strings.Contains(s2, `"sniffing"`) || !strings.Contains(s2, `"enabled": true`) {
		t.Fatalf("default sniffing on:\n%s", s2)
	}
	if strings.Contains(s2, `tcpFastOpen`) {
		t.Fatalf("default no TFO:\n%s", s2)
	}
}
