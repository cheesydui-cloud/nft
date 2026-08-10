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

	"golang.org/x/crypto/curve25519"
)

// VLESSConfig is the subset of fields we persist for VLESS (+ REALITY) publish.
// Extra transport / PQ fields are optional; defaults stay tcp + REALITY + vision.
type VLESSConfig struct {
	ListenPort        int    `json:"listen_port"`
	ShareHost         string `json:"share_host"`
	ServerName        string `json:"server_name"`
	ServerPort        int    `json:"server_port"`
	Fingerprint       string `json:"fingerprint"`
	Network           string `json:"network"` // tcp | ws | httpupgrade | xhttp
	Flow              string `json:"flow"`    // xtls-rprx-vision | none | ""
	MaxTimeDifference int    `json:"max_time_difference"`
	Security          string `json:"security"` // reality (deploy) | tls | none (uri-only stubs)
	PrivateKey        string `json:"private_key"`
	PublicKey         string `json:"public_key"`
	ShortID           string `json:"short_id"`
	// AllowEmptyShortID when true appends "" to shortIds so clients without sid still connect.
	// Default false = only configured short_id (stricter).
	AllowEmptyShortID bool   `json:"allow_empty_short_id"`
	Encryption        string `json:"encryption"` // client URI (optional ML-KEM / vlessenc)
	Decryption        string `json:"decryption"` // server settings.decryption (optional)
	UUID              string `json:"uuid"`
	SubVisible        bool   `json:"sub_visible"`
	// Transport extras (ws / httpupgrade / xhttp)
	Path      string `json:"path"`
	Host      string `json:"host"` // Host header / authority
	SpiderX   string `json:"spider_x"`
	XHTTPMode string `json:"xhttp_mode"` // auto | packet-up | stream-up | stream-one
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
		c.Network = strings.ToLower(strings.TrimSpace(c.Network))
		// flow: "none" / "off" / "关" = explicitly disabled; empty = default vision for tcp+reality
		switch strings.ToLower(strings.TrimSpace(c.Flow)) {
		case "none", "off", "关", "-":
			c.Flow = ""
		case "":
			// Only auto-enable vision on classic REALITY+tcp (best anti-block default).
			if c.Network == "tcp" && (c.Security == "" || c.Security == "reality") {
				c.Flow = "xtls-rprx-vision"
			}
		}
		if c.Security == "" {
			c.Security = "reality"
		}
		if c.Security == "reality" {
			// Always keep private/public as a matched pair. Partial fill used to
			// generate only the missing side and break client handshakes.
			if c.PrivateKey == "" || c.PublicKey == "" {
				priv, pub := GenerateRealityKeyPair()
				c.PrivateKey = priv
				c.PublicKey = pub
			}
			if c.ShortID == "" {
				c.ShortID = randomHex(8)
			}
		}
			// xray vlessenc / paste often wraps material in quotes — strip so share URI
			// and server config stay in sync (quoted encryption breaks clients).
			// Also auto-swap if user/panel put client (0rtt) into decryption and server (600s) into encryption.
			c.Encryption = normalizeVLESSEncToken(c.Encryption)
			c.Decryption = normalizeVLESSEncToken(c.Decryption)
			c.Encryption, c.Decryption = alignVLESSEncPair(c.Encryption, c.Decryption)
			if c.Path == "" && (c.Network == "ws" || c.Network == "httpupgrade" || c.Network == "xhttp") {
			c.Path = "/"
		}
		if c.XHTTPMode == "" && c.Network == "xhttp" {
			c.XHTTPMode = "auto"
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
			// encryption: none unless ML-KEM / vlessenc material provided.
			// Align against decryption so a swapped pair still exports the client (0rtt) string.
			enc, _ := alignVLESSEncPair(c.Encryption, c.Decryption)
			enc = normalizeVLESSEncToken(enc)
			if enc != "" && !strings.EqualFold(enc, "none") {
				q.Set("encryption", enc)
			} else {
				q.Set("encryption", "none")
			}
		if c.Flow != "" {
			q.Set("flow", c.Flow)
		}
		sec := c.Security
		if sec == "" {
			sec = "reality"
		}
		q.Set("security", sec)
		network := c.Network
		if network == "" {
			network = "tcp"
		}
		q.Set("type", network)
		if c.Fingerprint != "" {
			q.Set("fp", c.Fingerprint)
		}
		if sec == "reality" || sec == "tls" {
			if c.ServerName != "" {
				q.Set("sni", c.ServerName)
			}
		}
		if sec == "reality" {
			if c.PublicKey != "" {
				q.Set("pbk", c.PublicKey)
			}
			if c.ShortID != "" {
				q.Set("sid", c.ShortID)
			}
			if c.SpiderX != "" {
				q.Set("spx", c.SpiderX)
			}
		}
		// Transport-specific client params
		switch network {
		case "ws", "httpupgrade", "xhttp", "http":
			if c.Path != "" {
				q.Set("path", c.Path)
			}
			if c.Host != "" {
				q.Set("host", c.Host)
			}
			if network == "xhttp" && c.XHTTPMode != "" {
				q.Set("mode", c.XHTTPMode)
			}
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
		userinfo := base64.StdEncoding.EncodeToString([]byte(c.Method + ":" + c.Password))
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
			q.Add("port", strconv.Itoa(listenPort))
			q.Add("protocol", proto)
		}
		if c.TrafficPattern != "" {
			q.Set("traffic-pattern", c.TrafficPattern)
		}
		u := url.URL{
			Scheme:   "mierus",
			User:     url.UserPassword(c.Username, c.Password),
			Host:     host,
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

// GenerateRealityKeyPair returns a REALITY X25519 key pair (raw base64url, no padding),
// matching xray-core `xray x25519` output format used by clients as pbk.
func GenerateRealityKeyPair() (privateKey, publicKey string) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		// Extremely rare; still return a valid clamped pair from zero-padded entropy.
		priv[0] = 1
	}
	// Clamp as in X25519.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	var pub [32]byte
	curve25519.ScalarBaseMult(&pub, &priv)
	return base64.RawURLEncoding.EncodeToString(priv[:]), base64.RawURLEncoding.EncodeToString(pub[:])
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
