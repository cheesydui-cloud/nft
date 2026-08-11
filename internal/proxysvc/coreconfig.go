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
// Supports security=reality | tls | none with the transport matrix in NetworksForSecurity.
// For TLS, prefer cert_file/key_file (agent-written paths); falls back to cert_pem/key_pem
// only when file paths are set by deploy before calling this builder.
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
	sec := NormalizeSecurity(c.Security)
	c.Security = sec
	network := NormalizeNetwork(c.Network)
	c.Network = network
	if err := ValidateVLESSDeploy(&c); err != nil {
		return nil, err
	}

	// Vision flow only on tcp + (reality|tls).
	flow := strings.TrimSpace(c.Flow)
	if flow == "none" || flow == "off" || flow == "关" || flow == "-" {
		flow = ""
	}
	if !VisionAllowed(sec, network) {
		flow = ""
	}
	if flow != "" && flow != "xtls-rprx-vision" {
		return nil, fmt.Errorf("unsupported flow %q（仅 xtls-rprx-vision 或关）", flow)
	}
	client := map[string]any{"id": c.UUID}
	if flow != "" {
		client["flow"] = flow
	}

	stream := map[string]any{
		"network":  network,
		"security": sec,
	}

	switch sec {
	case "reality":
		privKey, err := normalizeRealityPrivateKey(strings.TrimSpace(c.PrivateKey))
		if err != nil {
			return nil, err
		}
		sni := strings.TrimSpace(c.ServerName)
		destPort := c.ServerPort
		if destPort <= 0 {
			destPort = 443
		}
		dest := sni + ":" + strconv.Itoa(destPort)
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
			shortIDs = []string{sid, ""}
		} else {
			shortIDs = []string{sid}
		}
		maxDiff := c.MaxTimeDifference
		if maxDiff <= 0 {
			maxDiff = 60000
		}
		// Server-side REALITY: dest/serverNames/privateKey/shortIds only.
		// spiderX / fingerprint are client outbound fields — do not put them here.
		stream["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        dest,
			"xver":        0,
			"serverNames": []string{sni},
			"privateKey":  privKey,
			"shortIds":    shortIDs,
			"maxTimeDiff": maxDiff,
		}
	case "tls":
		certFile := strings.TrimSpace(c.CertFile)
		keyFile := strings.TrimSpace(c.KeyFile)
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("TLS 部署需要 cert_file / key_file（由 agent 落盘后注入）")
		}
		tlsSettings := map[string]any{
			"certificates": []any{
				map[string]any{
					"certificateFile": certFile,
					"keyFile":         keyFile,
				},
			},
		}
		if alpn := parseALPNList(c.ALPN); len(alpn) > 0 {
			tlsSettings["alpn"] = alpn
		}
		stream["tlsSettings"] = tlsSettings
	case "none":
		// no security settings
	}

	// Transport-specific settings
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = "/"
	}
	hostHdr := strings.TrimSpace(c.Host)
	switch network {
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
	case "grpc":
		svcName := strings.TrimSpace(c.ServiceName)
		if svcName == "" {
			svcName = "GunService"
		}
		stream["grpcSettings"] = map[string]any{
			"serviceName": svcName,
		}
	}

	// Align swapped client/server material before writing inbound decryption.
	_, decAligned := alignVLESSEncPair(c.Encryption, c.Decryption)
	decryption := normalizeVLESSEncToken(decAligned)
	if decryption == "" {
		decryption = normalizeVLESSEncToken(c.Decryption)
	}
	if decryption == "" {
		decryption = "none"
	}
	if err := validateVLESSDecryption(decryption); err != nil {
		return nil, err
	}

	// TCP Fast Open: optional sockopt on stream (server listen path).
	if c.TcpFastOpen {
		stream["sockopt"] = map[string]any{"tcpFastOpen": true}
	}

	// Sniffing defaults on (historical behavior). Explicit false disables.
	sniffOn := true
	if c.Sniffing != nil {
		sniffOn = *c.Sniffing
	}

	inbound := map[string]any{
		"tag":      "vless-in",
		"listen":   "0.0.0.0",
		"port":     listenPort,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{client},
			"decryption": decryption,
		},
		"streamSettings": stream,
	}
	if sniffOn {
		inbound["sniffing"] = map[string]any{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
		}
	}

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			inbound,
		},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "tag": "direct"},
			map[string]any{"protocol": "blackhole", "tag": "block"},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// parseALPNList splits "h2,http/1.1" into ["h2","http/1.1"].
func parseALPNList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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
		if strings.HasSuffix(s, ",") {
			s = strings.TrimSpace(s[:len(s)-1])
			continue
		}
		break
	}
	return s
}

// alignVLESSEncPair fixes swapped client/server material.
// Client encryption uses 0rtt/1rtt in segment 3; server decryption uses ticket lifetime (e.g. 600s).
// A common failure mode was pasting or mis-parsing so server got 0rtt and client got 600s —
// handshake then fails while decryption=none still works (Weir-style "with enc works" vs ours broken).
func alignVLESSEncPair(encryption, decryption string) (enc, dec string) {
	enc = normalizeVLESSEncToken(encryption)
	dec = normalizeVLESSEncToken(decryption)
	if enc == "" || dec == "" || strings.EqualFold(enc, "none") || strings.EqualFold(dec, "none") {
		return enc, dec
	}
	encClient := vlessEncLooksClient(enc)
	decClient := vlessEncLooksClient(dec)
	encServer := vlessEncLooksServer(enc)
	decServer := vlessEncLooksServer(dec)
	// Clearly swapped: encryption looks like server and decryption looks like client.
	if (encServer && decClient) || (encServer && !decServer && decClient) || (decClient && encServer) {
		return dec, enc
	}
	// Only decryption looks client-side → swap.
	if decClient && !encClient {
		return dec, enc
	}
	// Only encryption looks server-side → swap.
	if encServer && !decServer {
		return dec, enc
	}
	return enc, dec
}

func vlessEncLooksClient(s string) bool {
	parts := strings.Split(strings.ToLower(normalizeVLESSEncToken(s)), ".")
	if len(parts) < 3 {
		return false
	}
	return parts[2] == "0rtt" || parts[2] == "1rtt"
}

func vlessEncLooksServer(s string) bool {
	parts := strings.Split(strings.ToLower(normalizeVLESSEncToken(s)), ".")
	if len(parts) < 3 {
		return false
	}
	seg := parts[2]
	if seg == "0rtt" || seg == "1rtt" {
		return false
	}
	if strings.HasSuffix(seg, "s") {
		return true
	}
	return strings.Contains(seg, "-")
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
		if strings.EqualFold(parts[2], "0rtt") || strings.EqualFold(parts[2], "1rtt") {
			return fmt.Errorf("decryption 看起来是客户端 encryption（含 0rtt）。服务端应填 vlessenc 的 Decryption（通常含 600s），请点「生成 vlessenc」或对调字段")
		}
		// Final auth material: base64url of 32 or 64 bytes (xray inbound).
		keyPart := parts[len(parts)-1]
		if len(keyPart) < 20 {
			return fmt.Errorf("decryption 密钥段过短。请用「生成 vlessenc」重新生成")
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
// Layout matches common production scripts (yyds): dual-stack listen, NTP, direct outbound.
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
	method := NormalizeSSMethod(c.Method)
	if c.Password == "" {
		return nil, fmt.Errorf("ss password missing")
	}
	if err := ValidateSSDeploy(&c); err != nil {
		return nil, err
	}
	listen := strings.TrimSpace(c.Listen)
	if listen == "" {
		listen = "::" // dual-stack (IPv4-mapped + IPv6), same as yyds install script
	}

	inbound := map[string]any{
		"type":        "shadowsocks",
		"tag":         "ss-in",
		"listen":      listen,
		"listen_port": listenPort,
		"method":      method,
		"password":    c.Password,
	}
	// tcp_fast_open remains a valid listen option; do NOT put sniff* on inbound —
	// sing-box ≥1.11 deprecated them and ≥1.13 fails check with:
	// "legacy inbound fields are deprecated".
	if c.TCPFastOpen {
		inbound["tcp_fast_open"] = true
	}
	if c.Multiplex {
		inbound["multiplex"] = map[string]any{
			"enabled": true,
			// smux is widely supported; clients that don't enable mux still work.
			"protocol": "smux",
			"padding":  false,
		}
	}

	cfg := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"inbounds": []any{
			inbound,
		},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct-out"},
		},
	}
	// Sniff via route action (sing-box 1.11+). Default on when Sniffing nil.
	sniffOn := true
	if c.Sniffing != nil {
		sniffOn = *c.Sniffing
	}
	if sniffOn {
		cfg["route"] = map[string]any{
			"rules": []any{
				map[string]any{
					"inbound": []string{"ss-in"},
					"action":  "sniff",
				},
			},
			// Single outbound: final direct is enough; no need for default_domain_resolver.
			"final": "direct-out",
		}
	}
	// NTP: default enabled (yyds uses time.apple.com).
	ntpOn := true
	if c.NTP != nil {
		ntpOn = *c.NTP
	}
	if ntpOn {
		cfg["ntp"] = map[string]any{
			"enabled":     true,
			"server":      "time.apple.com",
			"server_port": 123,
			"interval":    "30m",
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}
