package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nft/internal/proxysvc"
)

func findTestXray(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/tmp/xray", "xray"} {
		if p == "xray" {
			if b, err := exec.LookPath("xray"); err == nil {
				return b
			}
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	t.Skip("xray binary not found for live test")
	return ""
}

func TestLiveVlessEncParseAndXrayConfig(t *testing.T) {
	bin := findTestXray(t)

	// 1) Native X25519 (panel default) — must pass xray -test
	enc, dec := proxysvc.GenerateVlessEncX25519()
	if !strings.Contains(enc, "0rtt") || !strings.Contains(dec, "600s") {
		t.Fatalf("roles wrong enc=%q dec=%q", enc, dec)
	}
	if len(enc) > 100 || len(dec) > 100 {
		t.Fatalf("native X25519 should be short: enc=%d dec=%d", len(enc), len(dec))
	}
	t.Logf("native X25519 enc len=%d dec len=%d", len(enc), len(dec))

	// 2) Parse real xray vlessenc stdout — default short, PQ long
	cmd := exec.Command(bin, "vlessenc")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("vlessenc: %v\n%s", err, out)
	}
	raw := string(out)
	encP, decP, ok := parseVlessEncOutputPrefer(raw, false)
	if !ok || len(encP) > 120 {
		t.Fatalf("parse default short failed enc len=%d ok=%v", len(encP), ok)
	}
	encPQ, decPQ, ok := parseVlessEncOutputPrefer(raw, true)
	if !ok || len(encPQ) < 500 {
		t.Fatalf("parse PQ failed enc len=%d", len(encPQ))
	}
	_ = decP
	t.Logf("parse X25519 len=%d PQ enc len=%d dec len=%d", len(encP), len(encPQ), len(decPQ))

	// 3) Full chain: EnsureSecrets → BuildXray → xray -test → BuildShareURI
	priv, pub := proxysvc.GenerateRealityKeyPair()
	cfgJSON, _ := json.Marshal(map[string]any{
		"uuid": "83aca93f-7528-44d8-81f3-28f60f9b4eee", "flow": "xtls-rprx-vision",
		"network": "tcp", "security": "reality", "server_name": "www.kyoto-u.ac.jp",
		"server_port": 443, "fingerprint": "chrome", "private_key": priv, "public_key": pub,
		"short_id": "25504571db26a342", "encryption": enc, "decryption": dec, "listen_port": 34675,
	})
	fixed, err := proxysvc.EnsureSecrets("vless", cfgJSON)
	if err != nil {
		t.Fatal(err)
	}
	xcfg, err := proxysvc.BuildXrayVLESSConfig(34675, fixed)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(xcfg, &parsed); err != nil {
		t.Fatal(err)
	}
	gotDec := parsed["inbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)["decryption"].(string)
	if gotDec != dec {
		t.Fatalf("server decryption=%q want %q", gotDec, dec)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.json")
	if err := os.WriteFile(cfgPath, xcfg, 0o600); err != nil {
		t.Fatal(err)
	}
	testOut, testErr := exec.Command(bin, "run", "-test", "-c", cfgPath).CombinedOutput()
	if testErr != nil {
		t.Fatalf("xray -test native pair failed: %v\n%s\n%s", testErr, testOut, xcfg)
	}
	t.Log("xray -test native X25519 OK")

	uri, err := proxysvc.BuildShareURI("vless", "TEST7", "82.22.26.185", 34675, fixed)
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range []string{"encryption=", "0rtt", "security=reality", "flow=xtls-rprx-vision", "pbk=", "sni=www.kyoto-u.ac.jp"} {
		if !strings.Contains(uri, need) {
			t.Fatalf("uri missing %q: %s", need, uri)
		}
	}
	if strings.Contains(uri, "encryption=%22") {
		t.Fatalf("uri has quoted encryption: %s", uri)
	}
	t.Logf("share URI len=%d\n%s", len(uri), uri)

	// 4) PQ pair from real xray also validates
	cfgPQ, _ := json.Marshal(map[string]any{
		"uuid": "83aca93f-7528-44d8-81f3-28f60f9b4eee", "flow": "xtls-rprx-vision",
		"network": "tcp", "security": "reality", "server_name": "www.kyoto-u.ac.jp",
		"server_port": 443, "private_key": priv, "public_key": pub, "short_id": "aabbccdd",
		"encryption": encPQ, "decryption": decPQ,
	})
	fixedPQ, err := proxysvc.EnsureSecrets("vless", cfgPQ)
	if err != nil {
		t.Fatal(err)
	}
	xcfgPQ, err := proxysvc.BuildXrayVLESSConfig(34676, fixedPQ)
	if err != nil {
		t.Fatal(err)
	}
	cfgPathPQ := filepath.Join(dir, "pq.json")
	_ = os.WriteFile(cfgPathPQ, xcfgPQ, 0o600)
	if out2, err2 := exec.Command(bin, "run", "-test", "-c", cfgPathPQ).CombinedOutput(); err2 != nil {
		t.Fatalf("xray -test PQ failed: %v\n%s", err2, out2)
	}
	t.Log("xray -test PQ OK")

	// 5) generateVlessEncPair API path (native default)
	enc2, dec2, ver, err := generateVlessEncPair("x25519")
	if err != nil {
		t.Fatal(err)
	}
	if ver != "native-x25519" || len(enc2) > 100 {
		t.Fatalf("generate default: ver=%s enc len=%d", ver, len(enc2))
	}
	_ = dec2
}
