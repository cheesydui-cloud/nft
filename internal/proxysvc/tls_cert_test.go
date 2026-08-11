package proxysvc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateSelfSignedAndValidate(t *testing.T) {
	cert, key, info, err := GenerateSelfSignedTLS("vpn.example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cert, "BEGIN CERTIFICATE") || !strings.Contains(key, "BEGIN") {
		t.Fatalf("bad pem cert=%q key=%q", cert[:40], key[:40])
	}
	if !info.Configured || info.CN != "vpn.example.com" {
		t.Fatalf("info=%+v", info)
	}
	if info.KeyMatch == nil || !*info.KeyMatch {
		t.Fatal("key should match")
	}
	if info.SANMatch == nil || !*info.SANMatch {
		t.Fatal("SAN should match server name")
	}
	if err := ValidateTLSCertPair(cert, key, "vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTLSCertPair(cert, key, "other.example.com"); err == nil {
		t.Fatal("expected SAN mismatch error")
	}
}

func TestValidateTLSCertPairMismatchKey(t *testing.T) {
	cert, _, _, err := GenerateSelfSignedTLS("a.example.com", 7)
	if err != nil {
		t.Fatal(err)
	}
	_, key2, _, err := GenerateSelfSignedTLS("a.example.com", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTLSCertPair(cert, key2, "a.example.com"); err == nil {
		t.Fatal("expected key mismatch")
	}
}

func TestRedactVLESSConfigJSON(t *testing.T) {
	cert, key, _, err := GenerateSelfSignedTLS("vpn.example.com", 10)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"security":    "tls",
		"server_name": "vpn.example.com",
		"cert_pem":    cert,
		"key_pem":     key,
		"private_key": "secret-reality-key",
		"decryption":  "mlkem768x25519plus.native.600s.xxx",
	})
	out := RedactVLESSConfigJSON(raw)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["cert_pem"] != "" {
		t.Fatalf("cert should be redacted: %v", m["cert_pem"])
	}
	if m["key_pem"] != "" {
		t.Fatalf("key should be redacted")
	}
	if m["cert_configured"] != true || m["key_configured"] != true {
		t.Fatalf("flags: %+v", m)
	}
	info, ok := m["cert_info"].(map[string]any)
	if !ok || info["not_after"] == nil {
		t.Fatalf("cert_info missing: %+v", m["cert_info"])
	}
	if m["private_key"] != "" {
		t.Fatal("private_key should be cleared")
	}
	if m["decryption"] != "" {
		t.Fatal("decryption should be cleared")
	}
}

func TestInspectTLSCertEmpty(t *testing.T) {
	info := InspectTLSCert("", "", "")
	if info.Configured {
		t.Fatal("empty should not be configured")
	}
}
