package proxysvc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
				"port":    apiPort,
				"network": "tcp",
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


// InjectSingBoxSocksOutbound replaces direct-out with a SOCKS5 outbound and
// points route.final at it. Used for rule-scoped protocol entry (SS/anytls/… + SK5 exit).
// socksURI is socks5://[user:pass@]host:port.
func InjectSingBoxSocksOutbound(cfg []byte, socksURI string) ([]byte, error) {
	socksURI = strings.TrimSpace(socksURI)
	if socksURI == "" {
		return cfg, nil
	}
	u, err := url.Parse(socksURI)
	if err != nil {
		return nil, fmt.Errorf("socks uri: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "socks5" && scheme != "socks" {
		return nil, fmt.Errorf("socks scheme %q", u.Scheme)
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return nil, fmt.Errorf("socks missing host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("socks port invalid")
	}
	ob := map[string]any{
		"type":        "socks",
		"tag":         "sk5-out",
		"server":      host,
		"server_port": port,
		"version":     "5",
	}
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		if user != "" {
			ob["username"] = user
			ob["password"] = pass
		}
	}
	return injectSingBoxFinalOutbound(cfg, ob, "sk5-out")
}

// InjectSingBoxRedirectOutbound forces all traffic to a fixed host:port via a
// sing-box "direct" outbound with override_address/override_port (tunnel semantics
// for protocol entry + direct exit, analogous to xray freedom redirect).
func InjectSingBoxRedirectOutbound(cfg []byte, host string, port int) ([]byte, error) {
	host = strings.TrimSpace(host)
	if host == "" || port < 1 || port > 65535 {
		return nil, fmt.Errorf("redirect host:port invalid")
	}
	ob := map[string]any{
		"type":             "direct",
		"tag":              "redirect-out",
		"override_address": host,
		"override_port":    port,
	}
	return injectSingBoxFinalOutbound(cfg, ob, "redirect-out")
}

// injectSingBoxFinalOutbound drops prior direct/sk5/redirect finals and pins route.final.
func injectSingBoxFinalOutbound(cfg []byte, ob map[string]any, finalTag string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, err
	}
	outs, _ := m["outbounds"].([]any)
	filtered := make([]any, 0, len(outs)+1)
	for _, o := range outs {
		om, ok := o.(map[string]any)
		if !ok {
			filtered = append(filtered, o)
			continue
		}
		tag, _ := om["tag"].(string)
		if tag == "direct-out" || tag == "sk5-out" || tag == "redirect-out" || tag == finalTag {
			continue
		}
		filtered = append(filtered, o)
	}
	filtered = append([]any{ob}, filtered...)
	m["outbounds"] = filtered

	route, _ := m["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
	}
	route["final"] = finalTag
	m["route"] = route
	return json.MarshalIndent(m, "", "  ")
}
