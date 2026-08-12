package server

import (
	"testing"

	"nft/internal/db"
	"nft/internal/landing"
)

func TestClassifyExit(t *testing.T) {
	idx := map[string]landing.Node{
		"1.2.3.4:443": {Name: "HK-01", Protocol: "vless", Host: "1.2.3.4", Port: 443,
			URI: "vless://uuid@1.2.3.4:443?security=reality&sni=a.com#HK-01"},
	}

	t.Run("landing match yields relay uri with entry endpoint", func(t *testing.T) {
		idxWithExp := map[string]landing.Node{
			"1.2.3.4:443": {Name: "HK-01", Protocol: "vless", Host: "1.2.3.4", Port: 443,
				URI: "vless://uuid@1.2.3.4:443?security=reality&sni=a.com#HK-01", ExpiresAt: 1_787_000_000},
		}
		it := ruleListItem{Rule: &db.Rule{ExitHost: "1.2.3.4", ExitPort: 443},
			Entry: "relay.example:10001", Exit: "1.2.3.4:443"}
		it.classifyExit(idxWithExp, true)
		if it.ExitKind != "landing" {
			t.Fatalf("kind = %q, want landing", it.ExitKind)
		}
		if it.LandingName != "HK-01" {
			t.Errorf("landing_name = %q", it.LandingName)
		}
		if it.LandingProtocol != "vless" {
			t.Errorf("landing_protocol = %q, want vless", it.LandingProtocol)
		}
		if it.LandingExpiresAt != 1_787_000_000 {
			t.Errorf("landing_expires_at = %d, want user-exit expiry", it.LandingExpiresAt)
		}
		want := "vless://uuid@relay.example:10001?security=reality&sni=a.com#HK-01"
		if it.RelayURI != want {
			t.Errorf("relay_uri = %q, want %q", it.RelayURI, want)
		}
	})

	t.Run("admin list (withURI=false) marks kind but omits relay uri", func(t *testing.T) {
		it := ruleListItem{Rule: &db.Rule{ExitHost: "1.2.3.4", ExitPort: 443},
			Entry: "relay.example:10001", Exit: "1.2.3.4:443"}
		it.classifyExit(idx, false)
		if it.ExitKind != "landing" || it.RelayURI != "" {
			t.Fatalf("kind=%q relay=%q, want landing with empty relay", it.ExitKind, it.RelayURI)
		}
	})

	t.Run("custom exit has no relay uri (user URIs are client-side)", func(t *testing.T) {
		it := ruleListItem{Rule: &db.Rule{ExitHost: "9.9.9.9", ExitPort: 8443},
			Entry: "relay.example:20000", Exit: "9.9.9.9:8443"}
		it.classifyExit(idx, true)
		if it.ExitKind != "custom" || it.RelayURI != "" || it.LandingURI != "" {
			t.Fatalf("got kind=%q relay=%q landing=%q", it.ExitKind, it.RelayURI, it.LandingURI)
		}
	})

	t.Run("no entry yet skips relay uri", func(t *testing.T) {
		it := ruleListItem{Rule: &db.Rule{ExitHost: "1.2.3.4", ExitPort: 443},
			Entry: "—", Exit: "1.2.3.4:443"}
		it.classifyExit(idx, true)
		if it.ExitKind != "landing" || it.RelayURI != "" {
			t.Fatalf("kind=%q relay=%q, want landing with empty relay", it.ExitKind, it.RelayURI)
		}
	})

	t.Run("socks5 exit rewrites exit_uri to entry for copy/QR", func(t *testing.T) {
		it := ruleListItem{
			Rule: &db.Rule{
				ExitHost: "10.0.0.1", ExitPort: 443,
				ExitType: "socks5",
				ExitURI:  "socks5://alice:s3cret@proxy.example:1080",
			},
			Entry: "relay.example:10001",
			Exit:  "10.0.0.1:443",
		}
		it.classifyExit(idx, true)
		if it.ExitKind != "custom" {
			t.Fatalf("kind = %q, want custom", it.ExitKind)
		}
		if it.LandingProtocol != "socks5" {
			t.Errorf("landing_protocol = %q, want socks5", it.LandingProtocol)
		}
		want := "socks5://alice:s3cret@relay.example:10001"
		if it.RelayURI != want {
			t.Errorf("relay_uri = %q, want %q", it.RelayURI, want)
		}
		// ExitURI must be redacted in the embedded rule after classify.
		if it.ExitURI != "socks5://alice:***@proxy.example:1080" {
			t.Errorf("exit_uri after classify = %q, want redacted", it.ExitURI)
		}
	})

	t.Run("socks5 without entry omits relay uri but still redacts", func(t *testing.T) {
		it := ruleListItem{
			Rule: &db.Rule{
				ExitHost: "10.0.0.1", ExitPort: 443,
				ExitType: "socks5",
				ExitURI:  "socks5://alice:s3cret@proxy.example:1080",
			},
			Entry: "—",
			Exit:  "10.0.0.1:443",
		}
		it.classifyExit(idx, true)
		if it.RelayURI != "" {
			t.Fatalf("relay_uri = %q, want empty before entry ready", it.RelayURI)
		}
		if it.ExitURI != "socks5://alice:***@proxy.example:1080" {
			t.Errorf("exit_uri = %q, want redacted", it.ExitURI)
		}
	})

	t.Run("socks5 withURI=false omits relay but redacts exit_uri", func(t *testing.T) {
		it := ruleListItem{
			Rule: &db.Rule{
				ExitHost: "10.0.0.1", ExitPort: 443,
				ExitType: "socks5",
				ExitURI:  "socks5://alice:s3cret@proxy.example:1080",
			},
			Entry: "relay.example:10001",
			Exit:  "10.0.0.1:443",
		}
		it.classifyExit(idx, false)
		if it.RelayURI != "" {
			t.Fatalf("relay_uri = %q, want empty when withURI=false", it.RelayURI)
		}
		if it.ExitURI != "socks5://alice:***@proxy.example:1080" {
			t.Errorf("exit_uri = %q, want redacted", it.ExitURI)
		}
	})

	t.Run("SK5 exit keeps socks5 relay even when entry was a VLESS proxy service", func(t *testing.T) {
		// Data plane: L4 entry + last-hop ExitProxy CONNECT. Client link must be
		// socks5 rewritten to entry — never the VLESS share on the L4 port.
		it := ruleListItem{
			Rule: &db.Rule{
				NodeID: 7, ProxyServiceID: 3,
				ExitHost: "10.0.0.1", ExitPort: 443,
				ExitType: "socks5",
				ExitURI:  "socks5://alice:s3cret@proxy.example:1080",
			},
			Entry: "relay.example:10001",
			Exit:  "10.0.0.1:443",
		}
		share := "vless://uuid@1.2.3.4:443?security=reality&sni=a.com#测试1"
		it.classifyExitWithShare(idx, true, "vless", share, "测试1")
		want := "socks5://alice:s3cret@relay.example:10001"
		if it.RelayURI != want {
			t.Errorf("relay_uri = %q, want %q", it.RelayURI, want)
		}
		if it.LandingProtocol != "socks5" {
			t.Errorf("landing_protocol = %q, want socks5 (not entry proxy protocol)", it.LandingProtocol)
		}
		if it.LandingName != "测试1" {
			t.Errorf("landing_name = %q, want service display name", it.LandingName)
		}
		if it.ExitURI != "socks5://alice:***@proxy.example:1080" {
			t.Errorf("exit_uri = %q, want redacted", it.ExitURI)
		}
	})
}
