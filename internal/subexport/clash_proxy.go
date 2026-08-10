package subexport

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// URIToClashProxy converts a share URI to a Clash proxy YAML block
// (lines starting with "- name: ..."). Empty if unsupported.
func URIToClashProxy(uri, forceName string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return ""
	}
	switch strings.ToLower(scheme) {
	case "ss", "shadowsocks":
		return ssToClash(rest, forceName)
	case "vless":
		return vlessToClash(rest, forceName)
	case "mieru", "mierus":
		return mieruToClash(rest, forceName)
	default:
		return ""
	}
}

// mieruToClash maps mierus://user:pass@host?port=P&protocol=TCP to Mihomo type: mieru.
func mieruToClash(rest, forceName string) string {
	name := forceName
	if i := strings.Index(rest, "#"); i >= 0 {
		if name == "" {
			name, _ = url.QueryUnescape(rest[i+1:])
		}
		rest = rest[:i]
	}
	params := url.Values{}
	if i := strings.Index(rest, "?"); i >= 0 {
		params, _ = url.ParseQuery(rest[i+1:])
		rest = rest[:i]
	}
	var username, password string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo := rest[:at]
		if colon := strings.Index(userinfo, ":"); colon >= 0 {
			username, _ = url.QueryUnescape(userinfo[:colon])
			password, _ = url.QueryUnescape(userinfo[colon+1:])
		} else {
			username, _ = url.QueryUnescape(userinfo)
		}
		rest = rest[at+1:]
	}
	host := rest
	port := 0
	// authority may be host or host:port
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host = h
		if n, e := strconv.Atoi(p); e == nil {
			port = n
		}
	} else if strings.HasPrefix(rest, "[") {
		// bare IPv6 without port
		if end := strings.Index(rest, "]"); end > 0 {
			host = rest[1:end]
		}
	}
	if port <= 0 {
		for _, p := range params["port"] {
			if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
				port = n
				break
			}
		}
	}
	if host == "" || port <= 0 || username == "" || password == "" {
		return ""
	}
	transport := "TCP"
	protos := params["protocol"]
	hasTCP, hasUDP := false, false
	for _, p := range protos {
		u := strings.ToUpper(strings.TrimSpace(p))
		if u == "TCP" {
			hasTCP = true
		}
		if u == "UDP" {
			hasUDP = true
		}
	}
	if hasTCP {
		transport = "TCP"
	} else if hasUDP {
		transport = "UDP"
	} else if len(protos) > 0 {
		transport = strings.ToUpper(strings.TrimSpace(protos[0]))
	}
	if name == "" {
		if pr := params.Get("profile"); pr != "" {
			name = pr
		} else {
			name = host
		}
	}
	// Field order matches mieru-panel / Mihomo docs.
	var b strings.Builder
	fmt.Fprintf(&b, "- name: %s\n", strconv.Quote(name))
	b.WriteString("  type: mieru\n")
	fmt.Fprintf(&b, "  server: %s\n", strconv.Quote(host))
	fmt.Fprintf(&b, "  port: %d\n", port)
	fmt.Fprintf(&b, "  username: %s\n", strconv.Quote(username))
	fmt.Fprintf(&b, "  password: %s\n", strconv.Quote(password))
	fmt.Fprintf(&b, "  transport: %s\n", transport)
	b.WriteString("  multiplexing: MULTIPLEXING_OFF")
	if tp := params.Get("traffic-pattern"); tp != "" {
		fmt.Fprintf(&b, "\n  traffic-pattern: %s", strconv.Quote(tp))
	}
	return b.String()
}

func ssToClash(rest, forceName string) string {
	name := forceName
	if i := strings.Index(rest, "#"); i >= 0 {
		if name == "" {
			name, _ = url.QueryUnescape(rest[i+1:])
		}
		rest = rest[:i]
	}
	// strip query (plugins rare for 2022)
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}
	var method, password, host string
	var port int
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo := rest[:at]
		if dec, err := base64.StdEncoding.DecodeString(padB64(userinfo)); err == nil {
			userinfo = string(dec)
		} else if dec, err := base64.RawStdEncoding.DecodeString(userinfo); err == nil {
			userinfo = string(dec)
		}
		colon := strings.Index(userinfo, ":")
		if colon < 0 {
			return ""
		}
		method = userinfo[:colon]
		password = userinfo[colon+1:]
		h, p, err := splitHostPort(rest[at+1:])
		if err != nil {
			return ""
		}
		host, port = h, p
	} else {
		dec, err := base64.StdEncoding.DecodeString(padB64(rest))
		if err != nil {
			dec, err = base64.RawStdEncoding.DecodeString(rest)
		}
		if err != nil {
			return ""
		}
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return ""
		}
		colon := strings.Index(s[:at], ":")
		if colon < 0 {
			return ""
		}
		method = s[:colon]
		password = s[colon+1 : at]
		h, p, err := splitHostPort(s[at+1:])
		if err != nil {
			return ""
		}
		host, port = h, p
	}
	if name == "" {
		name = host
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- name: %s\n", strconv.Quote(name))
	b.WriteString("  type: ss\n")
	fmt.Fprintf(&b, "  server: %s\n", host)
	fmt.Fprintf(&b, "  port: %d\n", port)
	fmt.Fprintf(&b, "  cipher: %s\n", method)
	fmt.Fprintf(&b, "  password: %s\n", strconv.Quote(password))
	b.WriteString("  udp: true")
	return b.String()
}

func vlessToClash(rest, forceName string) string {
	name := forceName
	if i := strings.Index(rest, "#"); i >= 0 {
		if name == "" {
			name, _ = url.QueryUnescape(rest[i+1:])
		}
		rest = rest[:i]
	}
	params := map[string]string{}
	if i := strings.Index(rest, "?"); i >= 0 {
		q, _ := url.ParseQuery(rest[i+1:])
		for k, vs := range q {
			if len(vs) > 0 {
				params[k] = vs[0]
			}
		}
		rest = rest[:i]
	}
	at := strings.Index(rest, "@")
	if at < 0 {
		return ""
	}
	uuid := rest[:at]
	host, port, err := splitHostPort(rest[at+1:])
	if err != nil {
		return ""
	}
	if name == "" {
		name = host
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- name: %s\n", strconv.Quote(name))
	b.WriteString("  type: vless\n")
	fmt.Fprintf(&b, "  server: %s\n", host)
	fmt.Fprintf(&b, "  port: %d\n", port)
	fmt.Fprintf(&b, "  uuid: %s\n", uuid)
	if flow := params["flow"]; flow != "" {
		fmt.Fprintf(&b, "  flow: %s\n", flow)
	}
	sec := params["security"]
	if sec == "tls" || sec == "reality" {
		b.WriteString("  tls: true\n")
	}
	if sni := params["sni"]; sni != "" {
		fmt.Fprintf(&b, "  servername: %s\n", sni)
	}
	if fp := params["fp"]; fp != "" {
		fmt.Fprintf(&b, "  client-fingerprint: %s\n", fp)
	}
	if sec == "reality" {
		b.WriteString("  reality-opts:\n")
		if pbk := params["pbk"]; pbk != "" {
			fmt.Fprintf(&b, "    public-key: %s\n", pbk)
		}
		if sid := params["sid"]; sid != "" {
			fmt.Fprintf(&b, "    short-id: %s\n", sid)
		}
	}
	netw := params["type"]
	if netw == "" {
		netw = "tcp"
	}
	if netw != "tcp" {
		fmt.Fprintf(&b, "  network: %s\n", netw)
	}
	appendClashTransport(&b, netw, params)
	b.WriteString("  udp: true")
	return b.String()
}

func appendClashTransport(b *strings.Builder, netw string, params map[string]string) {
	path := params["path"]
	host := params["host"]
	switch netw {
	case "ws":
		b.WriteString("  ws-opts:\n")
		if path != "" {
			fmt.Fprintf(b, "    path: %s\n", strconv.Quote(path))
		}
		if host != "" {
			b.WriteString("    headers:\n")
			fmt.Fprintf(b, "      Host: %s\n", host)
		}
	case "httpupgrade":
		// Clash Meta may use ws-opts style; keep path/host if present
		b.WriteString("  ws-opts:\n")
		if path != "" {
			fmt.Fprintf(b, "    path: %s\n", strconv.Quote(path))
		}
		if host != "" {
			b.WriteString("    headers:\n")
			fmt.Fprintf(b, "      Host: %s\n", host)
		}
	case "grpc":
		if path != "" {
			b.WriteString("  grpc-opts:\n")
			fmt.Fprintf(b, "    grpc-service-name: %s\n", strconv.Quote(path))
		}
	case "xhttp", "splithttp", "h2":
		// Best-effort for Meta
		if path != "" || host != "" {
			b.WriteString("  smux:\n")
			b.WriteString("    enabled: false\n")
		}
	}
}

func splitHostPort(hp string) (host string, port int, err error) {
	hp = strings.TrimSpace(hp)
	// IPv6 in brackets
	h, p, err := net.SplitHostPort(hp)
	if err != nil {
		// bare host without port
		if !strings.Contains(hp, ":") {
			return hp, 443, nil
		}
		return "", 0, err
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		return "", 0, err
	}
	return h, port, nil
}

func padB64(s string) string {
	switch len(s) % 4 {
	case 2:
		return s + "=="
	case 3:
		return s + "="
	default:
		return s
	}
}
