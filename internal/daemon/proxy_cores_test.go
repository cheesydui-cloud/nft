package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nft/internal/proxysvc"
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

func TestHandleProxyServiceApplyMissingCores(t *testing.T) {
	// Without xray/sing-box/mita on PATH, deploys must fail (not silent dry-run ready-without-error).
	ack := handleProxyServiceApply(wsproto.ProxyServiceApply{
		Protocol: "vless", Core: "xray",
		Config: mustJSON(map[string]any{
			"uuid": "11111111-2222-3333-4444-555555555555",
			"server_name": "www.example.com",
			"private_key": "dGVzdA", // short — may fail at config stage if xray present
		}),
		ListenPort: 18443,
		InstanceID: 99,
	})
	if findCoreBinary([]string{"xray"}, []string{"/usr/local/bin/xray", "/usr/bin/xray"}) == "" {
		if ack.OK {
			t.Fatalf("expected fail without xray: %+v", ack)
		}
	}

	ack2 := handleProxyServiceApply(wsproto.ProxyServiceApply{
		Protocol: "shadowsocks", Core: "sing-box",
		Config: mustJSON(map[string]any{
			"method": "2022-blake3-aes-128-gcm", "password": "AAAAAAAAAAAAAAAAAAAAAA==",
		}),
		ListenPort: 18388,
		InstanceID: 98,
	})
	if findCoreBinary([]string{"sing-box", "singbox"}, []string{"/usr/local/bin/sing-box"}) == "" {
		if ack2.OK {
			t.Fatalf("expected fail without sing-box: %+v", ack2)
		}
	}
}

	func TestDeployMieruMissingMita(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"username": "u1", "password": "p1", "listen_port": proxysvc.DefaultMieruListenPort,
			"transports": []string{"TCP"},
		})
		ack := deployMieru(wsproto.ProxyServiceApply{
			InstanceID: 1, Protocol: "mieru", Core: "mieru", ListenPort: proxysvc.DefaultMieruListenPort, Config: raw,
		})
		if findMitaBinary() == "" {
			if ack.OK {
				t.Fatalf("expected failure without mita: %+v", ack)
			}
			if ack.Error == "" {
				t.Fatal("expected install error")
			}
		}
	}

	func TestDeployMieruRejectsPrivilegedPort(t *testing.T) {
		raw, _ := json.Marshal(map[string]any{
			"username": "u1", "password": "p1", "listen_port": 443,
			"transports": []string{"TCP"},
		})
		ack := deployMieru(wsproto.ProxyServiceApply{
			InstanceID: 1, Protocol: "mieru", Core: "mieru", ListenPort: 443, Config: raw,
		})
		if ack.OK {
			t.Fatalf("port 443 must fail: %+v", ack)
		}
		if !strings.Contains(ack.Error, "1025") {
			t.Fatalf("error should mention 1025–65535, got %q", ack.Error)
		}
	}

func TestBuildConfigsForDeploy(t *testing.T) {
	// Config builders used by deploy must produce valid JSON files on disk.
	priv, pub := proxysvc.GenerateRealityKeyPair()
	vlessRaw, err := json.Marshal(proxysvc.VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.cloudflare.com",
		ServerPort: 443, PrivateKey: priv, PublicKey: pub, ShortID: "aabbccdd",
		Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	xcfg, err := proxysvc.BuildXrayVLESSConfig(28443, vlessRaw)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	xp := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(xp, xcfg, 0o600); err != nil {
		t.Fatal(err)
	}

	ssRaw, err := json.Marshal(proxysvc.SSConfig{
		Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	scfg, err := proxysvc.BuildSingBoxSSConfig(28388, ssRaw)
	if err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(dir, "sb.json")
	if err := os.WriteFile(sp, scfg, 0o600); err != nil {
		t.Fatal(err)
	}
	// Sanity: files non-empty
	for _, p := range []string{xp, sp} {
		st, err := os.Stat(p)
		if err != nil || st.Size() < 50 {
			t.Fatalf("bad file %s: %v", p, err)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestFindCoreBinaryPrefersEarlierPath(t *testing.T) {
	dir := t.TempDir()
	panel := filepath.Join(dir, "panel-xray")
	sys := filepath.Join(dir, "sys-xray")
	if err := os.WriteFile(panel, []byte("panel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sys, []byte("sys"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := findCoreBinary([]string{"xray-not-on-path-xyz"}, []string{panel, sys})
	if got != panel {
		t.Fatalf("want panel-managed first, got %q", got)
	}
}

func TestXraySupportsVlessEnc(t *testing.T) {
	// Fake binary that only knows "version".
	dir := t.TempDir()
	old := filepath.Join(dir, "old-xray")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'Xray 1.8.0'; exit 0; fi\necho unknown; exit 1\n"
	if err := os.WriteFile(old, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if xraySupportsVlessEnc(old) {
		t.Fatal("old stub must not report vlessenc support")
	}
	// Real xray if present.
	if real, err := exec.LookPath("xray"); err == nil {
		if !xraySupportsVlessEnc(real) && !xraySupportsVlessEnc("/tmp/xray") {
			// Accept either; some CI hosts have ancient PATH xray.
			t.Logf("PATH xray %s does not support vlessenc (expected on old packages)", real)
		}
	}
	if st, err := os.Stat("/tmp/xray"); err == nil && !st.IsDir() {
		if !xraySupportsVlessEnc("/tmp/xray") {
			t.Fatal("/tmp/xray should support vlessenc")
		}
	}
}


func TestDeployXrayAndSingBoxIfPresent(t *testing.T) {
	xray := findCoreBinary([]string{"xray"}, []string{"/tmp/nft-core-verify/xray", "/usr/local/bin/xray"})
	sb := findCoreBinary([]string{"sing-box", "singbox"}, []string{"/tmp/nft-core-verify/sing-box", "/usr/local/bin/sing-box"})
	if xray == "" && sb == "" {
		t.Skip("no xray/sing-box for live deploy test")
	}
	state := t.TempDir()
	t.Setenv("NFT_CORE_STATE_DIR", state)
	if xray != "" {
		t.Setenv("PATH", filepath.Dir(xray)+string(os.PathListSeparator)+os.Getenv("PATH"))
		priv, pub := proxysvc.GenerateRealityKeyPair()
		raw, _ := json.Marshal(proxysvc.VLESSConfig{
			UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.cloudflare.com",
			ServerPort: 443, PrivateKey: priv, PublicKey: pub, ShortID: "aabbccdd",
			Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
		})
		ack := deployXrayVLESS(wsproto.ProxyServiceApply{
			InstanceID: 900001, Protocol: "vless", Core: "xray", ListenPort: 38443, Config: raw,
		})
		if !ack.OK {
			t.Fatalf("xray deploy: %+v", ack)
		}
		if ack.DryRun {
			t.Fatal("expected live deploy")
		}
		stopPIDFile(filepath.Join(state, "xray", "instance-900001.pid"))
		t.Log("xray deploy ok")
	}
	if sb != "" {
		t.Setenv("PATH", filepath.Dir(sb)+string(os.PathListSeparator)+os.Getenv("PATH"))
		raw, _ := json.Marshal(proxysvc.SSConfig{
			Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA==",
		})
		ack := deploySingBoxSS(wsproto.ProxyServiceApply{
			InstanceID: 900002, Protocol: "shadowsocks", Core: "sing-box", ListenPort: 38388, Config: raw,
		})
		if !ack.OK {
			t.Fatalf("sing-box deploy: %+v", ack)
		}
		if ack.DryRun {
			t.Fatal("expected live deploy")
		}
		stopPIDFile(filepath.Join(state, "sing-box", "instance-900002.pid"))
		t.Log("sing-box deploy ok")
	}
}
