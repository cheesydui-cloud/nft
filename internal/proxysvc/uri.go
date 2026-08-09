// Package proxysvc builds share URIs and validates protocol configs for
// panel-published proxy services (VLESS / Shadowsocks / mieru).
package proxysvc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// VLESSConfig is the subset of Weir/3X-UI fields we persist for phase 1.
type VLESSConfig struct {
	ListenPort        int    `json:"listen_port"`
	ShareHost         string `json:"share_host"`
	ServerName        string `json:"server_name"`
	ServerPort        int    `json:"server_port"`
	Fingerprint       string `json:"fingerprint"`
	Network           string `json:"network"`
	Flow              string `json:"flow"`
	MaxTimeDifference int    `json:"max_time_difference"`
	Security          string `json:"security"`
	PrivateKey        string `json:"private_key"`
	PublicKey         string `json:"public_key"`
	ShortID           string `json:"short_id"`
	Encryption        string `json:"encryption"`
	Decryption        string `json:"decryption"`
	UUID              string `json:"uuid"`
	SubVisible        bool   `json:"sub_visible"`
}

// SSConfig is Shadowsocks 2022 (sing-box) config.
type SSConfig struct {
	ListenPort int    `json:"listen_port"`
	ShareHost  string `json:"share_host"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	SubVisible bool   `json:"sub_visible"`
}

// MieruConfig matches the Weir mieru form.
type MieruConfig struct {
	ListenPort          int      `json:"listen_port"`
	ShareHost           string   `json:"share_host"`
	Transports          []string `json:"transports"`
	TrafficPattern      string   `json:"traffic_pattern"`
	UserHintIsMandatory bool     `json:"user_hint_is_mandatory"`
	Username            string   `json:"username"`
	Password            string   `json:"password"`
	SubVisible          bool     `json:"sub_visible"`
}

// EnsureSecrets fills auto-generated credentials when missing.
func EnsureSecrets(protocol string, raw json.RawMessage) (json.RawMessage, error) {
	switch strings.ToLower(protocol) {
	case "vless":
		var c VLESSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.UUID == "" {
			c.UUID = newUUID()
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 443
		}
		if c.ServerPort <= 0 {
			c.ServerPort = 443
		}
		if c.Fingerprint == "" {
			c.Fingerprint = "chrome"
		}
		if c.Network == "" {
			c.Network = "tcp"
		}
		if c.Flow == "" {
			c.Flow = "xtls-rprx-vision"
		}
		if c.Security == "" {
			c.Security = "reality"
		}
		if c.Security == "reality" {
			if c.PrivateKey == "" || c.PublicKey == "" {
				// Placeholder pair — real x25519 generation can replace later;
				// panel UI also has "生成密钥". Keep non-empty so deploy can proceed in dry-run.
				if c.PrivateKey == "" {
					c.PrivateKey = randomB64URL(32)
				}
				if c.PublicKey == "" {
					c.PublicKey = randomB64URL(32)
				}
			}
			if c.ShortID == "" {
				c.ShortID = randomHex(8)
			}
		}
		return json.Marshal(c)
	case "shadowsocks", "ss":
		var c SSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 443
		}
		if c.Method == "" {
			c.Method = "2022-blake3-aes-128-gcm"
		}
		if c.Password == "" {
			// SS2022 expects base64 key material; generate 16 or 32 bytes by method.
			n := 16
			if strings.Contains(c.Method, "256") {
				n = 32
			}
			c.Password = randomB64Std(n)
		}
		return json.Marshal(c)
	case "mieru":
		var c MieruConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 443
		}
		if len(c.Transports) == 0 {
			c.Transports = []string{"TCP", "UDP"}
		}
		if c.Username == "" {
			c.Username = "u" + randomHex(4)
		}
		if c.Password == "" {
			c.Password = randomHex(12)
		}
		return json.Marshal(c)
	default:
		return raw, fmt.Errorf("unknown protocol %s", protocol)
	}
}

func nonzeroJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// BuildShareURI constructs a client-importable URI for one instance.
func BuildShareURI(protocol, name, shareHost string, listenPort int, raw json.RawMessage) (string, error) {
	host := strings.TrimSpace(shareHost)
	if host == "" {
		return "", fmt.Errorf("share host empty")
	}
	if listenPort <= 0 {
		return "", fmt.Errorf("listen port invalid")
	}
	name = strings.TrimSpace(name)
	switch strings.ToLower(protocol) {
	case "vless":
		var c VLESSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		if c.UUID == "" {
			return "", fmt.Errorf("vless uuid missing")
		}
		q := url.Values{}
		q.Set("encryption", "none")
		if c.Flow != "" {
			q.Set("flow", c.Flow)
		}
		sec := c.Security
		if sec == "" {
			sec = "reality"
		}
		q.Set("security", sec)
		if c.Network != "" {
			q.Set("type", c.Network)
		}
		if c.Fingerprint != "" {
			q.Set("fp", c.Fingerprint)
		}
		if sec == "reality" {
			if c.ServerName != "" {
				q.Set("sni", c.ServerName)
			}
			if c.PublicKey != "" {
				q.Set("pbk", c.PublicKey)
			}
			if c.ShortID != "" {
				q.Set("sid", c.ShortID)
			}
		}
		if c.Encryption != "" {
			q.Set("encryption", c.Encryption) // may override none when vlessenc used
		}
		u := url.URL{
			Scheme:   "vless",
			User:     url.User(c.UUID),
			Host:     net.JoinHostPort(host, strconv.Itoa(listenPort)),
			RawQuery: q.Encode(),
			Fragment: name,
		}
		return u.String(), nil
	case "shadowsocks", "ss":
		var c SSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		if c.Method == "" || c.Password == "" {
			return "", fmt.Errorf("ss method/password missing")
		}
		// ss://base64(method:password)@host:port#name
		userinfo := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(c.Method + ":" + c.Password))
		// Many clients expect StdEncoding; use Std for broader compatibility.
		userinfo = base64.StdEncoding.EncodeToString([]byte(c.Method + ":" + c.Password))
		u := &url.URL{
			Scheme:   "ss",
			User:     url.User(userinfo),
			Host:     net.JoinHostPort(host, strconv.Itoa(listenPort)),
			Fragment: name,
		}
		return u.String(), nil
	case "mieru":
			var c MieruConfig
			if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
				return "", err
			}
			if c.Username == "" || c.Password == "" {
				return "", fmt.Errorf("mieru username/password missing")
			}
			// Official simple share link (client: mieru import config <URL>):
			//   mierus://user:pass@host?profile=NAME&port=P&protocol=TCP&port=P&protocol=UDP
			// See https://github.com/enfein/mieru docs (client-install.md).
			// profile is required once; port/protocol pair by position (multiples OK).
			profile := name
			if profile == "" {
				profile = "default"
			}
			transports := c.Transports
			if len(transports) == 0 {
				transports = []string{"TCP", "UDP"}
			}
			q := url.Values{}
			q.Set("profile", profile)
			for _, t := range transports {
				proto := strings.ToUpper(strings.TrimSpace(t))
				if proto == "" {
					continue
				}
				// Keep port/protocol counts equal so clients associate by position.
				q.Add("port", strconv.Itoa(listenPort))
				q.Add("protocol", proto)
			}
			if c.TrafficPattern != "" {
				// Official param is hyphenated; value is opaque base64 protobuf.
				q.Set("traffic-pattern", c.TrafficPattern)
			}
			u := url.URL{
				Scheme:   "mierus",
				User:     url.UserPassword(c.Username, c.Password),
				Host:     host, // simple form: host only; port is a query param
				RawQuery: q.Encode(),
			}
			return u.String(), nil
	default:
		return "", fmt.Errorf("unknown protocol %s", protocol)
	}
}

// ListenPortFromConfig extracts default listen port from config JSON.
func ListenPortFromConfig(raw json.RawMessage) int {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return 443
	}
	switch v := m["listen_port"].(type) {
	case float64:
		if int(v) > 0 {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return 443
}

// ShareHostFromConfig returns optional share_host override from config.
func ShareHostFromConfig(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return ""
	}
	if s, ok := m["share_host"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomB64URL(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomB64Std(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// GenerateRealityKeyPair returns placeholder private/public for UI "生成密钥".
// Real X25519 can replace this without API change.
func GenerateRealityKeyPair() (privateKey, publicKey string) {
	return randomB64URL(32), randomB64URL(32)
}

// GenerateShortID returns an 8-byte hex short_id.
func GenerateShortID() string {
	return randomHex(8)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
