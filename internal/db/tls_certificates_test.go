package db

import (
	"encoding/json"
	"testing"
)

func TestTLSCertificateCRUDAndCertID(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	row, err := CreateTLSCertificate(d, &TLSCertificate{
		Name:        "test",
		Domain:      "vpn.example.com",
		CertPEM:     "CERT",
		KeyPEM:      "KEY",
		Source:      TLSCertSourceUpload,
		NotAfter:    "2030-01-01T00:00:00Z",
		Fingerprint: "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ID <= 0 || row.Domain != "vpn.example.com" {
		t.Fatalf("bad row: %+v", row)
	}

	list, err := ListTLSCertificates(d)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v n=%d", err, len(list))
	}

	cfg, _ := json.Marshal(map[string]any{
		"server_name": "vpn.example.com",
		"cert_id":     row.ID,
	})
	svc, err := CreateProxyService(d, "svc1", ProxyProtoAnyTLS, ProxyCoreSingBox, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	n, err := CountProxyServicesByCertID(d, row.ID)
	if err != nil || n != 1 {
		t.Fatalf("ref count=%d err=%v", n, err)
	}
	ids, err := ListProxyServiceIDsByCertID(d, row.ID)
	if err != nil || len(ids) != 1 || ids[0] != svc.ID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}

	if CertIDFromConfigJSON(cfg) != row.ID {
		t.Fatalf("CertIDFromConfigJSON got %d", CertIDFromConfigJSON(cfg))
	}
	if CertIDFromConfigJSON([]byte(`{"cert_id":"12"}`)) != 12 {
		t.Fatal("string cert_id")
	}
	if CertIDFromConfigJSON([]byte(`{}`)) != 0 {
		t.Fatal("empty")
	}

	if err := DeleteTLSCertificate(d, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetTLSCertificate(d, row.ID); err == nil {
		t.Fatal("expected not found")
	}
}

func TestListTLSCertificatesACMEEnabled(t *testing.T) {
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, _ = CreateTLSCertificate(d, &TLSCertificate{
		Domain: "a.com", CertPEM: "c", KeyPEM: "k", Source: TLSCertSourceUpload,
	})
	_, _ = CreateTLSCertificate(d, &TLSCertificate{
		Domain: "b.com", CertPEM: "c", KeyPEM: "k", Source: TLSCertSourceACME, ACMEEnabled: true,
	})
	list, err := ListTLSCertificatesACMEEnabled(d)
	if err != nil || len(list) != 1 || list[0].Domain != "b.com" {
		t.Fatalf("acme list=%+v err=%v", list, err)
	}
}
