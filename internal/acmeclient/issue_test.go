package acmeclient

import (
	"strings"
	"testing"
)

func TestEnsureAccountKeyRoundTrip(t *testing.T) {
	k1, pem1, err := EnsureAccountKey("")
	if err != nil || k1 == nil || !strings.Contains(pem1, "EC PRIVATE KEY") {
		t.Fatalf("gen: k=%v pem=%q err=%v", k1 != nil, pem1[:min(40, len(pem1))], err)
	}
	k2, pem2, err := EnsureAccountKey(pem1)
	if err != nil || k2 == nil {
		t.Fatal(err)
	}
	if k1.D.Cmp(k2.D) != 0 {
		t.Fatal("account key not stable across EnsureAccountKey")
	}
	// Re-encode may differ only in formatting; parse both.
	p1, err := ParseAccountKeyPEM(pem1)
	if err != nil || p1 == nil {
		t.Fatal(err)
	}
	p2, err := ParseAccountKeyPEM(pem2)
	if err != nil || p2 == nil {
		t.Fatal(err)
	}
	if p1.D.Cmp(p2.D) != 0 {
		t.Fatal("parsed keys differ")
	}
}

func TestParseAccountKeyPEMEmpty(t *testing.T) {
	k, err := ParseAccountKeyPEM("")
	if err != nil || k != nil {
		t.Fatalf("want nil,nil got %v %v", k, err)
	}
	_, err = ParseAccountKeyPEM("not-pem")
	if err == nil {
		t.Fatal("expected error for garbage")
	}
}

func TestIsAccountExists(t *testing.T) {
	if !isAccountExists(errString("account already exists")) {
		t.Fatal("expected true")
	}
	if isAccountExists(nil) || isAccountExists(errString("rate limited")) {
		t.Fatal("expected false")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
