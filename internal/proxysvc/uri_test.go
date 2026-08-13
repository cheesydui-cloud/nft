package proxysvc

import (
	"encoding/json"
	"strconv"
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
	if strings.Contains(uri, "%2F") || strings.Contains(uri, "%2f") {
		t.Fatalf("SIP002 userinfo must not percent-encode base64 slashes: %s", uri)
	}
}

func TestBuildShareURISSIP002SlashInPassword(t *testing.T) {
	// SS2022 keys are std-base64 and often contain '/'.
	raw := json.RawMessage(`{"method":"2022-blake3-aes-128-gcm","password":"AA/BB+CC=="}`)
	uri, err := BuildShareURI("shadowsocks", "ss1", "1.2.3.4", 8388, raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uri, "%2F") || strings.Contains(uri, "%2f") {
		t.Fatalf("slash must stay in raw userinfo, not %%2F: %s", uri)
	}
	if !strings.Contains(uri, "ss://") || !strings.Contains(uri, "@1.2.3.4:8388") {
		t.Fatalf("bad uri: %s", uri)
	}
}

func TestBuildShareURIMieruIPv6AndDefaultTCP(t *testing.T) {
	raw := json.RawMessage(`{"username":"alice","password":"secret"}`)
	uri, err := BuildShareURI("mieru", "m1", "2001:db8::1", 8964, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "[2001:db8::1]") {
		t.Fatalf("IPv6 share host must be bracketed: %s", uri)
	}
	if strings.Contains(uri, "protocol=UDP") {
		t.Fatalf("empty transports must default TCP only: %s", uri)
	}
	if !strings.Contains(uri, "protocol=TCP") {
		t.Fatalf("missing TCP: %s", uri)
	}
}

func TestEnsureSecretsMieruDefaultsTCP(t *testing.T) {
	raw, err := EnsureSecrets("mieru", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var c MieruConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Transports) != 1 || strings.ToUpper(c.Transports[0]) != "TCP" {
		t.Fatalf("default transports = %v, want [TCP]", c.Transports)
	}
	uri, err := BuildShareURI("mieru", "m1", "9.9.9.9", DefaultMieruListenPort, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "protocol=TCP") {
		t.Fatalf("share missing TCP: %s", uri)
	}
	if strings.Contains(uri, "protocol=UDP") {
		t.Fatalf("default share must not advertise UDP: %s", uri)
	}
}

func TestEnsureSecretsAndBuildMieru(t *testing.T) {
	raw, err := EnsureSecrets("mieru", json.RawMessage(`{"transports":["TCP","UDP"]}`))
	if err != nil {
		t.Fatal(err)
	}
	uri, err := BuildShareURI("mieru", "m1", "9.9.9.9", DefaultMieruListenPort, raw)
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
	portTok := "port=" + strconv.Itoa(DefaultMieruListenPort)
	if !strings.Contains(uri, portTok) {
		t.Fatalf("missing port: %s", uri)
	}
		if !strings.Contains(uri, "protocol=TCP") {
			t.Fatalf("missing TCP: %s", uri)
		}
		if strings.Contains(uri, "protocol=UDP") {
			t.Fatalf("share must advertise TCP only even when server also binds UDP: %s", uri)
		}
	// Host must not embed :port in simple form (port is a query param).
	if strings.Contains(uri, "9.9.9.9:"+strconv.Itoa(DefaultMieruListenPort)) {
		t.Fatalf("host should not include listen port: %s", uri)
	}
		want := "profile=m1&" + portTok + "&protocol=TCP"
		if !strings.Contains(uri, want) {
			t.Fatalf("query order = %s, want substring %s", uri, want)
		}
}

func TestValidateMieruDeployRejectsPrivilegedPort(t *testing.T) {
	c := &MieruConfig{Username: "u", Password: "p", ListenPort: 443}
	if err := ValidateMieruDeploy(c, 0); err == nil {
		t.Fatal("expected error for port 443")
	}
	c.ListenPort = DefaultMieruListenPort
	if err := ValidateMieruDeploy(c, 0); err != nil {
		t.Fatal(err)
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
