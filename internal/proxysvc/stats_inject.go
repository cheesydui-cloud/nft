package proxysvc

import (
	"encoding/json"
	"fmt"
)

// InjectXrayStatsAPI adds stats + gRPC StatsService on 127.0.0.1:apiPort so the
// agent can poll inbound uplink/downlink. Mutates a full xray config JSON.
// Inbound tag is expected to be "vless-in" (BuildXrayVLESSConfig).
func InjectXrayStatsAPI(cfg []byte, apiPort int) ([]byte, error) {
	if apiPort <= 0 || apiPort > 65535 {
		return nil, fmt.Errorf("invalid api port %d", apiPort)
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, err
	}
	m["stats"] = map[string]any{}
	m["api"] = map[string]any{
		"tag":      "api",
		"services": []string{"StatsService"},
	}
	m["policy"] = map[string]any{
		"system": map[string]any{
			"statsInboundUplink":   true,
			"statsInboundDownlink": true,
			"statsOutboundUplink":  true,
			"statsOutboundDownlink": true,
		},
	}
	// dokodemo-door API listener (loopback only).
	apiInbound := map[string]any{
		"listen":   "127.0.0.1",
		"port":     apiPort,
		"protocol": "dokodemo-door",
		"settings": map[string]any{
			"address": "127.0.0.1",
		},
		"tag": "api",
	}
	inbounds, _ := m["inbounds"].([]any)
	// Ensure no duplicate api inbound from a previous inject.
	filtered := make([]any, 0, len(inbounds)+1)
	for _, in := range inbounds {
		if im, ok := in.(map[string]any); ok {
			if tag, _ := im["tag"].(string); tag == "api" {
				continue
			}
		}
		filtered = append(filtered, in)
	}
	filtered = append(filtered, apiInbound)
	m["inbounds"] = filtered

	// Route api tag to api outbound.
	routing, _ := m["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
	}
	rules, _ := routing["rules"].([]any)
	// Drop prior api rules then prepend.
	clean := make([]any, 0, len(rules)+1)
	for _, r := range rules {
		if rm, ok := r.(map[string]any); ok {
			if inboundTag, ok := rm["inboundTag"].([]any); ok && len(inboundTag) == 1 {
				if s, _ := inboundTag[0].(string); s == "api" {
					continue
				}
			}
		}
		clean = append(clean, r)
	}
	apiRule := map[string]any{
		"inboundTag": []string{"api"},
		"outboundTag": "api",
		"type":       "field",
	}
	routing["rules"] = append([]any{apiRule}, clean...)
	m["routing"] = routing
	// outboundTag "api" refers to the api module tag — do not add a freedom
	// outbound with the same tag (would shadow StatsService).
	return json.MarshalIndent(m, "", "  ")
}

// InjectSingBoxClashAPI enables experimental.clash_api on 127.0.0.1:apiPort for
// traffic polling (connections uplink/downlink sum).
func InjectSingBoxClashAPI(cfg []byte, apiPort int) ([]byte, error) {
	if apiPort <= 0 || apiPort > 65535 {
		return nil, fmt.Errorf("invalid api port %d", apiPort)
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, err
	}
	exp, _ := m["experimental"].(map[string]any)
	if exp == nil {
		exp = map[string]any{}
	}
	exp["clash_api"] = map[string]any{
		"external_controller": fmt.Sprintf("127.0.0.1:%d", apiPort),
		// Empty secret: loopback only; agent is local.
		"secret": "",
	}
	m["experimental"] = exp
	return json.MarshalIndent(m, "", "  ")
}
