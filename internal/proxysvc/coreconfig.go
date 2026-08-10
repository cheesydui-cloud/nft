package proxysvc

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BuildXrayVLESSConfig builds a standalone xray-core JSON config for one VLESS inbound.
// Deploy requires REALITY. Xray-core REALITY only accepts tcp (RAW) / xhttp / gRPC —
// ws and httpupgrade are rejected early with a clear error (xray 26.x).
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
	privKey, err := normalizeRealityPrivateKey(strings.TrimSpace(c.PrivateKey))
	if err != nil {
		return nil, err
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
	// Xray REALITY: "REALITY only supports RAW, XHTTP and gRPC for now."
	// We expose tcp + xhttp in the panel; reject the rest before writing config.
	switch network {
	case "tcp", "xhttp":
	case "ws", "httpupgrade", "websocket":
		return nil, fmt.Errorf(
			"Xray REALITY 不支持传输层 %q（仅 tcp / xhttp）。请改回 tcp（推荐）或 xhttp 后重新发布",
			network,
		)
	default:
		return nil, fmt.Errorf("unsupported network %q for REALITY (tcp/xhttp)", network)
	}

	// Vision flow is only valid with tcp (+ REALITY). Strip on other transports
	// so xray does not reject the config.
	flow := strings.TrimSpace(c.Flow)
	if flow == "none" || flow == "off" || flow == "关" || flow == "-" {
		flow = ""
	}
	if network != "tcp" {
		flow = ""
	}
	if flow != "" && flow != "xtls-rprx-vision" {
		return nil, fmt.Errorf("unsupported flow %q（仅 xtls-rprx-vision 或关）", flow)
	}
	client := map[string]any{"id": c.UUID}
	if flow != "" {
		client["flow"] = flow
	}

	sid := strings.TrimSpace(c.ShortID)
	if sid != "" {
		if err := validateRealityShortID(sid); err != nil {
			return nil, err
		}
	}
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

	// Server-side REALITY: dest/serverNames/privateKey/shortIds only.
	// spiderX / fingerprint are client outbound fields — do not put them here.
	reality := map[string]any{
		"show":        false,
		"dest":        dest,
		"xver":        0,
		"serverNames": []string{sni},
		"privateKey":  privKey,
		"shortIds":    shortIDs,
		"maxTimeDiff": maxDiff,
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
	if network == "xhttp" {
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

	decryption := normalizeVLESSEncToken(c.Decryption)
	if decryption == "" {
		decryption = "none"
	}
	if err := validateVLESSDecryption(decryption); err != nil {
		return nil, err
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

// normalizeRealityPrivateKey matches xray: base64.RawURLEncoding of exactly 32 bytes.
// Accepts padded / std alphabet paste and re-encodes to RawURL for the config file.
func normalizeRealityPrivateKey(priv string) (string, error) {
	priv = strings.TrimSpace(priv)
	if priv == "" {
		return "", fmt.Errorf("reality private_key missing")
	}
	raw := strings.TrimRight(priv, "=")
	if b, err := base64.RawURLEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	// std with padding
	if b, err := base64.StdEncoding.DecodeString(priv); err == nil && len(b) == 32 {
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	return "", fmt.Errorf("reality private_key 无效（需 xray x25519 输出的 32 字节 base64url）。请点「生成密钥」重新生成")
}

// validateRealityShortID matches xray: hex, length 0..16 (decoded into 8-byte slot).
func validateRealityShortID(sid string) error {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil
	}
	if len(sid) > 16 {
		return fmt.Errorf("short_id 过长（最多 16 个十六进制字符）: %q", sid)
	}
	if len(sid)%2 != 0 {
		return fmt.Errorf("short_id 须为偶数位十六进制: %q", sid)
	}
	if _, err := hex.DecodeString(sid); err != nil {
		return fmt.Errorf("short_id 须为十六进制: %q", sid)
	}
	return nil
}

// normalizeVLESSEncToken strips whitespace and surrounding quotes that some
// xray vlessenc outputs (or copy-paste) wrap around the material.
func normalizeVLESSEncToken(s string) string {
	s = strings.TrimSpace(s)
	for {
		if len(s) >= 2 {
			if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
				s = strings.TrimSpace(s[1 : len(s)-1])
				continue
			}
		}
		// Leading quote only (truncated paste / regex capture of "token)
		if strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") {
			s = strings.TrimSpace(s[1:])
			continue
		}
		if strings.HasSuffix(s, "\"") || strings.HasSuffix(s, "'") {
			s = strings.TrimSpace(s[:len(s)-1])
			continue
		}
		break
	}
	return s
}

// validateVLESSDecryption accepts "none" or mlkem768x25519plus.* server decryption string.
func validateVLESSDecryption(dec string) error {
	dec = normalizeVLESSEncToken(dec)
	if dec == "" || dec == "none" {
		return nil
	}
	// Client encryption string must not be pasted into server decryption.
	// Client material often contains "0rtt"; server uses "600s" (or similar) in segment 2.
	low := strings.ToLower(dec)
	if strings.HasPrefix(low, "mlkem768x25519plus.") {
		parts := strings.Split(dec, ".")
		if len(parts) < 4 {
			return fmt.Errorf("decryption 格式不完整（mlkem768x25519plus 段数不足）。请用「生成 vlessenc」写入，或清空用 none")
		}
		switch strings.ToLower(parts[1]) {
		case "native", "xorpub", "random":
		default:
			return fmt.Errorf("decryption 模式无效 %q（native/xorpub/random）", parts[1])
		}
		// Heuristic: client encryption usually has "0rtt" in the third segment.
		if strings.EqualFold(parts[2], "0rtt") {
			return fmt.Errorf("decryption 看起来是客户端 encryption（含 0rtt）。服务端应填 vlessenc 的 Decryption（通常含 600s），请点「生成 vlessenc」或对调字段")
		}
		return nil
	}
	return fmt.Errorf("decryption 不支持 %q：须为 none 或 xray vlessenc 的服务端 decryption 整串。勿把客户端 encryption 填到服务端", truncateForErr(dec, 48))
}

func truncateForErr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
