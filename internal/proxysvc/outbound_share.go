package proxysvc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// BuildXrayOutboundFromShareURI builds a single Xray outbound object (tag "sk5")
// from a client share link. Mirrors 3x-ui "proxy outbound" chaining:
// inbound protocol stays separate; this is the egress hop.
//
// Supported: ss://, shadowsocks://, vless://, socks5://, socks://, trojan://
func BuildXrayOutboundFromShareURI(shareURI, tag string) (map[string]any, error) {
	shareURI = strings.TrimSpace(shareURI)
	if shareURI == "" {
		return nil, fmt.Errorf("share uri empty")
	}
	if tag == "" {
		tag = "sk5"
	}
	scheme, rest, ok := strings.Cut(shareURI, "://")
	if !ok {
		return nil, fmt.Errorf("share uri missing scheme")
	}
	switch strings.ToLower(scheme) {
	case "ss", "shadowsocks":
		return buildXraySSOutbound(shareURI, tag)
	case "vless":
		return buildXrayVLESSOutbound(shareURI, tag)
	case "socks5", "socks":
		return buildXraySocksOutboundFromURI(shareURI, tag)
	case "trojan":
		return buildXrayTrojanOutbound(shareURI, tag)
	default:
		return nil, fmt.Errorf("出站协议暂不支持 %s（支持 ss / vless / socks5 / trojan）", scheme)
	}
	_ = rest
	return nil, fmt.Errorf("unreachable")
}

// IsProxyShareURI reports whether s looks like a proxy share link (not bare host:port).
func IsProxyShareURI(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	i := strings.Index(s, "://")
	if i <= 0 {
		return false
	}
	switch strings.ToLower(s[:i]) {
	case "ss", "shadowsocks", "vless", "vmess", "trojan", "socks", "socks5",
		"hysteria2", "hy2", "tuic", "anytls", "naive", "mieru", "mierus":
		return true
	default:
		return false
	}
}

func buildXraySocksOutboundFromURI(raw, tag string) (map[string]any, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return nil, fmt.Errorf("socks share missing host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("socks share port invalid")
	}
	server := map[string]any{"address": host, "port": port}
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		if user != "" {
			server["users"] = []any{map[string]any{"user": user, "pass": pass}}
		}
	}
	return map[string]any{
		"protocol": "socks",
		"tag":      tag,
		"settings": map[string]any{"servers": []any{server}},
	}, nil
}

func buildXraySSOutbound(raw, tag string) (map[string]any, error) {
	method, password, host, port, err := parseSSShare(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"protocol": "shadowsocks",
		"tag":      tag,
		"settings": map[string]any{
			"servers": []any{
				map[string]any{
					"address":  host,
					"port":     port,
					"method":   method,
					"password": password,
				},
			},
		},
	}, nil
}

// parseSSShare supports common ss:// share forms used by panels / Clash / SIP002:
//
//	ss://base64(method:pass)@host:port#name          (SIP002)
//	ss://method:pass@host:port#name                  (plain userinfo)
//	ss://method:url-encoded-pass@host:port           (percent-encoded)
//	ss://base64(method:pass@host:port)#name          (legacy whole-base64)
//	ss://base64(method:pass@host:port)/?plugin=...   (legacy + query)
//
// Many warehouse pastes URL-encode the colon (%3A) or the whole userinfo; we
// unescape before splitting so "ss userinfo missing method:password" is not
// raised on otherwise valid links.
func parseSSShare(uri string) (method, password, host string, port int, err error) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(strings.ToLower(uri), "ss://") && !strings.HasPrefix(strings.ToLower(uri), "shadowsocks://") {
		return "", "", "", 0, fmt.Errorf("not ss uri")
	}
	rest := uri
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if h := strings.Index(rest, "#"); h >= 0 {
		rest = rest[:h]
	}
	// plugin query is after host:port in SIP002; strip for host parse but keep path-less.
	query := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		query = rest[q+1:]
		rest = rest[:q]
	}
	_ = query // plugin not needed for xray basic ss outbound

	var userinfo, hostport string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo = rest[:at]
		hostport = rest[at+1:]
		// hostport may still carry a trailing "/" from some generators
		hostport = strings.TrimSuffix(hostport, "/")
		method, password, err = splitSSUserinfo(userinfo)
		if err != nil {
			return "", "", "", 0, err
		}
	} else {
		// Legacy: entire body is base64(method:pass@host:port)
		dec, ok := b64DecodeFlexible(rest)
		if !ok {
			// try unescape then decode
			if u, e := url.PathUnescape(rest); e == nil {
				dec, ok = b64DecodeFlexible(u)
			}
		}
		if !ok {
			return "", "", "", 0, fmt.Errorf("ss legacy base64 decode failed")
		}
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return "", "", "", 0, fmt.Errorf("ss legacy missing @")
		}
		method, password, err = splitSSUserinfo(s[:at])
		if err != nil {
			return "", "", "", 0, err
		}
		hostport = s[at+1:]
		if q := strings.Index(hostport, "?"); q >= 0 {
			hostport = hostport[:q]
		}
		hostport = strings.TrimSuffix(hostport, "/")
	}

	h, pStr, e := net.SplitHostPort(hostport)
	if e != nil {
		// IPv6 without brackets rare in ss shares; try last colon split
		if i := strings.LastIndex(hostport, ":"); i > 0 {
			h = hostport[:i]
			pStr = hostport[i+1:]
		} else {
			return "", "", "", 0, fmt.Errorf("ss host:port: %w", e)
		}
	}
	p, e := strconv.Atoi(pStr)
	if e != nil || p < 1 || p > 65535 || h == "" {
		return "", "", "", 0, fmt.Errorf("ss host:port invalid")
	}
	if method == "" || password == "" {
		return "", "", "", 0, fmt.Errorf("ss method/password empty")
	}
	return method, password, h, p, nil
}

// splitSSUserinfo extracts method + password from the authority userinfo part.
// Tries: percent-unescape → plain method:pass → base64(method:pass) → base64(method):pass.
func splitSSUserinfo(userinfo string) (method, password string, err error) {
	userinfo = strings.TrimSpace(userinfo)
	if userinfo == "" {
		return "", "", fmt.Errorf("ss userinfo empty")
	}
	// Reject already-redacted secrets from list API.
	if userinfo == "***" || strings.HasPrefix(userinfo, "***") {
		return "", "", fmt.Errorf("ss 分享链已被脱敏，请重新选择落地节点")
	}

	// Percent-decode first (aes-256-gcm%3Apass, or encoded base64 blobs).
	// Prefer PathUnescape; fall back to QueryUnescape only if still no colon.
	unescaped := userinfo
	if u, e := url.PathUnescape(userinfo); e == nil && u != "" {
		unescaped = u
	}
	if !strings.Contains(unescaped, ":") {
		if u, e := url.QueryUnescape(userinfo); e == nil && u != "" {
			unescaped = u
		}
	}

	// 1) Plain method:password (or method:pass:with:colons)
	if m, p, ok := splitMethodPass(unescaped); ok && !looksLikeBase64Only(m) {
		return m, p, nil
	}

	// 2) SIP002: entire userinfo is base64(method:password)
	if dec, ok := b64DecodeFlexible(unescaped); ok {
		mp := strings.Trim(string(dec), "\x00\r\n\t ")
		if m, p, ok := splitMethodPass(mp); ok {
			return m, p, nil
		}
	}
	// Try original (pre-unescape) base64 — some links encode '+' as %2B only in password half
	if unescaped != userinfo {
		if dec, ok := b64DecodeFlexible(userinfo); ok {
			mp := strings.Trim(string(dec), "\x00\r\n\t ")
			if m, p, ok := splitMethodPass(mp); ok {
				return m, p, nil
			}
		}
	}

	// 3) Non-standard: base64(method):password  or  method:base64(password)
	if m, p, ok := splitMethodPass(unescaped); ok {
		// method might itself be base64 of the real method name (rare)
		if dec, ok2 := b64DecodeFlexible(m); ok2 {
			dm := strings.TrimSpace(string(dec))
			if dm != "" && !strings.Contains(dm, ":") && len(dm) < 64 {
				return dm, p, nil
			}
		}
		// password might be base64 — keep as-is for xray (password is opaque)
		return m, p, nil
	}

	// 4) url.Parse("ss://"+userinfo+"@x:1") for edge userinfo forms
	if u, e := url.Parse("ss://" + userinfo + "@x:1"); e == nil && u.User != nil {
		name := u.User.Username()
		pass, hasPass := u.User.Password()
		if hasPass && name != "" {
			// method:password already split by url
			if dec, ok := b64DecodeFlexible(name); ok {
				if m, p, ok2 := splitMethodPass(string(dec) + ":" + pass); ok2 {
					return m, p, nil
				}
				// name was base64(method) only
				dm := strings.TrimSpace(string(dec))
				if dm != "" {
					return dm, pass, nil
				}
			}
			return name, pass, nil
		}
		if name != "" {
			if m, p, ok := splitMethodPass(name); ok {
				return m, p, nil
			}
			if dec, ok := b64DecodeFlexible(name); ok {
				if m, p, ok2 := splitMethodPass(strings.TrimSpace(string(dec))); ok2 {
					return m, p, nil
				}
			}
		}
	}

	preview := userinfo
	if len(preview) > 24 {
		preview = preview[:24] + "…"
	}
	return "", "", fmt.Errorf("ss userinfo missing method:password（userinfo=%q，请确认落地仓库 ss:// 含加密方式与密码）", preview)
}

// splitMethodPass splits "method:password" on the first colon.
func splitMethodPass(mp string) (method, password string, ok bool) {
	mp = strings.TrimSpace(mp)
	colon := strings.Index(mp, ":")
	if colon <= 0 || colon >= len(mp)-1 {
		return "", "", false
	}
	method = strings.TrimSpace(mp[:colon])
	password = mp[colon+1:] // keep password as-is (may start/end with spaces rarely)
	if method == "" || password == "" {
		return "", "", false
	}
	return method, password, true
}

// looksLikeBase64Only reports userinfo that is almost certainly base64 of
// method:pass (no raw colon before decode). Used to prefer base64 path.
// Known SS cipher names (aes-*-gcm, 2022-blake3-*, chacha20-*) must NOT match
// so plain "method:password" is preferred over a bogus base64 attempt.
func looksLikeBase64Only(s string) bool {
	if strings.Contains(s, ":") {
		return false
	}
	if len(s) < 4 {
		return false
	}
	// Cipher tokens look base64-ish (hyphens + alnum) but are not.
	low := strings.ToLower(s)
	if strings.Contains(low, "aes-") || strings.Contains(low, "chacha") ||
		strings.Contains(low, "blake3") || strings.Contains(low, "poly1305") ||
		strings.HasPrefix(low, "2022-") || strings.HasPrefix(low, "xchacha") ||
		low == "rc4-md5" || low == "none" || strings.Contains(low, "gcm") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '=' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

func b64DecodeFlexible(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	// url-safe without padding
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	// pad and retry std
	if m := len(s) % 4; m != 0 {
		s2 := s + strings.Repeat("=", 4-m)
		if b, err := base64.StdEncoding.DecodeString(s2); err == nil {
			return b, true
		}
		if b, err := base64.URLEncoding.DecodeString(s2); err == nil {
			return b, true
		}
	}
	return nil, false
}

func buildXrayVLESSOutbound(raw, tag string) (map[string]any, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	uuid := ""
	if u.User != nil {
		uuid = u.User.Username()
	}
	if uuid == "" {
		return nil, fmt.Errorf("vless uuid missing")
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return nil, fmt.Errorf("vless host:port missing")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("vless port invalid")
	}
	q := u.Query()
	encryption := q.Get("encryption")
	if encryption == "" {
		encryption = "none"
	}
	flow := q.Get("flow")
	// Xray client VLESS uses flat settings in some builds; standard is vnext.
	user := map[string]any{
		"id":         uuid,
		"encryption": encryption,
	}
	if flow != "" {
		user["flow"] = flow
	}
	ob := map[string]any{
		"protocol": "vless",
		"tag":      tag,
		"settings": map[string]any{
			"vnext": []any{
				map[string]any{
					"address": host,
					"port":    port,
					"users":   []any{user},
				},
			},
		},
	}
	stream := map[string]any{}
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	stream["network"] = network
	sec := q.Get("security")
	if sec == "" {
		sec = "none"
	}
	stream["security"] = sec
	switch network {
	case "ws":
		ws := map[string]any{"path": firstNonEmptyStr(q.Get("path"), "/")}
		if h := q.Get("host"); h != "" {
			ws["headers"] = map[string]any{"Host": h}
		}
		stream["wsSettings"] = ws
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": firstNonEmptyStr(q.Get("serviceName"), q.Get("path"), "GunService")}
	case "httpupgrade":
		hu := map[string]any{"path": firstNonEmptyStr(q.Get("path"), "/")}
		if h := q.Get("host"); h != "" {
			hu["host"] = h
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp":
		xh := map[string]any{"path": firstNonEmptyStr(q.Get("path"), "/"), "mode": firstNonEmptyStr(q.Get("mode"), "auto")}
		if h := q.Get("host"); h != "" {
			xh["host"] = h
		}
		stream["xhttpSettings"] = xh
	}
	switch sec {
	case "tls":
		tls := map[string]any{}
		if sni := q.Get("sni"); sni != "" {
			tls["serverName"] = sni
		}
		if fp := q.Get("fp"); fp != "" {
			tls["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			var parts []string
			for _, p := range strings.Split(alpn, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					parts = append(parts, p)
				}
			}
			if len(parts) > 0 {
				tls["alpn"] = parts
			}
		}
		if q.Get("allowInsecure") == "1" || q.Get("allowInsecure") == "true" {
			tls["allowInsecure"] = true
		}
		stream["tlsSettings"] = tls
	case "reality":
		reality := map[string]any{
			"fingerprint": firstNonEmptyStr(q.Get("fp"), "chrome"),
		}
		if pbk := q.Get("pbk"); pbk != "" {
			reality["publicKey"] = pbk
		}
		if sni := q.Get("sni"); sni != "" {
			reality["serverName"] = sni
		}
		if sid := q.Get("sid"); sid != "" {
			reality["shortId"] = sid
		}
		if spx := q.Get("spx"); spx != "" {
			reality["spiderX"] = spx
		}
		stream["realitySettings"] = reality
	}
	ob["streamSettings"] = stream
	return ob, nil
}

func buildXrayTrojanOutbound(raw, tag string) (map[string]any, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	pass := ""
	if u.User != nil {
		pass = u.User.Username()
	}
	if pass == "" {
		return nil, fmt.Errorf("trojan password missing")
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return nil, fmt.Errorf("trojan host:port missing")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("trojan port invalid")
	}
	q := u.Query()
	server := map[string]any{
		"address":  host,
		"port":     port,
		"password": pass,
	}
	ob := map[string]any{
		"protocol": "trojan",
		"tag":      tag,
		"settings": map[string]any{"servers": []any{server}},
	}
	stream := map[string]any{
		"network":  firstNonEmptyStr(q.Get("type"), "tcp"),
		"security": firstNonEmptyStr(q.Get("security"), "tls"),
	}
	if stream["security"] == "tls" {
		tls := map[string]any{}
		if sni := q.Get("sni"); sni != "" {
			tls["serverName"] = sni
		}
		if fp := q.Get("fp"); fp != "" {
			tls["fingerprint"] = fp
		}
		stream["tlsSettings"] = tls
	}
	ob["streamSettings"] = stream
	return ob, nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// BuildSingBoxOutboundFromShareURI builds a sing-box outbound object for ss/socks.
// VLESS/Trojan share → error (use xray entry core for those exits).
func BuildSingBoxOutboundFromShareURI(shareURI, tag string) (map[string]any, error) {
	shareURI = strings.TrimSpace(shareURI)
	if tag == "" {
		tag = "sk5-out"
	}
	scheme, _, ok := strings.Cut(shareURI, "://")
	if !ok {
		return nil, fmt.Errorf("share uri missing scheme")
	}
	switch strings.ToLower(scheme) {
	case "ss", "shadowsocks":
		method, password, host, port, err := parseSSShare(shareURI)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"type":        "shadowsocks",
			"tag":         tag,
			"server":      host,
			"server_port": port,
			"method":      method,
			"password":    password,
		}, nil
	case "socks5", "socks":
		u, err := url.Parse(shareURI)
		if err != nil {
			return nil, err
		}
		host := u.Hostname()
		portStr := u.Port()
		port, _ := strconv.Atoi(portStr)
		if host == "" || port < 1 {
			return nil, fmt.Errorf("socks host:port invalid")
		}
		ob := map[string]any{
			"type":        "socks",
			"tag":         tag,
			"server":      host,
			"server_port": port,
			"version":     "5",
		}
		if u.User != nil {
			ob["username"] = u.User.Username()
			pass, _ := u.User.Password()
			ob["password"] = pass
		}
		return ob, nil
	default:
		return nil, fmt.Errorf("sing-box 出站暂不支持 %s（入口请用 VLESS/xray，或出口用 ss/socks5）", scheme)
	}
}

// InjectSingBoxShareOutbound pins route.final to a share-derived outbound.
func InjectSingBoxShareOutbound(cfg []byte, shareURI string) ([]byte, error) {
	ob, err := BuildSingBoxOutboundFromShareURI(shareURI, "share-out")
	if err != nil {
		return nil, err
	}
	return injectSingBoxFinalOutbound(cfg, ob, "share-out")
}

// Must be valid JSON helper used by tests.
func outboundToJSON(ob map[string]any) string {
	b, _ := json.Marshal(ob)
	return string(b)
}
