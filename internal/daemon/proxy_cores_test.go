package daemon

import (
	"encoding/json"
	"testing"

	"nft/internal/wsproto"
)

func TestMitaPortBindings(t *testing.T) {
	b := mitaPortBindings(443, []string{"TCP", "UDP", "tcp"})
	if len(b) != 2 {
		t.Fatalf("want 2 bindings (dedupe), got %d: %+v", len(b), b)
	}
	if b[0]["port"] != 443 || b[0]["protocol"] != "TCP" {
		t.Fatalf("first: %+v", b[0])
	}
	if b[1]["protocol"] != "UDP" {
		t.Fatalf("second: %+v", b[1])
	}
}

func TestHandleProxyServiceApplyUnknown(t *testing.T) {
	ack := handleProxyServiceApply(wsproto.ProxyServiceApply{Protocol: "wireguard"})
	if ack.OK {
		t.Fatal("expected failure for unknown protocol")
	}
}

func TestHandleProxyServiceApplyVLESSDryRun(t *testing.T) {
	ack := handleProxyServiceApply(wsproto.ProxyServiceApply{Protocol: "vless", Core: "xray"})
	if !ack.OK || !ack.DryRun {
		t.Fatalf("vless should dry-run ok: %+v", ack)
	}
	if ack.Error == "" {
		t.Fatal("expected dry-run explanation")
	}
}

func TestDeployMieruMissingMita(t *testing.T) {
	// When mita is not installed, deploy must fail (not silent dry-run ready).
	raw, _ := json.Marshal(map[string]any{
		"username": "u1", "password": "p1", "listen_port": 443,
		"transports": []string{"TCP"},
	})
	ack := deployMieru(wsproto.ProxyServiceApply{
		InstanceID: 1,
		Protocol:   "mieru",
		Core:       "mieru",
		ListenPort: 443,
		Config:     raw,
	})
	// On CI/dev hosts without mita: not OK, dry-run true, error mentions mita.
	if findMitaBinary() == "" {
		if ack.OK {
			t.Fatalf("expected failure without mita: %+v", ack)
		}
		if ack.Error == "" {
			t.Fatal("expected install error")
		}
	}
}
