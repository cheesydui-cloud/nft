package proxysvc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnsureSecretsAndBuildVLESS(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{"server_name":"cdn.example.com","listen_port":443}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.UUID == "" || c.PublicKey == "" || c.ShortID == "" {
		t.Fatalf("expected auto secrets, got %+v", c)
	}
	uri, err := BuildShareURI("vless", "gen2", "1.2.3.4", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "vless://") || !strings.Contains(uri, "security=reality") {
		t.Fatalf("bad uri: %s", uri)
	}
	if !strings.Contains(uri, "1.2.3.4:443") {
		t.Fatalf("missing host: %s", uri)
	}
}

func TestEnsureSecretsAndBuildSS(t *testing.T) {
	raw, err := EnsureSecrets("shadowsocks", json.RawMessage(`{"method":"2022-blake3-aes-128-gcm"}`))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := BuildShareURI("shadowsocks", "ss1", "node.example", 8388, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "ss://") {
		t.Fatalf("bad uri: %s", uri)
	}
}

func TestEnsureSecretsAndBuildMieru(t *testing.T) {
	raw, err := EnsureSecrets("mieru", json.RawMessage(`{"transports":["TCP","UDP"]}`))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := BuildShareURI("mieru", "m1", "9.9.9.9", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Official simple share link for `mieru import config`.
	if !strings.HasPrefix(uri, "mierus://") {
		t.Fatalf("bad uri scheme: %s", uri)
	}
	if !strings.Contains(uri, "profile=m1") {
		t.Fatalf("missing profile: %s", uri)
	}
	if !strings.Contains(uri, "port=443") {
		t.Fatalf("missing port: %s", uri)
	}
	if !strings.Contains(uri, "protocol=TCP") || !strings.Contains(uri, "protocol=UDP") {
		t.Fatalf("missing protocols: %s", uri)
	}
	// Host must not embed :port in simple form (port is a query param).
	if strings.Contains(uri, "9.9.9.9:443") {
		t.Fatalf("host should not include listen port: %s", uri)
	}
}

func TestEnsureSecretsStripsVlessEncQuotes(t *testing.T) {
	raw, err := EnsureSecrets("vless", json.RawMessage(`{
		"server_name":"www.example.com",
		"encryption":"\"mlkem768x25519plus.native.0rtt.abc\"",
		"decryption":"\"mlkem768x25519plus.native.600s.xyz\""
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var c VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Encryption, `"`) || strings.Contains(c.Decryption, `"`) {
		t.Fatalf("quotes remain enc=%q dec=%q", c.Encryption, c.Decryption)
	}
	uri, err := BuildShareURI("vless", "t", "1.2.3.4", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uri, "%22") || strings.Contains(uri, `"`) {
		// encryption value must not be quote-encoded garbage
		if strings.Contains(uri, "encryption=%22") {
			t.Fatalf("uri still has quoted encryption: %s", uri)
		}
	}
	if !strings.Contains(uri, "encryption=mlkem768x25519plus") {
		t.Fatalf("want clean encryption in uri: %s", uri)
	}
}

func TestEnsureSecretsAndBuildSocks5(t *testing.T) {
	raw, err := EnsureSecrets("socks5", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var c Socks5Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.ListenPort != 1080 || c.Username == "" || c.Password == "" {
		t.Fatalf("unexpected %+v", c)
	}
	uri, err := BuildShareURI("socks5", "sk5", "1.2.3.4", 1080, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "socks5://") || !strings.Contains(uri, "1.2.3.4:1080") {
		t.Fatalf("bad uri: %s", uri)
	}
	// no-auth
	none, err := EnsureSecrets("socks5", json.RawMessage(`{"auth_mode":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	uri2, err := BuildShareURI("socks5", "open", "9.9.9.9", 1080, none)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uri2, "@") {
		t.Fatalf("no-auth should not have userinfo: %s", uri2)
	}
}

func TestEnsureSecretsAndBuildAnyTLS(t *testing.T) {
	raw, err := EnsureSecrets("anytls", json.RawMessage(`{"server_name":"vpn.example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	var c AnyTLSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Password == "" || c.Fingerprint != "chrome" {
		t.Fatalf("unexpected %+v", c)
	}
	uri, err := BuildShareURI("anytls", "a1", "vpn.example.com", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "anytls://") || !strings.Contains(uri, "sni=vpn.example.com") {
		t.Fatalf("bad uri: %s", uri)
	}
}

func TestEnsureSecretsAndBuildNaive(t *testing.T) {
	raw, err := EnsureSecrets("naive", json.RawMessage(`{"server_name":"n.example.com","network":"udp"}`))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := BuildShareURI("naive", "n1", "n.example.com", 443, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "naive+quic://") {
		t.Fatalf("want quic scheme: %s", uri)
	}
	raw2, err := EnsureSecrets("naive", json.RawMessage(`{"server_name":"n.example.com","network":"tcp"}`))
	if err != nil {
		t.Fatal(err)
	}
	uri2, err := BuildShareURI("naive", "n2", "n.example.com", 443, raw2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri2, "naive+https://") {
		t.Fatalf("want https scheme: %s", uri2)
	}
}
