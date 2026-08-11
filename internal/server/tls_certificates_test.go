package server

import (
	"encoding/json"
	"testing"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

func TestResolveProxyConfigCertID(t *testing.T) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := &Server{DB: d}

	cert, key, _, err := proxysvc.GenerateSelfSignedTLS("vpn.example.com", 7)
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.CreateTLSCertificate(d, &db.TLSCertificate{
		Name: "t", Domain: "vpn.example.com",
		CertPEM: cert, KeyPEM: key, Source: db.TLSCertSourceSelfSigned,
	})
	if err != nil {
		t.Fatal(err)
	}

	in, _ := json.Marshal(map[string]any{
		"server_name": "",
		"cert_id":     row.ID,
	})
	out, err := s.resolveProxyConfigCertID(in)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["cert_pem"] != cert {
		t.Fatalf("cert_pem not injected")
	}
	if m["key_pem"] != key {
		t.Fatalf("key_pem not injected")
	}
	if m["server_name"] != "vpn.example.com" {
		t.Fatalf("server_name fill: %v", m["server_name"])
	}

	// No cert_id → passthrough
	plain, _ := json.Marshal(map[string]any{"cert_pem": "X", "key_pem": "Y"})
	out2, err := s.resolveProxyConfigCertID(plain)
	if err != nil {
		t.Fatal(err)
	}
	var m2 map[string]any
	_ = json.Unmarshal(out2, &m2)
	if m2["cert_pem"] != "X" {
		t.Fatalf("passthrough failed: %v", m2)
	}

	// Missing vault id
	bad, _ := json.Marshal(map[string]any{"cert_id": 99999})
	if _, err := s.resolveProxyConfigCertID(bad); err == nil {
		t.Fatal("expected error for missing cert")
	}
}
