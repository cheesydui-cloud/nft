package server

import (
	"testing"

	"nft/internal/db"
)

func TestDefaultProxyShareHostPrefersPublicIP(t *testing.T) {
	n := &db.Node{
		Address:     "203.0.113.9",
		BackendIP:   "198.51.100.7",
		RelayHost:   "cf-landing.example",
		RelayHostV6: "2001:db8::1",
	}
	if got := defaultProxyShareHost(n); got != "203.0.113.9" {
		t.Fatalf("defaultProxyShareHost = %q, want public Address", got)
	}
}

func TestDefaultProxyShareHostSkipsURLAddress(t *testing.T) {
	n := &db.Node{
		Address:   "https://panel.example",
		BackendIP: "203.0.113.10",
		RelayHost: "cf.example",
	}
	if got := defaultProxyShareHost(n); got != "203.0.113.10" {
		t.Fatalf("defaultProxyShareHost = %q, want BackendIP", got)
	}
}

func TestLiveProxyShareHostUsesExplicitOverride(t *testing.T) {
	n := &db.Node{Address: "203.0.113.9", RelayHost: "cf.example"}
	cfg := []byte(`{"share_host":"ddns.example"}`)
	if got := liveProxyShareHost(cfg, "old.example", n); got != "ddns.example" {
		t.Fatalf("live = %q, want explicit share_host", got)
	}
}

func TestUsableProxyShareHostRejectsURL(t *testing.T) {
	if got := usableProxyShareHost("https://x"); got != "" {
		t.Fatalf("url should be rejected, got %q", got)
	}
	if got := usableProxyShareHost("1.2.3.4:443"); got != "1.2.3.4" {
		t.Fatalf("got %q", got)
	}
}

func TestLiveProxyShareHostPrefersIPOverCFRelay(t *testing.T) {
	n := &db.Node{
		Address:   "https://panel.example",
		RelayHost: "cf-landing.example",
	}
	if got := liveProxyShareHost(nil, "203.0.113.9", n); got != "203.0.113.9" {
		t.Fatalf("live = %q, want stored public IP over CF RelayHost", got)
	}
}
