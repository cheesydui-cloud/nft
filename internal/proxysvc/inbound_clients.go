package proxysvc

import (
	"encoding/json"
	"strings"
)

// InboundClient is one live inbound identity overlaid onto a published template.
type InboundClient struct {
	UUID     string
	Username string
	Password string
}

// TemplateSecret extracts the admin-published inbound secret from config_json.
func TemplateSecret(protocol string, raw json.RawMessage) (uuid, username, password string) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vless":
		var c VLESSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return strings.TrimSpace(c.UUID), "", ""
		}
	case "shadowsocks", "ss":
		var c SSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return "", "", strings.TrimSpace(c.Password)
		}
	case "mieru":
		var c MieruConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return "", strings.TrimSpace(c.Username), strings.TrimSpace(c.Password)
		}
	case "socks5", "socks":
		var c Socks5Config
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return "", strings.TrimSpace(c.Username), strings.TrimSpace(c.Password)
		}
	case "anytls":
		var c AnyTLSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return "", strings.TrimSpace(c.Username), strings.TrimSpace(c.Password)
		}
	case "naive", "naiveproxy":
		var c NaiveConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil {
			return "", strings.TrimSpace(c.Username), strings.TrimSpace(c.Password)
		}
	}
	return "", "", ""
}

// MintUserSecret generates a new inbound identity for protocol.
func MintUserSecret(protocol string, raw json.RawMessage) (uuid, username, password string) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	username = "u" + randomHex(4)
	switch protocol {
	case "vless":
		return newUUID(), "", ""
	case "shadowsocks", "ss":
		method := "2022-blake3-aes-128-gcm"
		var c SSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err == nil && strings.TrimSpace(c.Method) != "" {
			method = NormalizeSSMethod(c.Method)
		}
		return "", "", GenerateSSPassword(method)
	case "anytls":
		return "", username, randomB64Std(16)
	default:
		return "", username, randomHex(12)
	}
}

// ConfigWithUserSecret copies the template and replaces the inbound secret
// so BuildShareURI emits that user's link.
func ConfigWithUserSecret(protocol string, raw json.RawMessage, cl InboundClient) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vless":
		if id := strings.TrimSpace(cl.UUID); id != "" {
			m["uuid"] = id
		}
	case "shadowsocks", "ss":
		if pass := ssClientPassword(raw, cl.Password); pass != "" {
			m["password"] = pass
		}
	case "mieru", "socks5", "socks", "anytls", "naive", "naiveproxy":
		if u := strings.TrimSpace(cl.Username); u != "" {
			m["username"] = u
		}
		if p := strings.TrimSpace(cl.Password); p != "" {
			m["password"] = p
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// OverlayInboundClients writes the live client list into config_json so core
// builders / mita deploy only accept those identities. Empty list = nobody.
func OverlayInboundClients(protocol string, raw json.RawMessage, clients []InboundClient) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vless":
		arr := make([]any, 0, len(clients))
		first := ""
		for _, cl := range clients {
			id := strings.TrimSpace(cl.UUID)
			if id == "" {
				continue
			}
			if first == "" {
				first = id
			}
			arr = append(arr, map[string]any{"id": id})
		}
		m["clients"] = arr
		if first != "" {
			m["uuid"] = first
		} else {
			// Keep a dummy uuid for validate/EnsureSecrets; Clients=[] wins in the builder.
			if _, ok := m["uuid"].(string); !ok || strings.TrimSpace(m["uuid"].(string)) == "" {
				m["uuid"] = "00000000-0000-4000-8000-000000000000"
			}
		}
	case "shadowsocks", "ss":
		arr := make([]any, 0, len(clients))
		for i, cl := range clients {
			pass := strings.TrimSpace(cl.Password)
			if pass == "" {
				continue
			}
			name := strings.TrimSpace(cl.Username)
			if name == "" {
				name = "u" + randomHex(3)
			}
			arr = append(arr, map[string]any{"name": name, "password": pass})
			_ = i
		}
		m["users"] = arr
	case "mieru":
		arr := make([]any, 0, len(clients))
		firstUser, firstPass := "", ""
		for _, cl := range clients {
			u, p := strings.TrimSpace(cl.Username), strings.TrimSpace(cl.Password)
			if u == "" || p == "" {
				continue
			}
			if firstUser == "" {
				firstUser, firstPass = u, p
			}
			arr = append(arr, map[string]any{"name": u, "password": p})
		}
		m["users"] = arr
		if firstUser != "" {
			m["username"] = firstUser
			m["password"] = firstPass
		} else {
			// Keep template fields so ValidateMieruDeploy still passes;
			// deployMieru prefers users when present.
		}
	case "socks5", "socks":
		arr := make([]any, 0, len(clients))
		firstUser, firstPass := "", ""
		for _, cl := range clients {
			u, p := strings.TrimSpace(cl.Username), strings.TrimSpace(cl.Password)
			if u == "" || p == "" {
				continue
			}
			if firstUser == "" {
				firstUser, firstPass = u, p
			}
			arr = append(arr, map[string]any{"username": u, "password": p})
		}
		m["users"] = arr
		if firstUser != "" {
			m["username"] = firstUser
			m["password"] = firstPass
			m["auth_mode"] = "password"
		}
	case "anytls":
		arr := make([]any, 0, len(clients))
		firstUser, firstPass := "default", ""
		for _, cl := range clients {
			u := strings.TrimSpace(cl.Username)
			if u == "" {
				u = "default"
			}
			p := strings.TrimSpace(cl.Password)
			if p == "" {
				continue
			}
			if firstPass == "" {
				firstUser, firstPass = u, p
			}
			arr = append(arr, map[string]any{"name": u, "password": p})
		}
		m["users"] = arr
		if firstPass != "" {
			m["username"] = firstUser
			m["password"] = firstPass
		}
	case "naive", "naiveproxy":
		arr := make([]any, 0, len(clients))
		firstUser, firstPass := "", ""
		for _, cl := range clients {
			u, p := strings.TrimSpace(cl.Username), strings.TrimSpace(cl.Password)
			if u == "" || p == "" {
				continue
			}
			if firstUser == "" {
				firstUser, firstPass = u, p
			}
			arr = append(arr, map[string]any{"username": u, "password": p})
		}
		m["users"] = arr
		if firstUser != "" {
			m["username"] = firstUser
			m["password"] = firstPass
		}
	}
	return json.Marshal(m)
}

func clientsToAny(clients []map[string]any) []any {
	out := make([]any, 0, len(clients))
	for _, c := range clients {
		out = append(out, c)
	}
	return out
}

func ssClientPassword(raw json.RawMessage, userPass string) string {
	userPass = strings.TrimSpace(userPass)
	if userPass == "" {
		return ""
	}
	var c SSConfig
	if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
		return userPass
	}
	method := NormalizeSSMethod(c.Method)
	psk := strings.TrimSpace(c.Password)
	if strings.HasPrefix(method, "2022-") && psk != "" && userPass != psk {
		return psk + ":" + userPass
	}
	return userPass
}

func vlessClientsFromConfig(c *VLESSConfig) []map[string]any {
	// Non-nil Clients (including empty) means overlay decided the live set.
	// Do not stamp c.Flow here — Vision is only legal on tcp+(reality|tls)
	// and the builder applies / strips it after ValidateVLESSDeploy.
	if c.Clients != nil {
		out := make([]map[string]any, 0, len(c.Clients))
		for _, cl := range c.Clients {
			id := strings.TrimSpace(cl.ID)
			if id == "" {
				continue
			}
			item := map[string]any{"id": id}
			if f := strings.TrimSpace(cl.Flow); f != "" && f != "none" && f != "off" && f != "关" && f != "-" {
				item["flow"] = f
			}
			out = append(out, item)
		}
		return out
	}
	if strings.TrimSpace(c.UUID) == "" {
		return nil
	}
	return []map[string]any{{"id": c.UUID}}
}
