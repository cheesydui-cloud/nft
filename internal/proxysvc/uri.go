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

// VLESSConfig is the subset of fields we persist for VLESS publish.
// Security: reality (default) | tls | none. Transports are filtered by security matrix.
type VLESSConfig struct {
	ListenPort        int    `json:"listen_port"`
	ShareHost         string `json:"share_host"`
	ServerName        string `json:"server_name"` // REALITY dest SNI | TLS SNI / cert domain
	ServerPort        int    `json:"server_port"` // REALITY dest port (default 443)
	Fingerprint       string `json:"fingerprint"`
	Network           string `json:"network"` // tcp | ws | grpc | httpupgrade | xhttp
	Flow              string `json:"flow"`    // xtls-rprx-vision | none | ""
	MaxTimeDifference int    `json:"max_time_difference"`
	Security          string `json:"security"` // reality | tls | none
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
	// Sniffing enables inbound traffic sniffing (http/tls/quic destOverride).
	// nil = default on (matches historical hard-coded behavior); false disables.
	Sniffing *bool `json:"sniffing,omitempty"`
	// TcpFastOpen writes streamSettings.sockopt.tcpFastOpen. Off by default —
	// needs kernel support; leave false unless you know the node enables TFO.
	TcpFastOpen bool `json:"tcp_fast_open,omitempty"`
	// Transport extras (ws / httpupgrade / xhttp / grpc)
	Path        string `json:"path"`
	Host        string `json:"host"`         // Host header / authority
	SpiderX     string `json:"spider_x"`
	XHTTPMode   string `json:"xhttp_mode"`   // auto | packet-up | stream-up | stream-one
	ServiceName string `json:"service_name"` // gRPC service name
	// TLS fields (security=tls)
	ALPN          string `json:"alpn"`           // e.g. "h2,http/1.1"
	AllowInsecure bool   `json:"allow_insecure"` // client skip verify (URI only)
	CertPEM       string `json:"cert_pem"`       // server certificate PEM
	KeyPEM        string `json:"key_pem"`        // server private key PEM
	// Deploy-only: absolute paths written by agent before BuildXrayVLESSConfig.
	// Not set by panel; never required in config_json from API.
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

// NetworksForSecurity returns allowed transport networks for a security mode.
func NetworksForSecurity(security string) []string {
	switch NormalizeSecurity(security) {
	case "reality":
		return []string{"tcp", "xhttp", "grpc"}
	case "tls", "none":
		return []string{"tcp", "ws", "grpc", "xhttp", "httpupgrade"}
	default:
		return []string{"tcp"}
	}
}

// NormalizeSecurity maps empty / aliases to canonical security values.
func NormalizeSecurity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "reality":
		return "reality"
	case "tls", "xtls":
		return "tls"
	case "none", "plain", "raw":
		return "none"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

// NormalizeNetwork maps aliases (websocket → ws) and lowercases.
func NormalizeNetwork(n string) string {
	n = strings.ToLower(strings.TrimSpace(n))
	switch n {
	case "", "raw":
		return "tcp"
	case "websocket":
		return "ws"
	case "http_upgrade", "http-upgrade":
		return "httpupgrade"
	case "splithttp":
		return "xhttp"
	case "gun":
		return "grpc"
	default:
		return n
	}
}

// NetworkAllowed reports whether network is valid under security.
func NetworkAllowed(security, network string) bool {
	sec := NormalizeSecurity(security)
	netw := NormalizeNetwork(network)
	for _, n := range NetworksForSecurity(sec) {
		if n == netw {
			return true
		}
	}
	return false
}

// VisionAllowed reports whether xtls-rprx-vision may be used.
func VisionAllowed(security, network string) bool {
	sec := NormalizeSecurity(security)
	netw := NormalizeNetwork(network)
	return netw == "tcp" && (sec == "reality" || sec == "tls")
}

// ValidateVLESSDeploy checks fields required to deploy an inbound (not just URI).
func ValidateVLESSDeploy(c *VLESSConfig) error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	sec := NormalizeSecurity(c.Security)
	netw := NormalizeNetwork(c.Network)
	if !NetworkAllowed(sec, netw) {
		return fmt.Errorf("安全层 %s 不支持传输 %q（允许: %s）", sec, netw, strings.Join(NetworksForSecurity(sec), "/"))
	}
	switch sec {
	case "reality":
		if strings.TrimSpace(c.ServerName) == "" {
			return fmt.Errorf("server_name（REALITY SNI / dest）必填")
		}
		if strings.TrimSpace(c.PrivateKey) == "" {
			return fmt.Errorf("reality private_key missing")
		}
	case "tls":
		if strings.TrimSpace(c.ServerName) == "" {
			return fmt.Errorf("server_name（TLS SNI / 证书域名）必填")
		}
		hasFiles := strings.TrimSpace(c.CertFile) != "" && strings.TrimSpace(c.KeyFile) != ""
		hasPEM := strings.TrimSpace(c.CertPEM) != "" && strings.TrimSpace(c.KeyPEM) != ""
		if !hasFiles && !hasPEM {
			return fmt.Errorf("TLS 需要证书与私钥（cert_pem / key_pem）")
		}
		// When PEM is present (panel publish path), fully validate parse/key/SAN/expiry.
		// Agent path only has files — skip PEM parse here (files already written).
		if hasPEM {
			if err := ValidateTLSCertPair(c.CertPEM, c.KeyPEM, c.ServerName); err != nil {
				return err
			}
		}
	case "none":
		// no cert / reality keys required
	default:
		return fmt.Errorf("不支持的安全层 %q（reality / tls / none）", c.Security)
	}
	return nil
}

// SSConfig is Shadowsocks / SS2022 (sing-box) config.
	// Aligned with common production sing-box SS deploys (e.g. yyds install script):
	// dual-stack listen, optional NTP, multiplex / TFO / sniff.
	type SSConfig struct {
		ListenPort int    `json:"listen_port"`
		ShareHost  string `json:"share_host"`
		Method     string `json:"method"`
		Password   string `json:"password"`
		// Listen address for sing-box inbound. Empty → "::" (dual-stack, yyds-style).
		// Use "0.0.0.0" for IPv4-only when the host has no IPv6.
		Listen string `json:"listen,omitempty"`
		// NTP keeps server clock accurate (LE/SS2022 less sensitive but good practice).
		// nil / omitted → enabled by default in BuildSingBoxSSConfig.
		NTP *bool `json:"ntp,omitempty"`
		// Multiplex enables sing-box inbound multiplex (smux).
		Multiplex bool `json:"multiplex,omitempty"`
		// TCPFastOpen sets sockopt tcp_fast_open when supported.
		TCPFastOpen bool `json:"tcp_fast_open,omitempty"`
		// Sniffing enables inbound sniff (default true when nil).
		Sniffing *bool `json:"sniffing,omitempty"`
		SubVisible bool `json:"sub_visible"`
	}

	// SSMethods lists supported ciphers for Wizard / validation.
	// Prefer SS2022 (blake3); legacy AEAD kept for older clients.
	var SSMethods = []string{
		"2022-blake3-aes-128-gcm",
		"2022-blake3-aes-256-gcm",
		"2022-blake3-chacha20-poly1305",
		"aes-128-gcm",
		"aes-256-gcm",
		"chacha20-ietf-poly1305",
	}

	// NormalizeSSMethod returns a known method or the default SS2022-128.
	func NormalizeSSMethod(m string) string {
		m = strings.TrimSpace(strings.ToLower(m))
		if m == "" {
			return "2022-blake3-aes-128-gcm"
		}
		for _, known := range SSMethods {
			if m == known {
				return known
			}
		}
		// Accept any non-empty method sing-box may support; wizard only offers SSMethods.
		return m
	}

	// SSPasswordBytes returns the recommended random key length in bytes for method.
	// SS2022-128 → 16; SS2022-256 / chacha20 → 32; legacy AEAD → 16.
	func SSPasswordBytes(method string) int {
		m := strings.ToLower(strings.TrimSpace(method))
		switch {
		case strings.Contains(m, "256"), strings.Contains(m, "chacha20"):
			return 32
		default:
			return 16
		}
	}

	// GenerateSSPassword returns a std base64 random key sized for method.
	func GenerateSSPassword(method string) string {
		return randomB64Std(SSPasswordBytes(method))
	}

	// ValidateSSDeploy checks method/password before publish.
	func ValidateSSDeploy(c *SSConfig) error {
		if c == nil {
			return fmt.Errorf("ss config nil")
		}
		method := NormalizeSSMethod(c.Method)
		if c.Password == "" {
			return fmt.Errorf("Shadowsocks 密码未配置")
		}
		// Soft check: SS2022 keys should be valid base64 of expected length.
		if strings.HasPrefix(method, "2022-") {
			raw, err := base64.StdEncoding.DecodeString(c.Password)
			if err != nil {
				// try raw URL encoding without padding
				raw, err = base64.RawStdEncoding.DecodeString(c.Password)
			}
			if err != nil {
				return fmt.Errorf("SS2022 密码须为 base64 密钥材料: %w", err)
			}
			want := SSPasswordBytes(method)
			if len(raw) != want {
				return fmt.Errorf("SS2022 method %s 需要 %d 字节密钥（当前 %d 字节 base64 解码后）", method, want, len(raw))
			}
		}
		return nil
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
			c.Security = NormalizeSecurity(c.Security)
			c.Network = NormalizeNetwork(c.Network)
			if !NetworkAllowed(c.Security, c.Network) {
				// Soft-correct illegal combo (e.g. old ws+reality) back to tcp.
				c.Network = "tcp"
			}
			// flow: "none" / "off" / "关" = explicitly disabled; empty = default vision when allowed
			switch strings.ToLower(strings.TrimSpace(c.Flow)) {
			case "none", "off", "关", "-":
				c.Flow = ""
			case "":
				if VisionAllowed(c.Security, c.Network) {
					c.Flow = "xtls-rprx-vision"
				}
			default:
				if !VisionAllowed(c.Security, c.Network) {
					c.Flow = ""
				}
			}
			switch c.Security {
			case "reality":
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
			case "tls":
				// TLS uses cert_pem/key_pem; do not auto-generate REALITY keys.
			case "none":
				// plain: no keys required
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
			if c.ServiceName == "" && c.Network == "grpc" {
				c.ServiceName = "GunService"
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
			c.Method = NormalizeSSMethod(c.Method)
			if c.Password == "" {
				c.Password = GenerateSSPassword(c.Method)
			}
			// Default dual-stack listen (yyds / production sing-box SS).
			if strings.TrimSpace(c.Listen) == "" {
				c.Listen = "::"
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
			sec := NormalizeSecurity(c.Security)
			q.Set("security", sec)
			network := NormalizeNetwork(c.Network)
			q.Set("type", network)
			if c.Fingerprint != "" && (sec == "reality" || sec == "tls") {
				q.Set("fp", c.Fingerprint)
			}
			if sec == "reality" || sec == "tls" {
				if c.ServerName != "" {
					q.Set("sni", c.ServerName)
				}
			}
			if sec == "tls" {
				if alpn := strings.TrimSpace(c.ALPN); alpn != "" {
					q.Set("alpn", alpn)
				}
				if c.AllowInsecure {
					q.Set("allowInsecure", "1")
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
			case "grpc":
				svcName := strings.TrimSpace(c.ServiceName)
				if svcName == "" {
					svcName = "GunService"
				}
				q.Set("serviceName", svcName)
				// Some clients also read path as service name
				if c.Path != "" && c.Path != "/" {
					q.Set("path", c.Path)
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

// NeedsVLESSEnc reports whether the VLESS config enables VLESS Encryption
// (server settings.decryption is not empty/none). Used by deploy to force a
// modern xray core — old system xray accepts the JSON but times out clients.
func NeedsVLESSEnc(raw json.RawMessage) bool {
	var c VLESSConfig
	if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
		return false
	}
	dec := normalizeVLESSEncToken(c.Decryption)
	if dec == "" || strings.EqualFold(dec, "none") {
		// Also treat client-only encryption as needing the feature (mis-paste).
		enc := normalizeVLESSEncToken(c.Encryption)
		return enc != "" && !strings.EqualFold(enc, "none")
	}
	return true
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

// GenerateVlessEncX25519 builds a matched VLESS Encryption pair using X25519,
// identical to the first block of `xray vlessenc` (not the ML-KEM-768 PQ block):
//
//	decryption = mlkem768x25519plus.native.600s.<private>
//	encryption = mlkem768x25519plus.native.0rtt.<public>
//
// This is what Weir and most clients use. Generating natively avoids parsing
// xray's dual-block stdout (which previously preferred the multi-KB PQ keys).
func GenerateVlessEncX25519() (encryption, decryption string) {
	priv, pub := GenerateRealityKeyPair()
	decryption = "mlkem768x25519plus.native.600s." + priv
	encryption = "mlkem768x25519plus.native.0rtt." + pub
	return encryption, decryption
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
