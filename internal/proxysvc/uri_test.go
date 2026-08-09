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
