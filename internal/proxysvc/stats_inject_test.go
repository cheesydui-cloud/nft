package proxysvc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectXrayStatsAPI(t *testing.T) {
	base, err := BuildXrayVLESSConfig(443, json.RawMessage(`{
		"uuid":"11111111-2222-3333-4444-555555555555",
		"security":"reality",
		"network":"tcp",
		"server_name":"www.cloudflare.com",
		"private_key":"MMX7m0Mj3faZ7/M4+8tFr6R8a0q0q0q0q0q0q0q0q0s",
		"short_id":"aabbccdd"
	}`))
	// private_key may fail normalize — use a real-looking 32-byte rawurl key if needed
	if err != nil {
		// generate valid key
		priv, _ := GenerateRealityKeyPair()
		base, err = BuildXrayVLESSConfig(443, mustJSON(map[string]any{
			"uuid":        "11111111-2222-3333-4444-555555555555",
			"security":    "reality",
			"network":     "tcp",
			"server_name": "www.cloudflare.com",
			"private_key": priv,
			"short_id":    "aabbccdd",
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
	out, err := InjectXrayStatsAPI(base, 10085)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "StatsService") {
		t.Fatalf("missing StatsService: %s", s)
	}
	if !strings.Contains(s, "10085") {
		t.Fatalf("missing api port: %s", s)
	}
	if !strings.Contains(s, "statsInboundUplink") {
		t.Fatalf("missing policy stats flags: %s", s)
	}
	// Valid JSON
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
}

func TestInjectSingBoxClashAPI(t *testing.T) {
	base, err := BuildSingBoxSSConfig(8388, mustJSON(map[string]any{
		"method":   "aes-128-gcm",
		"password": "secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := InjectSingBoxClashAPI(base, 19090)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "127.0.0.1:19090") {
		t.Fatalf("missing controller: %s", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	exp := m["experimental"].(map[string]any)
	if exp["clash_api"] == nil {
		t.Fatal("clash_api missing")
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
