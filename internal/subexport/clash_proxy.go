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
	case "ss":
		return ssToClash(rest, forceName)
	case "vless":
		return vlessToClash(rest, forceName)
	default:
		return ""
	}
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
