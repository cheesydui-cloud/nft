package proxysvc

import (
	"encoding/json"
	"fmt"
)

// EgressDomainStrategy is the xray routing / freedom domainStrategy for a
// blocked-family policy. Empty means leave the builder default (AsIs).
func EgressDomainStrategy(blockV4, blockV6 bool) string {
	switch {
	case blockV6 && !blockV4:
		return "UseIPv4"
	case blockV4 && !blockV6:
		return "UseIPv6"
	default:
		return ""
	}
}

// EgressSingBoxStrategy is sing-box domain_strategy / dns.strategy.
func EgressSingBoxStrategy(blockV4, blockV6 bool) string {
	switch {
	case blockV6 && !blockV4:
		return "ipv4_only"
	case blockV4 && !blockV6:
		return "ipv6_only"
	default:
		return ""
	}
}

// EgressMitaDualStack is mita dns.dualStack. Empty = omit (mita default).
func EgressMitaDualStack(blockV4, blockV6 bool) string {
	switch {
	case blockV6 && !blockV4:
		return "ONLY_IPv4"
	case blockV4 && !blockV6:
		return "ONLY_IPv6"
	default:
		return ""
	}
}

// ApplyXrayEgressPolicy pins freedom + routing to one address family so Happy
// Eyeballs cannot pick AAAA (or A) when that stack is disabled on the node.
func ApplyXrayEgressPolicy(cfg []byte, blockV4, blockV6 bool) ([]byte, error) {
	strat := EgressDomainStrategy(blockV4, blockV6)
	if strat == "" || len(cfg) == 0 {
		return cfg, nil
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, fmt.Errorf("xray egress policy: %w", err)
	}
	if outs, ok := m["outbounds"].([]any); ok {
		for _, o := range outs {
			om, ok := o.(map[string]any)
			if !ok {
				continue
			}
			proto, _ := om["protocol"].(string)
			if proto != "freedom" {
				continue
			}
			settings, _ := om["settings"].(map[string]any)
			if settings == nil {
				settings = map[string]any{}
				om["settings"] = settings
			}
			settings["domainStrategy"] = strat
		}
	}
	routing, _ := m["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
		m["routing"] = routing
	}
	routing["domainStrategy"] = strat
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApplySingBoxEgressPolicy pins direct outbounds and dns to ipv4_only / ipv6_only.
func ApplySingBoxEgressPolicy(cfg []byte, blockV4, blockV6 bool) ([]byte, error) {
	strat := EgressSingBoxStrategy(blockV4, blockV6)
	if strat == "" || len(cfg) == 0 {
		return cfg, nil
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		return nil, fmt.Errorf("sing-box egress policy: %w", err)
	}
	if outs, ok := m["outbounds"].([]any); ok {
		for _, o := range outs {
			om, ok := o.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := om["type"].(string)
			if typ == "direct" || typ == "socks" || typ == "shadowsocks" || typ == "vless" || typ == "trojan" {
				om["domain_strategy"] = strat
			}
		}
	}
	dns, _ := m["dns"].(map[string]any)
	if dns == nil {
		dns = map[string]any{}
		m["dns"] = dns
	}
	dns["strategy"] = strat
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return out, nil
}
