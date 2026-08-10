package proxysvc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BuildXrayVLESSConfig builds a standalone xray-core JSON config for one VLESS inbound.
// Deploy currently requires REALITY; transports tcp/ws/httpupgrade/xhttp are supported.
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
		return nil, fmt.Errorf("目前部署仅支持 REALITY（security=reality），got %q", c.Security)
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
	network := strings.ToLower(strings.TrimSpace(c.Network))
	if network == "" {
		network = "tcp"
	}
	switch network {
	case "tcp", "ws", "httpupgrade", "xhttp":
	default:
		return nil, fmt.Errorf("unsupported network %q (tcp/ws/httpupgrade/xhttp)", network)
	}

	// Vision flow is only valid with tcp (+ REALITY). Strip on other transports
	// so xray does not reject the config.
	flow := strings.TrimSpace(c.Flow)
	if flow == "none" || flow == "off" {
		flow = ""
	}
	if network != "tcp" {
		flow = ""
	}
	client := map[string]any{"id": c.UUID}
	if flow != "" {
		client["flow"] = flow
	}

	sid := strings.TrimSpace(c.ShortID)
	var shortIDs []string
	if sid == "" {
		shortIDs = []string{""}
	} else if c.AllowEmptyShortID {
		// Loose: configured sid + empty (older clients / misconfigured sid still work).
		shortIDs = []string{sid, ""}
	} else {
		// Strict default: only the configured short_id.
		shortIDs = []string{sid}
	}

	maxDiff := c.MaxTimeDifference
	if maxDiff <= 0 {
		maxDiff = 60000
	}

	reality := map[string]any{
		"show":        false,
		"dest":        dest,
		"xver":        0,
		"serverNames": []string{sni},
		"privateKey":  c.PrivateKey,
		"shortIds":    shortIDs,
		"maxTimeDiff": maxDiff,
	}
	if spx := strings.TrimSpace(c.SpiderX); spx != "" {
		reality["spiderX"] = spx
	}

	stream := map[string]any{
		"network":         network,
		"security":        "reality",
		"realitySettings": reality,
	}
	// Transport-specific settings
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = "/"
	}
	hostHdr := strings.TrimSpace(c.Host)
	switch network {
	case "ws":
		ws := map[string]any{"path": path}
		if hostHdr != "" {
			ws["headers"] = map[string]any{"Host": hostHdr}
		}
		stream["wsSettings"] = ws
	case "httpupgrade":
		hu := map[string]any{"path": path}
		if hostHdr != "" {
			hu["host"] = hostHdr
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp":
		mode := strings.TrimSpace(c.XHTTPMode)
		if mode == "" {
			mode = "auto"
		}
		xh := map[string]any{"path": path, "mode": mode}
		if hostHdr != "" {
			xh["host"] = hostHdr
		}
		stream["xhttpSettings"] = xh
	}

	decryption := strings.TrimSpace(c.Decryption)
	if decryption == "" {
		decryption = "none"
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
					"decryption": decryption,
				},
				"streamSettings": stream,
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
				"listen":      "0.0.0.0",
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
