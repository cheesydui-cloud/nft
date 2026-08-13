package proxysvc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEgressStrategies(t *testing.T) {
	if EgressDomainStrategy(false, true) != "UseIPv4" {
		t.Fatal("xray block v6")
	}
	if EgressSingBoxStrategy(false, true) != "ipv4_only" {
		t.Fatal("sing-box block v6")
	}
	if EgressMitaDualStack(false, true) != "ONLY_IPv4" {
		t.Fatal("mita block v6")
	}
	if EgressDomainStrategy(false, false) != "" || EgressMitaDualStack(true, true) != "" {
		t.Fatal("no lock / both locked → leave default")
	}
}

func TestApplyXrayEgressPolicyUseIPv4(t *testing.T) {
	priv, pub := GenerateRealityKeyPair()
	raw, err := json.Marshal(VLESSConfig{
		UUID:       "11111111-2222-3333-4444-555555555555",
		ServerName: "www.cloudflare.com",
		ServerPort: 443,
		PrivateKey: priv,
		PublicKey:  pub,
		Security:   "reality",
		Network:    "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildXrayVLESSConfig(8443, raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplyXrayEgressPolicy(cfg, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"domainStrategy": "UseIPv4"`) {
		t.Fatalf("missing UseIPv4:\n%s", s)
	}
	if strings.Contains(s, `"domainStrategy": "AsIs"`) {
		t.Fatalf("AsIs should be replaced:\n%s", s)
	}
}

func TestApplySingBoxEgressPolicyIPv4Only(t *testing.T) {
	raw, err := json.Marshal(SSConfig{
		Method:   "2022-blake3-aes-128-gcm",
		Password: "AAAAAAAAAAAAAAAAAAAAAA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildSingBoxSSConfig(8388, raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ApplySingBoxEgressPolicy(cfg, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, `"domain_strategy"`) {
		t.Fatalf("legacy domain_strategy fails sing-box 1.12+ check:\n%s", s)
	}
	for _, want := range []string{
		`"type": "local"`,
		`"tag": "nft-local"`,
		`"domain_resolver"`,
		`"server": "nft-local"`,
		`"strategy": "ipv4_only"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
}

func TestApplySingBoxEgressPolicyKeepsExistingDNS(t *testing.T) {
	cfg := []byte(`{
		  "outbounds": [{"type":"direct","tag":"direct-out"}],
		  "dns": {"servers":[{"type":"local","tag":"local"}]}
		}`)
	out, err := ApplySingBoxEgressPolicy(cfg, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"server": "local"`) {
		t.Fatalf("should reuse existing dns tag:\n%s", s)
	}
	if strings.Contains(s, `"nft-local"`) {
		t.Fatalf("should not add a second local resolver:\n%s", s)
	}
	if !strings.Contains(s, `"strategy": "ipv4_only"`) {
		t.Fatalf("existing dns should inherit strategy:\n%s", s)
	}
}

func TestApplySingBoxEgressPolicyPassesSingBoxCheck(t *testing.T) {
	bin := findSingBoxForTest()
	if bin == "" {
		t.Skip("sing-box binary not available")
	}
	raw, err := json.Marshal(SSConfig{
		Method:   "2022-blake3-aes-128-gcm",
		Password: "AAAAAAAAAAAAAAAAAAAAAA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildSingBoxSSConfig(8388, raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = ApplySingBoxEgressPolicy(cfg, false, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = InjectSingBoxClashAPI(cfg, 19091)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ss.json")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "check", "-c", path).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check: %v (%s)\n%s", err, out, cfg)
	}
}

func findSingBoxForTest() string {
	if p := strings.TrimSpace(os.Getenv("SINGBOX")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	candidates := []string{
		"/tmp/nft-singbox-check/sing-box-1.13.18-darwin-arm64/sing-box",
		"/usr/local/bin/sing-box",
		"/opt/homebrew/bin/sing-box",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
