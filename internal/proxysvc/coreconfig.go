package proxysvc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BuildXrayVLESSConfig builds a standalone xray-core JSON config for one VLESS+REALITY inbound.
func BuildXrayVLESSConfig(listenPort int, raw json.RawMessage) ([]byte, error) {
	var c VLESSConfig
	if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
		return nil, err
	}
	if listenPort <= 0 {
		listenPort = c.ListenPort
	}
	if listenPort <= 0 || listenPort > 65535 {
		return nil, fmt.Errorf("invalid listen port %d", listenPort)
	}
	if c.UUID == "" {
		return nil, fmt.Errorf("vless uuid missing")
	}
	sec := strings.ToLower(strings.TrimSpace(c.Security))
	if sec == "" {
		sec = "reality"
	}
	if sec != "reality" {
		return nil, fmt.Errorf("only reality security is supported for deploy, got %q", c.Security)
	}
	if c.PrivateKey == "" {
		return nil, fmt.Errorf("reality private_key missing")
	}
	sni := strings.TrimSpace(c.ServerName)
	if sni == "" {
		return nil, fmt.Errorf("server_name (REALITY SNI / dest) required for deploy")
	}
	destPort := c.ServerPort
	if destPort <= 0 {
		destPort = 443
	}
	dest := sni + ":" + strconv.Itoa(destPort)
	network := c.Network
	if network == "" {
		network = "tcp"
	}
	flow := c.Flow
	client := map[string]any{"id": c.UUID}
	if flow != "" {
		client["flow"] = flow
	}
	shortIDs := []string{""}
	if sid := strings.TrimSpace(c.ShortID); sid != "" {
		shortIDs = []string{sid, ""}
	}
	maxDiff := c.MaxTimeDifference
	if maxDiff <= 0 {
		maxDiff = 60000
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "vless-in",
				"listen":   "0.0.0.0",
				"port":     listenPort,
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    []any{client},
					"decryption": "none",
				},
				"streamSettings": map[string]any{
					"network":  network,
					"security": "reality",
					"realitySettings": map[string]any{
						"show":        false,
						"dest":        dest,
						"xver":        0,
						"serverNames": []string{sni},
						"privateKey":  c.PrivateKey,
						"shortIds":    shortIDs,
						"maxTimeDiff": maxDiff,
					},
				},
				"sniffing": map[string]any{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
		},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "tag": "direct"},
			map[string]any{"protocol": "blackhole", "tag": "block"},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// BuildSingBoxSSConfig builds a standalone sing-box JSON config for one Shadowsocks inbound.
func BuildSingBoxSSConfig(listenPort int, raw json.RawMessage) ([]byte, error) {
	var c SSConfig
	if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
		return nil, err
	}
	if listenPort <= 0 {
		listenPort = c.ListenPort
	}
	if listenPort <= 0 || listenPort > 65535 {
		return nil, fmt.Errorf("invalid listen port %d", listenPort)
	}
	method := strings.TrimSpace(c.Method)
	if method == "" {
		method = "2022-blake3-aes-128-gcm"
	}
	if c.Password == "" {
		return nil, fmt.Errorf("ss password missing")
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{
			map[string]any{
				"type":        "shadowsocks",
				"tag":         "ss-in",
				"listen":      "::",
				"listen_port": listenPort,
				"method":      method,
				"password":    c.Password,
			},
		},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct"},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
