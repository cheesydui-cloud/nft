// Package proxysvc builds share URIs and validates protocol configs for
// panel-published proxy services (VLESS / Shadowsocks / mieru / AnyTLS / Naive).
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
	// Clients is the live inbound UUID list. When set, builders emit every
	// entry instead of the single UUID (shared-secret → per-user isolation).
	Clients    []VLESSClient `json:"clients,omitempty"`
	SubVisible bool          `json:"sub_visible"`
	// Sniffing enables inbound traffic sniffing (http/tls/quic destOverride).
	// nil = default on (matches historical hard-coded behavior); false disables.
	Sniffing *bool `json:"sniffing,omitempty"`
	// TcpFastOpen writes streamSettings.sockopt.tcpFastOpen. Off by default —
	// needs kernel support; leave false unless you know the node enables TFO.
	TcpFastOpen bool `json:"tcp_fast_open,omitempty"`
	// Transport extras (ws / httpupgrade / xhttp / grpc)
	Path        string `json:"path"`
	Host        string `json:"host"` // Host header / authority
	SpiderX     string `json:"spider_x"`
	XHTTPMode   string `json:"xhttp_mode"`   // auto | packet-up | stream-up | stream-one
	ServiceName string `json:"service_name"` // gRPC service name
	// TLS fields (security=tls)
	ALPN          string `json:"alpn"`           // e.g. "h2,http/1.1"
	AllowInsecure bool   `json:"allow_insecure"` // client skip verify (URI only)
	CertPEM       string `json:"cert_pem"`       // server certificate PEM
	KeyPEM        string `json:"key_pem"`        // server private key PEM
	// CertID references tls_certificates vault; panel resolves to PEM before deploy.
	CertID int64 `json:"cert_id,omitempty"`
	// Deploy-only: absolute paths written by agent before BuildXrayVLESSConfig.
	// Not set by panel; never required in config_json from API.
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
}

// VLESSClient is one inbound UUID (xray clients[]).
type VLESSClient struct {
	ID   string `json:"id"`
	Flow string `json:"flow,omitempty"`
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

// DefaultSSListenPort matches the yyds sing-box installer range (10000–60000).
// 443 collides with nginx / exclusive inbound eviction on the same VPS.
const DefaultSSListenPort = 18388

// SSConfig is Shadowsocks / SS2022 (sing-box) config.
// Layout follows caigouzi121380/singbox-deploy (yyds): listen ::, SS2022,
// NTP, one direct outbound. Sniff / mux / TFO stay off unless asked.
type SSConfig struct {
	ListenPort int    `json:"listen_port"`
	ShareHost  string `json:"share_host"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	// Listen address for sing-box inbound. Empty → "::" (dual-stack, yyds-style).
	// Use "0.0.0.0" for IPv4-only when the host has no IPv6.
	Listen string `json:"listen,omitempty"`
	// NTP keeps server clock accurate. nil / omitted → enabled (yyds default).
	NTP *bool `json:"ntp,omitempty"`
	// Multiplex enables sing-box inbound multiplex (smux). Off in yyds.
	Multiplex bool `json:"multiplex,omitempty"`
	// TCPFastOpen sets sockopt tcp_fast_open when supported. Off in yyds.
	TCPFastOpen bool `json:"tcp_fast_open,omitempty"`
	// Sniffing: yyds inbound has none. nil / omitted → off.
	Sniffing   *bool `json:"sniffing,omitempty"`
	SubVisible bool  `json:"sub_visible"`
	// Users is the live inbound list (sing-box shadowsocks users[]).
	// Each password is a per-user key; SS2022 user keys are combined with
	// the inbound PSK at apply time.
	Users []SSUser `json:"users,omitempty"`
}

// SSUser is one Shadowsocks inbound user.
type SSUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
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

// DefaultMieruListenPort is in the official mita range (1025–65535).
// 443 is rejected by current mita apply ("ports must fall in 1025-65535").
const DefaultMieruListenPort = 8964

// Official mita / mierus:// defaults (enfein/mieru server-install + client-install).
const (
	DefaultMieruMTU           = 1400
	DefaultMieruHandshakeMode = "HANDSHAKE_NO_WAIT"
)

// MieruConfig matches official mita server JSON + simple mierus:// fields.
type MieruConfig struct {
	ListenPort          int      `json:"listen_port"`
	ShareHost           string   `json:"share_host"`
	Transports          []string `json:"transports"`
	TrafficPattern      string   `json:"traffic_pattern"`
	UserHintIsMandatory bool     `json:"user_hint_is_mandatory"`
	// MTU is written to the mita server config and advertised on mierus://.
	// Official default 1400; only used for UDP. 0 → 1400.
	MTU int `json:"mtu,omitempty"`
	// HandshakeMode is a client-share param (HANDSHAKE_STANDARD | HANDSHAKE_NO_WAIT).
	HandshakeMode string `json:"handshake_mode,omitempty"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	SubVisible    bool   `json:"sub_visible"`
	// Users is the live mita user list. When set, deploy prefers this over
	// the single username/password pair.
	Users []MieruUser `json:"users,omitempty"`
}

// MieruUser is one mita account.
type MieruUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// ValidateMieruDeploy checks fields required to publish mieru/mita.
func ValidateMieruDeploy(c *MieruConfig, listenPort int) error {
	if c == nil {
		return fmt.Errorf("mieru config nil")
	}
	port := listenPort
	if port <= 0 {
		port = c.ListenPort
	}
	if port < 1025 || port > 65535 {
		return fmt.Errorf("mieru 监听端口须为 1025–65535（当前 %d）。官方 mita 拒绝 443 等特权端口，请改成例如 %d 后重新发布", port, DefaultMieruListenPort)
	}
	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return fmt.Errorf("mieru 用户名/密码未配置")
	}
	return nil
}

// Socks5Config is sing-box socks inbound (standard SOCKS5 server).
// Share: socks5://user:pass@host:port#name  or socks5://host:port when auth_mode=none.
type Socks5Config struct {
	ListenPort int    `json:"listen_port"`
	ShareHost  string `json:"share_host"`
	Listen     string `json:"listen,omitempty"` // empty → "::"
	// AuthMode: "password" (default) | "none"
	AuthMode string `json:"auth_mode,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
	// UDP enables UDP ASSOCIATE. Default true when nil.
	UDP         *bool       `json:"udp,omitempty"`
	NTP         *bool       `json:"ntp,omitempty"`
	Sniffing    *bool       `json:"sniffing,omitempty"`
	TCPFastOpen bool        `json:"tcp_fast_open,omitempty"`
	SubVisible  bool        `json:"sub_visible"`
	Users       []SocksUser `json:"users,omitempty"`
}

// SocksUser is one SOCKS inbound account.
type SocksUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AnyTLSConfig is sing-box anytls inbound (TLS required; matches anytls-go + sing-box).
// Share: anytls://password@host:port?sni=...&fp=chrome#name
type AnyTLSConfig struct {
	ListenPort    int    `json:"listen_port"`
	ShareHost     string `json:"share_host"`
	Listen        string `json:"listen,omitempty"` // empty → "::"
	Username      string `json:"username"`         // user name field; default "default"
	Password      string `json:"password"`
	ServerName    string `json:"server_name"` // TLS SNI / cert domain
	Fingerprint   string `json:"fingerprint"` // client uTLS hint for share URI
	ALPN          string `json:"alpn,omitempty"`
	AllowInsecure bool   `json:"allow_insecure,omitempty"`
	CertPEM       string `json:"cert_pem,omitempty"`
	KeyPEM        string `json:"key_pem,omitempty"`
	CertID        int64  `json:"cert_id,omitempty"`
	CertFile      string `json:"cert_file,omitempty"`
	KeyFile       string `json:"key_file,omitempty"`
	// PaddingScheme optional; empty → sing-box default.
	PaddingScheme []string     `json:"padding_scheme,omitempty"`
	NTP           *bool        `json:"ntp,omitempty"`
	Sniffing      *bool        `json:"sniffing,omitempty"`
	TCPFastOpen   bool         `json:"tcp_fast_open,omitempty"`
	SubVisible    bool         `json:"sub_visible"`
	Users         []AnyTLSUser `json:"users,omitempty"`
}

// AnyTLSUser is one anytls inbound account.
type AnyTLSUser struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// NaiveConfig is sing-box naive inbound (protocol-compatible; not Caddy original stack).
// Share: naive+https://user:pass@host:port  or naive+quic when network=udp only.
type NaiveConfig struct {
	ListenPort int    `json:"listen_port"`
	ShareHost  string `json:"share_host"`
	Listen     string `json:"listen,omitempty"`
	// Network: "tcp", "udp", or "" for both (sing-box default).
	Network       string `json:"network,omitempty"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ServerName    string `json:"server_name"`
	ALPN          string `json:"alpn,omitempty"`
	AllowInsecure bool   `json:"allow_insecure,omitempty"`
	CertPEM       string `json:"cert_pem,omitempty"`
	KeyPEM        string `json:"key_pem,omitempty"`
	CertID        int64  `json:"cert_id,omitempty"`
	CertFile      string `json:"cert_file,omitempty"`
	KeyFile       string `json:"key_file,omitempty"`
	// QuicCongestionControl since sing-box 1.13; empty → default bbr.
	QuicCongestionControl string      `json:"quic_congestion_control,omitempty"`
	NTP                   *bool       `json:"ntp,omitempty"`
	Sniffing              *bool       `json:"sniffing,omitempty"`
	TCPFastOpen           bool        `json:"tcp_fast_open,omitempty"`
	SubVisible            bool        `json:"sub_visible"`
	Users                 []NaiveUser `json:"users,omitempty"`
}

// NaiveUser is one naive inbound account.
type NaiveUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ValidateSocks5Deploy checks fields required to publish SOCKS5.
func ValidateSocks5Deploy(c *Socks5Config) error {
	if c == nil {
		return fmt.Errorf("socks5 config nil")
	}
	mode := strings.ToLower(strings.TrimSpace(c.AuthMode))
	if mode == "" {
		mode = "password"
	}
	if mode != "password" && mode != "none" {
		return fmt.Errorf("auth_mode 须为 password 或 none")
	}
	if mode == "password" {
		if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
			return fmt.Errorf("SOCKS5 用户名/密码未配置")
		}
	}
	return nil
}

// ValidateAnyTLSDeploy checks fields required to publish AnyTLS.
func ValidateAnyTLSDeploy(c *AnyTLSConfig) error {
	if c == nil {
		return fmt.Errorf("anytls config nil")
	}
	if strings.TrimSpace(c.Password) == "" {
		return fmt.Errorf("AnyTLS 密码未配置")
	}
	if strings.TrimSpace(c.ServerName) == "" {
		return fmt.Errorf("server_name（TLS 域名 / SNI）必填")
	}
	cert := strings.TrimSpace(c.CertPEM)
	key := strings.TrimSpace(c.KeyPEM)
	// Allow agent-side cert_file path for deploy without PEM in JSON (re-publish).
	if cert == "" && strings.TrimSpace(c.CertFile) == "" {
		return fmt.Errorf("TLS 证书未配置（请申请 ACME 或粘贴 PEM）")
	}
	if key == "" && strings.TrimSpace(c.KeyFile) == "" {
		return fmt.Errorf("TLS 私钥未配置")
	}
	if cert != "" && key != "" {
		if err := ValidateTLSCertPair(cert, key, c.ServerName); err != nil {
			return err
		}
	}
	return nil
}

// ValidateNaiveDeploy checks fields required to publish Naive.
func ValidateNaiveDeploy(c *NaiveConfig) error {
	if c == nil {
		return fmt.Errorf("naive config nil")
	}
	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return fmt.Errorf("Naive 用户名/密码未配置")
	}
	if strings.TrimSpace(c.ServerName) == "" {
		return fmt.Errorf("server_name（TLS 域名 / SNI）必填")
	}
	cert := strings.TrimSpace(c.CertPEM)
	key := strings.TrimSpace(c.KeyPEM)
	if cert == "" && strings.TrimSpace(c.CertFile) == "" {
		return fmt.Errorf("TLS 证书未配置（请申请 ACME 或粘贴 PEM）")
	}
	if key == "" && strings.TrimSpace(c.KeyFile) == "" {
		return fmt.Errorf("TLS 私钥未配置")
	}
	if cert != "" && key != "" {
		if err := ValidateTLSCertPair(cert, key, c.ServerName); err != nil {
			return err
		}
	}
	n := strings.ToLower(strings.TrimSpace(c.Network))
	if n != "" && n != "tcp" && n != "udp" {
		return fmt.Errorf("network 须为 tcp / udp 或留空（双栈）")
	}
	return nil
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
			c.ListenPort = DefaultSSListenPort
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
			// Official mita rejects ports below 1025 (443 is invalid).
			c.ListenPort = DefaultMieruListenPort
		}
		if len(c.Transports) == 0 {
			// TCP-only default: UDP share makes Clash/some clients
			// sit on UDP, which dies behind NAT while panel TCP
			// probe still looks green. Operators can still tick UDP.
			c.Transports = []string{"TCP"}
		}
		if c.MTU <= 0 {
			c.MTU = DefaultMieruMTU
		}
		if strings.TrimSpace(c.HandshakeMode) == "" {
			c.HandshakeMode = DefaultMieruHandshakeMode
		}
		if c.Username == "" {
			c.Username = "u" + randomHex(4)
		}
		if c.Password == "" {
			c.Password = randomHex(12)
		}
		return json.Marshal(c)
	case "socks5", "socks":
		var c Socks5Config
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 1080
		}
		if strings.TrimSpace(c.Listen) == "" {
			c.Listen = "::"
		}
		mode := strings.ToLower(strings.TrimSpace(c.AuthMode))
		if mode == "" {
			mode = "password"
		}
		c.AuthMode = mode
		if mode == "password" {
			if strings.TrimSpace(c.Username) == "" {
				c.Username = "u" + randomHex(4)
			}
			if strings.TrimSpace(c.Password) == "" {
				c.Password = randomHex(12)
			}
		}
		return json.Marshal(c)
	case "anytls":
		var c AnyTLSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 443
		}
		if strings.TrimSpace(c.Listen) == "" {
			c.Listen = "::"
		}
		if strings.TrimSpace(c.Username) == "" {
			c.Username = "default"
		}
		if strings.TrimSpace(c.Password) == "" {
			c.Password = randomB64Std(16)
		}
		if strings.TrimSpace(c.Fingerprint) == "" {
			c.Fingerprint = "chrome"
		}
		return json.Marshal(c)
	case "naive", "naiveproxy":
		var c NaiveConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return nil, err
		}
		if c.ListenPort <= 0 {
			c.ListenPort = 443
		}
		if strings.TrimSpace(c.Listen) == "" {
			c.Listen = "::"
		}
		if strings.TrimSpace(c.Username) == "" {
			c.Username = "u" + randomHex(4)
		}
		if strings.TrimSpace(c.Password) == "" {
			c.Password = randomHex(12)
		}
		n := strings.ToLower(strings.TrimSpace(c.Network))
		if n != "" && n != "tcp" && n != "udp" {
			c.Network = "tcp"
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
		// SIP002: ss://base64url(method:password)@host:port#name
		// Must be RawURLEncoding (no padding) and must NOT go through
		// url.User — that percent-encodes '/' as %2F, and many clients
		// (Clash / Shadowrocket / sing-box) then fail to decode the
		// userinfo. SS2022 keys are std-base64 and frequently contain '/'.
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(c.Method + ":" + c.Password))
		out := "ss://" + userinfo + "@" + net.JoinHostPort(host, strconv.Itoa(listenPort))
		if name != "" {
			out += "#" + url.PathEscape(name)
		}
		return out, nil
	case "mieru":
		var c MieruConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		if c.Username == "" || c.Password == "" {
			return "", fmt.Errorf("mieru username/password missing")
		}
		if strings.ContainsAny(c.Username, ":@/?#") || strings.ContainsAny(c.Password, "@/?#") {
			return "", fmt.Errorf("mieru 用户名/密码含非法字符（:@/?#），请重新生成")
		}
		profile := name
		if profile == "" {
			profile = "default"
		}
		// Simple share is one transport. Shadowrocket / some iOS
		// clients take the last protocol= and show MIERU/UDP — then
		// time out against a TCP-only (or TCP-preferred) listen
		// while the panel TCP probe still looks green.
		transports := mieruShareTransports(c.Transports)
		// Official simple share pairs port/protocol by appearance order
		// (profile&port=P&protocol=TCP). url.Values.Encode sorts keys
		// and groups all port= before protocol=, which some clients
		// fail to pair. Build the query in official order.
		var q strings.Builder
		q.WriteString("profile=")
		q.WriteString(url.QueryEscape(profile))
		wroteProto := false
		for _, t := range transports {
			proto := strings.ToUpper(strings.TrimSpace(t))
			if proto == "" {
				continue
			}
			q.WriteString("&port=")
			q.WriteString(strconv.Itoa(listenPort))
			q.WriteString("&protocol=")
			q.WriteString(url.QueryEscape(proto))
			wroteProto = true
		}
		if !wroteProto {
			q.WriteString("&port=")
			q.WriteString(strconv.Itoa(listenPort))
			q.WriteString("&protocol=TCP")
		}
		mtu := c.MTU
		if mtu <= 0 {
			mtu = DefaultMieruMTU
		}
		if mtu < 1280 {
			mtu = 1280
		}
		q.WriteString("&mtu=")
		q.WriteString(strconv.Itoa(mtu))
		hs := strings.TrimSpace(c.HandshakeMode)
		if hs == "" {
			hs = DefaultMieruHandshakeMode
		}
		q.WriteString("&handshake-mode=")
		q.WriteString(url.QueryEscape(hs))
		if c.TrafficPattern != "" {
			q.WriteString("&traffic-pattern=")
			q.WriteString(url.QueryEscape(c.TrafficPattern))
		}
		u := url.URL{
			Scheme:   "mierus",
			User:     url.UserPassword(c.Username, c.Password),
			Host:     shareHostAuthority(host),
			RawQuery: q.String(),
		}
		return u.String(), nil
	case "socks5", "socks":
		var c Socks5Config
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		mode := strings.ToLower(strings.TrimSpace(c.AuthMode))
		if mode == "" {
			mode = "password"
		}
		u := url.URL{
			Scheme:   "socks5",
			Host:     net.JoinHostPort(host, strconv.Itoa(listenPort)),
			Fragment: name,
		}
		if mode == "password" {
			if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
				return "", fmt.Errorf("socks5 username/password missing")
			}
			u.User = url.UserPassword(c.Username, c.Password)
		}
		return u.String(), nil
	case "anytls":
		var c AnyTLSConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		if strings.TrimSpace(c.Password) == "" {
			return "", fmt.Errorf("anytls password missing")
		}
		q := url.Values{}
		if sn := strings.TrimSpace(c.ServerName); sn != "" {
			q.Set("sni", sn)
		}
		fp := strings.TrimSpace(c.Fingerprint)
		if fp == "" {
			fp = "chrome"
		}
		q.Set("fp", fp)
		if c.AllowInsecure {
			q.Set("insecure", "1")
		}
		if alpn := strings.TrimSpace(c.ALPN); alpn != "" {
			q.Set("alpn", alpn)
		}
		u := url.URL{
			Scheme:   "anytls",
			User:     url.User(c.Password),
			Host:     net.JoinHostPort(host, strconv.Itoa(listenPort)),
			RawQuery: q.Encode(),
			Fragment: name,
		}
		return u.String(), nil
	case "naive", "naiveproxy":
		var c NaiveConfig
		if err := json.Unmarshal(nonzeroJSON(raw), &c); err != nil {
			return "", err
		}
		if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
			return "", fmt.Errorf("naive username/password missing")
		}
		// udp-only → quic; otherwise https (tcp or both).
		scheme := "naive+https"
		if strings.EqualFold(strings.TrimSpace(c.Network), "udp") {
			scheme = "naive+quic"
		}
		u := url.URL{
			Scheme:   scheme,
			User:     url.UserPassword(c.Username, c.Password),
			Host:     net.JoinHostPort(host, strconv.Itoa(listenPort)),
			Fragment: name,
		}
		q := url.Values{}
		if sn := strings.TrimSpace(c.ServerName); sn != "" {
			q.Set("sni", sn)
		}
		if c.AllowInsecure {
			q.Set("insecure", "1")
		}
		if s := q.Encode(); s != "" {
			u.RawQuery = s
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

// mieruShareTransports is what the simple mierus:// link advertises.
// Server may still bind UDP when the operator ticked it; clients that
// can only use one protocol get TCP so NAT / CF / UDP-blocked paths work.
func mieruShareTransports(in []string) []string {
	var hasTCP, hasUDP bool
	for _, t := range in {
		switch strings.ToUpper(strings.TrimSpace(t)) {
		case "TCP":
			hasTCP = true
		case "UDP":
			hasUDP = true
		}
	}
	switch {
	case hasTCP:
		return []string{"TCP"}
	case hasUDP:
		return []string{"UDP"}
	default:
		return []string{"TCP"}
	}
}

// shareHostAuthority puts IPv6 literals in brackets for URL Host (no port).
// Official mierus:// keeps the listen port in the query, so Host is host-only.
func shareHostAuthority(host string) string {
	h := strings.TrimSpace(host)
	if h == "" {
		return h
	}
	if strings.HasPrefix(h, "[") {
		return h
	}
	if ip := net.ParseIP(h); ip != nil && ip.To4() == nil {
		return "[" + h + "]"
	}
	return h
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

// RandomHex is the exported form of randomHex for per-user inbound secrets.
func RandomHex(nBytes int) string { return randomHex(nBytes) }

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

// RandomB64Std is the exported form of randomB64Std for per-user inbound secrets.
func RandomB64Std(nBytes int) string { return randomB64Std(nBytes) }

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

// NewUUID is the exported form of newUUID for per-user VLESS clients.
func NewUUID() string { return newUUID() }
