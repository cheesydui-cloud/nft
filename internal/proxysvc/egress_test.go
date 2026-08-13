package proxysvc

import (
	"encoding/json"
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
	if !strings.Contains(s, `"domain_strategy": "ipv4_only"`) {
		t.Fatalf("missing ipv4_only on outbound:\n%s", s)
	}
	if !strings.Contains(s, `"strategy": "ipv4_only"`) {
		t.Fatalf("missing dns.strategy:\n%s", s)
	}
}
