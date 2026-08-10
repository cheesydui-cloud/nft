package server

import (
	"strings"
	"testing"
)

func TestParseVlessEncOutputEnglish(t *testing.T) {
	out := `Authentication: ML-KEM-768, X25519
Encryption (client): mlkem768x25519plus.native.0rtt.AAAA.clientmaterial
Decryption (server): mlkem768x25519plus.native.600s.BBBB.servermaterial
`
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.Contains(enc, "0rtt") {
		t.Fatalf("enc should be client 0rtt, got %q", enc)
	}
	if !strings.Contains(dec, "600s") {
		t.Fatalf("dec should be server 600s, got %q", dec)
	}
}

// Real xray vlessenc stdout: decryption first, encryption second, two auth blocks.
// Default must pick X25519 short pair (Weir-compatible), not multi-KB PQ.
func TestParseVlessEncOutputRealXrayFormat(t *testing.T) {
	// PQ client key is intentionally much longer (simulates 1184-byte pubkey).
	pqEnc := "mlkem768x25519plus.native.0rtt." + strings.Repeat("PQclientKeyMaterialXXXX", 40)
	out := `Choose one Authentication to use, do not mix them. Ephemeral key exchange is Post-Quantum safe anyway.

Authentication: X25519, not Post-Quantum
"decryption": "mlkem768x25519plus.native.600s.ServerKeyX25519aaaaaaaaaaaa"
"encryption": "mlkem768x25519plus.native.0rtt.ClientKeyX25519aaaaaaaaaaaa"

Authentication: ML-KEM-768, Post-Quantum
"decryption": "mlkem768x25519plus.native.600s.8ZxOJD32Q0kRda9m00A8snbjArPjF4WUeqV5qIJlEj0"
"encryption": "` + pqEnc + `"
`
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.HasPrefix(enc, "mlkem768x25519plus.native.0rtt.") {
		t.Fatalf("enc=%q", enc)
	}
	if !strings.HasPrefix(dec, "mlkem768x25519plus.native.600s.") {
		t.Fatalf("dec=%q", dec)
	}
	// Default: X25519 short pair (first block), not PQ.
	if !strings.Contains(enc, "ClientKeyX25519aaaaaaaaaaaa") {
		t.Fatalf("expected X25519 client key in enc, got %q (len=%d)", enc, len(enc))
	}
	if !strings.Contains(dec, "ServerKeyX25519aaaaaaaaaaaa") {
		t.Fatalf("expected X25519 server key in dec, got %q", dec)
	}
	if len(enc) > 120 {
		t.Fatalf("default encryption too long (picked PQ?): len=%d", len(enc))
	}
	// Critical: never put 0rtt into decryption.
	if strings.Contains(dec, "0rtt") {
		t.Fatalf("decryption still has 0rtt (swapped): %q", dec)
	}
	if strings.Contains(enc, "600s") {
		t.Fatalf("encryption still has 600s (swapped): %q", enc)
	}

	// Explicit PQ request still works.
	encPQ, decPQ, ok := parseVlessEncOutputPrefer(out, true)
	if !ok {
		t.Fatal("pq expected ok")
	}
	if !strings.Contains(encPQ, "PQclientKeyMaterial") {
		t.Fatalf("preferPQ should pick long PQ enc, got len=%d", len(encPQ))
	}
	if !strings.Contains(decPQ, "8ZxOJD32Q0kRda9m00A8snbjArPjF4WUeqV5qIJlEj0") {
		t.Fatalf("preferPQ dec=%q", decPQ)
	}
}

func TestParseVlessEncOutputTokenFallback(t *testing.T) {
	// Order as printed by xray: decryption (600s) first, encryption (0rtt) second.
	out := "foo mlkem768x25519plus.native.600s.ServerKeyMaterialAAAAAAA bar\nmlkem768x25519plus.native.0rtt.ClientKeyMaterialBBBBBBBB"
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.Contains(enc, "0rtt") {
		t.Fatalf("enc=%q want client 0rtt", enc)
	}
	if !strings.Contains(dec, "600s") {
		t.Fatalf("dec=%q want server 600s", dec)
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

func TestAlignVlessEncRolesSwap(t *testing.T) {
	// Given swapped order, still return client, server.
	enc, dec := alignVlessEncRoles(
		"mlkem768x25519plus.native.600s.ServerAAAA",
		"mlkem768x25519plus.native.0rtt.ClientBBBB",
	)
	if !strings.Contains(enc, "0rtt") || !strings.Contains(dec, "600s") {
		t.Fatalf("enc=%q dec=%q", enc, dec)
	}
}
