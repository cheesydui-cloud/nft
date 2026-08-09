package proxysvc_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nft/internal/proxysvc"
)

// TestLiveCoreConfigIfBinariesPresent runs xray/sing-box config check + brief
// listen when binaries are on PATH or under NFT_CORE_BIN_DIR. Skips otherwise
// so CI stays green without shipping third-party cores.
func TestLiveCoreConfigIfBinariesPresent(t *testing.T) {
	dir := t.TempDir()
	priv, pub := proxysvc.GenerateRealityKeyPair()
	vlessRaw, err := json.Marshal(proxysvc.VLESSConfig{
		UUID: "11111111-2222-3333-4444-555555555555", ServerName: "www.cloudflare.com",
		ServerPort: 443, PrivateKey: priv, PublicKey: pub, ShortID: "aabbccdd",
		Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	xcfg, err := proxysvc.BuildXrayVLESSConfig(38443, vlessRaw)
	if err != nil {
		t.Fatal(err)
	}
	xPath := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(xPath, xcfg, 0o600); err != nil {
		t.Fatal(err)
	}

	ssRaw, err := json.Marshal(proxysvc.SSConfig{
		Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	scfg, err := proxysvc.BuildSingBoxSSConfig(38388, ssRaw)
	if err != nil {
		t.Fatal(err)
	}
	sPath := filepath.Join(dir, "sb.json")
	if err := os.WriteFile(sPath, scfg, 0o600); err != nil {
		t.Fatal(err)
	}

	// Always assert builders produce non-trivial JSON.
	if len(xcfg) < 100 || len(scfg) < 50 {
		t.Fatalf("configs too small x=%d s=%d", len(xcfg), len(scfg))
	}
	uri, err := proxysvc.BuildShareURI("vless", "t", "1.2.3.4", 38443, vlessRaw)
	if err != nil || !strings.HasPrefix(uri, "vless://") || !strings.Contains(uri, "pbk=") {
		t.Fatalf("vless uri: %v %s", err, uri)
	}
	uriS, err := proxysvc.BuildShareURI("shadowsocks", "t", "1.2.3.4", 38388, ssRaw)
	if err != nil || !strings.HasPrefix(uriS, "ss://") {
		t.Fatalf("ss uri: %v %s", err, uriS)
	}

	xray := findBin("xray")
	if xray != "" {
		// config test
		cmd := exec.Command(xray, "run", "-test", "-c", xPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			cmd = exec.Command(xray, "-test", "-config", xPath)
			out2, err2 := cmd.CombinedOutput()
			if err2 != nil {
				t.Fatalf("xray config test failed: %v (%s) / %v (%s)", err, out, err2, out2)
			}
		}
		// brief run
		logf := filepath.Join(dir, "xray.log")
		run := exec.Command(xray, "run", "-c", xPath)
		lf, _ := os.Create(logf)
		run.Stdout, run.Stderr = lf, lf
		if err := run.Start(); err != nil {
			t.Fatalf("xray start: %v", err)
		}
		time.Sleep(800 * time.Millisecond)
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
		_ = lf.Close()
		t.Log("xray config+start ok")
	} else {
		t.Log("xray not installed; config build verified only")
	}

	sb := findBin("sing-box", "singbox")
	if sb != "" {
		cmd := exec.Command(sb, "check", "-c", sPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sing-box check failed: %v (%s)", err, out)
		}
		logf := filepath.Join(dir, "sb.log")
		run := exec.Command(sb, "run", "-c", sPath)
		lf, _ := os.Create(logf)
		run.Stdout, run.Stderr = lf, lf
		if err := run.Start(); err != nil {
			t.Fatalf("sing-box start: %v", err)
		}
		time.Sleep(800 * time.Millisecond)
		_ = run.Process.Kill()
		_, _ = run.Process.Wait()
		_ = lf.Close()
		t.Log("sing-box config+start ok")
	} else {
		t.Log("sing-box not installed; config build verified only")
	}
}

func findBin(names ...string) string {
	extra := os.Getenv("NFT_CORE_BIN_DIR")
	for _, n := range names {
		if extra != "" {
			p := filepath.Join(extra, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}
