package subexport

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// URIToShadowrocketLine converts a share URI to a Shadowrocket [Proxy] line.
// Format examples:
//
//	Name = ss, host, port, method, "password"
//	Name = vless, host, port, "uuid", tls=true, ...
func URIToShadowrocketLine(uri, forceName string) string {
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
		return ssToSR(rest, forceName)
	case "vless":
		return vlessToSR(rest, forceName)
	default:
		return ""
	}
}

func ssToSR(rest, forceName string) string {
	name := forceName
	if i := strings.Index(rest, "#"); i >= 0 {
		if name == "" {
			name, _ = url.QueryUnescape(rest[i+1:])
		}
		rest = rest[:i]
	}
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
		return ""
	}
	if name == "" {
		name = host
	}
	// Shadowrocket: Name = ss, server, port, encrypt-method, password
	return fmt.Sprintf("%s = ss, %s, %d, encrypt-method=%s, password=%s",
		escapeSRName(name), host, port, method, password)
}

func vlessToSR(rest, forceName string) string {
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
	// Common SR vless line (extensions vary by SR version)
	parts := []string{
		fmt.Sprintf("%s = vless, %s, %d, %s", escapeSRName(name), host, port, uuid),
	}
	if sec := params["security"]; sec == "reality" || sec == "tls" {
		parts = append(parts, "tls=true")
	}
	if sni := params["sni"]; sni != "" {
		parts = append(parts, "peer="+sni)
	}
	if pbk := params["pbk"]; pbk != "" {
		parts = append(parts, "public-key="+pbk)
	}
	if sid := params["sid"]; sid != "" {
		parts = append(parts, "short-id="+sid)
	}
	if fp := params["fp"]; fp != "" {
		parts = append(parts, "client-fingerprint="+fp)
	}
	if flow := params["flow"]; flow != "" {
		parts = append(parts, "flow="+flow)
	}
	netw := params["type"]
	if netw == "" {
		netw = "tcp"
	}
	if netw != "tcp" {
		parts = append(parts, "obfs="+netw)
		if path := params["path"]; path != "" {
			parts = append(parts, "obfs-path="+path)
		}
		if h := params["host"]; h != "" {
			parts = append(parts, "obfs-host="+h)
		}
	}
	return strings.Join(parts, ", ")
}

func escapeSRName(name string) string {
	// Keep simple; strip commas that break conf
	name = strings.ReplaceAll(name, ",", " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return "proxy"
	}
	return name
}
