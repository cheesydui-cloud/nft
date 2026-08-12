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

// parseSSShare supports SIP002 (ss://base64(method:pass)@host:port#name) and
// legacy whole-base64 (ss://base64(method:pass@host:port)#name).
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
	if q := strings.Index(rest, "?"); q >= 0 {
		rest = rest[:q]
	}
	var userinfo, hostport string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo = rest[:at]
		hostport = rest[at+1:]
		// SIP002: userinfo is base64(method:password)
		dec, ok := b64DecodeFlexible(userinfo)
		if !ok {
			// sometimes already method:password
			dec = []byte(userinfo)
		}
		mp := string(dec)
		colon := strings.Index(mp, ":")
		if colon < 0 {
			return "", "", "", 0, fmt.Errorf("ss userinfo missing method:password")
		}
		method = mp[:colon]
		password = mp[colon+1:]
	} else {
		dec, ok := b64DecodeFlexible(rest)
		if !ok {
			return "", "", "", 0, fmt.Errorf("ss legacy base64 decode failed")
		}
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return "", "", "", 0, fmt.Errorf("ss legacy missing @")
		}
		mp := s[:at]
		hostport = s[at+1:]
		colon := strings.Index(mp, ":")
		if colon < 0 {
			return "", "", "", 0, fmt.Errorf("ss legacy missing method:password")
		}
		method = mp[:colon]
		password = mp[colon+1:]
	}
	h, pStr, e := net.SplitHostPort(hostport)
	if e != nil {
		return "", "", "", 0, fmt.Errorf("ss host:port: %w", e)
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
