package server

import "testing"

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
