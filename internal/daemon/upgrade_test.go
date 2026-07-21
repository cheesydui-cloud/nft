package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"nft/internal/wsproto"
)

func TestUpgradeBinaryFromData(t *testing.T) {
	payload := []byte("hello-binary")
	sum := sha256.Sum256(payload)
	good := wsproto.Upgrade{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(payload)), Data: payload}
	got, err := upgradeBinary(good)
	if err != nil {
		t.Fatalf("good data: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}

	bad := wsproto.Upgrade{SHA256: "deadbeef", Size: int64(len(payload)), Data: payload}
	if _, err := upgradeBinary(bad); err == nil {
		t.Fatal("sha mismatch should error")
	}
}

func TestFixUpgradeDownloadURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://panel/v1/binary", "https://panel/v1/binary"},
		{"http://1.2.3.4:7788/v1/binary", "http://1.2.3.4:7788/v1/binary"},
		{"107.174.202.136:7788/v1/binary", "http://107.174.202.136:7788/v1/binary"},
		{"  panel.example/v1/binary  ", "http://panel.example/v1/binary"},
	}
	for _, tc := range cases {
		if got := fixUpgradeDownloadURL(tc.in); got != tc.want {
			t.Errorf("fixUpgradeDownloadURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

