package server

import (
	"strings"
	"testing"
)

func TestParseVlessEncOutputEnglish(t *testing.T) {
	out := `Authentication: ML-KEM-768, X25519
Encryption (client): mlkem768x25519plus.AAAA.clientmaterial
Decryption (server): mlkem768x25519plus.BBBB.servermaterial
`
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if enc != "mlkem768x25519plus.AAAA.clientmaterial" {
		t.Fatalf("enc=%q", enc)
	}
	if dec != "mlkem768x25519plus.BBBB.servermaterial" {
		t.Fatalf("dec=%q", dec)
	}
}

func TestParseVlessEncOutputTokenFallback(t *testing.T) {
	out := "foo mlkem768x25519plusAAAAAAAAAAAAAAAAAAAA bar\nmlkem768x25519plusBBBBBBBBBBBBBBBBBBBB"
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if enc == "" || dec == "" || enc == dec {
		t.Fatalf("enc=%q dec=%q", enc, dec)
	}
}

func TestParseVlessEncOutputQuoted(t *testing.T) {
	out := `Encryption (client): "mlkem768x25519plus.native.0rtt.ySi246client"
Decryption (server): "mlkem768x25519plus.native.600s.4Mioaxserver"
`
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.Contains(enc, `"`) || strings.Contains(dec, `"`) {
		t.Fatalf("quotes not stripped enc=%q dec=%q", enc, dec)
	}
	if !strings.HasPrefix(enc, "mlkem768x25519plus.native.0rtt.") {
		t.Fatalf("enc=%q", enc)
	}
	if !strings.HasPrefix(dec, "mlkem768x25519plus.native.600s.") {
		t.Fatalf("dec=%q", dec)
	}
}
