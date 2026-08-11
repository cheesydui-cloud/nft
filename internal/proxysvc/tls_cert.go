package proxysvc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// TLSCertInfo is a safe summary of a server certificate (no private material).
type TLSCertInfo struct {
	Configured bool     `json:"configured"`
	CN         string   `json:"cn,omitempty"`
	DNSNames   []string `json:"dns_names,omitempty"`
	IPs        []string `json:"ips,omitempty"`
	NotBefore  string   `json:"not_before,omitempty"` // RFC3339
	NotAfter   string   `json:"not_after,omitempty"`  // RFC3339
	DaysLeft   int      `json:"days_left,omitempty"`
	Expired    bool     `json:"expired,omitempty"`
	Expiring   bool     `json:"expiring,omitempty"` // within 14 days
	Fingerprint string  `json:"fingerprint,omitempty"` // SHA-256 of DER, hex
	KeyMatch   *bool    `json:"key_match,omitempty"`
	SANMatch   *bool    `json:"san_match,omitempty"` // server_name in CN/SAN when checked
	Error      string   `json:"error,omitempty"`
}

// ParseCertificatePEM decodes the first CERTIFICATE block from PEM text.
func ParseCertificatePEM(certPEM string) (*x509.Certificate, error) {
	certPEM = strings.TrimSpace(certPEM)
	if certPEM == "" {
		return nil, fmt.Errorf("证书 PEM 为空")
	}
	var rest = []byte(certPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("无法解析证书 PEM（需要 -----BEGIN CERTIFICATE-----）")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
}

// ParsePrivateKeyPEM decodes PKCS#1 / PKCS#8 / EC private keys from PEM.
func ParsePrivateKeyPEM(keyPEM string) (any, error) {
	keyPEM = strings.TrimSpace(keyPEM)
	if keyPEM == "" {
		return nil, fmt.Errorf("私钥 PEM 为空")
	}
	var rest = []byte(keyPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("无法解析私钥 PEM（需要 BEGIN PRIVATE KEY / RSA / EC）")
		}
		switch block.Type {
		case "PRIVATE KEY":
			return x509.ParsePKCS8PrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		default:
			// try next block
			if len(rest) == 0 {
				return nil, fmt.Errorf("不支持的私钥类型 %q", block.Type)
			}
		}
	}
}

// PublicKeysEqual reports whether the private key matches the certificate public key.
func PublicKeysEqual(cert *x509.Certificate, key any) bool {
	if cert == nil || key == nil {
		return false
	}
	switch k := key.(type) {
	case *rsa.PrivateKey:
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return false
		}
		return k.PublicKey.N.Cmp(pub.N) == 0 && k.PublicKey.E == pub.E
	case *ecdsa.PrivateKey:
		pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return false
		}
		return k.PublicKey.X.Cmp(pub.X) == 0 && k.PublicKey.Y.Cmp(pub.Y) == 0 && k.Curve == pub.Curve
	default:
		return false
	}
}

// NameInCertificate reports whether host matches CN or DNS/IP SANs (wildcard ok).
func NameInCertificate(cert *x509.Certificate, host string) bool {
	if cert == nil {
		return false
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return false
	}
	// IP literal
	if ip := net.ParseIP(host); ip != nil {
		for _, cip := range cert.IPAddresses {
			if cip.Equal(ip) {
				return true
			}
		}
		return false
	}
	if err := cert.VerifyHostname(host); err == nil {
		return true
	}
	// Fallback CN exact / wildcard
	cn := strings.ToLower(cert.Subject.CommonName)
	if cn == host {
		return true
	}
	if strings.HasPrefix(cn, "*.") && strings.HasSuffix(host, cn[1:]) {
		// *.example.com matches a.example.com but not example.com
		rest := host[len(host)-len(cn)+1:]
		if rest == cn[1:] && !strings.Contains(host[:len(host)-len(cn)+1], ".") {
			return true
		}
	}
	return false
}

// InspectTLSCert builds a summary; if keyPEM/serverName empty, skip those checks.
func InspectTLSCert(certPEM, keyPEM, serverName string) TLSCertInfo {
	info := TLSCertInfo{}
	certPEM = strings.TrimSpace(certPEM)
	if certPEM == "" {
		return info
	}
	info.Configured = true
	cert, err := ParseCertificatePEM(certPEM)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.CN = cert.Subject.CommonName
	info.DNSNames = append([]string(nil), cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		info.IPs = append(info.IPs, ip.String())
	}
	info.NotBefore = cert.NotBefore.UTC().Format(time.RFC3339)
	info.NotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	now := time.Now()
	if now.After(cert.NotAfter) {
		info.Expired = true
		info.DaysLeft = 0
	} else {
		info.DaysLeft = int(cert.NotAfter.Sub(now).Hours() / 24)
		if info.DaysLeft <= 14 {
			info.Expiring = true
		}
	}
	sum := sha256.Sum256(cert.Raw)
	info.Fingerprint = hex.EncodeToString(sum[:])

	if strings.TrimSpace(keyPEM) != "" {
		key, err := ParsePrivateKeyPEM(keyPEM)
		match := err == nil && PublicKeysEqual(cert, key)
		info.KeyMatch = &match
		if err != nil {
			info.Error = err.Error()
		} else if !match {
			info.Error = "证书与私钥不匹配"
		}
	}
	if sn := strings.TrimSpace(serverName); sn != "" {
		ok := NameInCertificate(cert, sn)
		info.SANMatch = &ok
		if !ok && info.Error == "" {
			// soft: leave Error empty; callers decide hard vs warn
		}
	}
	return info
}

// ValidateTLSCertPair hard-checks PEM parse + key match for deploy.
// serverName SAN mismatch is a hard error when serverName is non-empty.
func ValidateTLSCertPair(certPEM, keyPEM, serverName string) error {
	cert, err := ParseCertificatePEM(certPEM)
	if err != nil {
		return err
	}
	key, err := ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return err
	}
	if !PublicKeysEqual(cert, key) {
		return fmt.Errorf("证书与私钥不匹配（public key 不一致）")
	}
	now := time.Now()
	if now.After(cert.NotAfter) {
		return fmt.Errorf("证书已过期（notAfter=%s）", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("证书尚未生效（notBefore=%s）", cert.NotBefore.UTC().Format(time.RFC3339))
	}
	if sn := strings.TrimSpace(serverName); sn != "" {
		if !NameInCertificate(cert, sn) {
			return fmt.Errorf("server_name %q 不在证书 CN/SAN 中（CN=%q DNS=%v）", sn, cert.Subject.CommonName, cert.DNSNames)
		}
	}
	return nil
}

// GenerateSelfSignedTLS creates a 1-year ECDSA P-256 self-signed cert for domain (or IP).
// Intended for lab/debug only — clients need allowInsecure or trust the cert.
func GenerateSelfSignedTLS(serverName string, validDays int) (certPEM, keyPEM string, info TLSCertInfo, err error) {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return "", "", info, fmt.Errorf("生成自签证书需要域名或 IP（server_name）")
	}
	if validDays <= 0 {
		validDays = 365
	}
	if validDays > 825 {
		validDays = 825 // ~27 months common CA max
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", info, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", info, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   serverName,
			Organization: []string{"NFT self-signed (debug)"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.AddDate(0, 0, validDays),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(serverName); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{serverName}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", info, err
	}
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", info, err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM = string(certOut)
	keyPEM = string(keyOut)
	info = InspectTLSCert(certPEM, keyPEM, serverName)
	return certPEM, keyPEM, info, nil
}

	// RedactVLESSConfigJSON replaces sensitive PEM/key material for API responses.
	// Adds cert_info / cert_configured / key_configured for UI. DB storage is unchanged.
	func RedactVLESSConfigJSON(raw json.RawMessage) json.RawMessage {
		if len(raw) == 0 {
			return raw
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m == nil {
			return raw
		}
		certPEM, _ := m["cert_pem"].(string)
		keyPEM, _ := m["key_pem"].(string)
		serverName, _ := m["server_name"].(string)
		hasCert := strings.TrimSpace(certPEM) != ""
		hasKey := strings.TrimSpace(keyPEM) != ""
		// cert_id vault reference counts as configured even before PEMs are inlined.
		if certIDFromAny(m["cert_id"]) > 0 {
			m["cert_configured"] = true
			m["key_configured"] = true
		}
		if hasCert || hasKey {
			info := InspectTLSCert(certPEM, keyPEM, serverName)
			info.Configured = hasCert
			m["cert_info"] = info
			m["cert_configured"] = hasCert || certIDFromAny(m["cert_id"]) > 0
			m["key_configured"] = hasKey || certIDFromAny(m["cert_id"]) > 0
			if hasCert {
				m["cert_pem"] = ""
			}
			if hasKey {
				m["key_pem"] = ""
			}
		}
	// Soft-redact REALITY private key (keep public/short_id for display).
	if pk, ok := m["private_key"].(string); ok && strings.TrimSpace(pk) != "" {
		m["private_key_configured"] = true
		// Keep full private_key for edit convenience in admin UI? User asked PEM mainly.
		// Truncate in API so list/detail don't leak full key; PATCH preserve handles empty.
		// Actually wizard edit needs either full key or empty+preserve. Empty works with mergePreserved.
		m["private_key"] = ""
	}
	// decryption is also sensitive (vlessenc server material)
	if dec, ok := m["decryption"].(string); ok && strings.TrimSpace(dec) != "" && !strings.EqualFold(strings.TrimSpace(dec), "none") {
		m["decryption_configured"] = true
		m["decryption"] = ""
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// RedactProxyConfigJSON redacts by protocol.
func RedactProxyConfigJSON(protocol string, raw json.RawMessage) json.RawMessage {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "vless":
		return RedactVLESSConfigJSON(raw)
	case "shadowsocks", "ss":
		return redactSSPassword(raw)
	case "mieru":
		return redactMieruPassword(raw)
	case "socks5", "socks":
		return redactUserPass(raw)
	case "anytls":
		return redactAnyTLS(raw)
	case "naive", "naiveproxy":
		return redactNaive(raw)
	default:
		return raw
	}
}

func redactUserPass(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return raw
	}
	if p, ok := m["password"].(string); ok && p != "" {
		m["password_configured"] = true
		m["password"] = ""
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func certIDFromAny(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		var n int64
		for _, ch := range strings.TrimSpace(t) {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int64(ch-'0')
		}
		return n
	default:
		return 0
	}
}

func redactTLSPEMs(m map[string]any) {
		hasCert, hasKey := false, false
		if s, ok := m["cert_pem"].(string); ok && strings.TrimSpace(s) != "" {
			hasCert = true
			m["cert_pem"] = ""
		}
		if s, ok := m["key_pem"].(string); ok && strings.TrimSpace(s) != "" {
			hasKey = true
			m["key_pem"] = ""
		}
		if certIDFromAny(m["cert_id"]) > 0 {
			m["cert_configured"] = true
			m["key_configured"] = true
		}
		if hasCert {
			m["cert_configured"] = true
		}
		if hasKey {
		m["key_configured"] = true
	}
}

func redactAnyTLS(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return raw
	}
	if p, ok := m["password"].(string); ok && p != "" {
		m["password_configured"] = true
		m["password"] = ""
	}
	redactTLSPEMs(m)
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func redactNaive(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return raw
	}
	if p, ok := m["password"].(string); ok && p != "" {
		m["password_configured"] = true
		m["password"] = ""
	}
	redactTLSPEMs(m)
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func redactSSPassword(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return raw
	}
	if p, ok := m["password"].(string); ok && p != "" {
		m["password_configured"] = true
		m["password"] = ""
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func redactMieruPassword(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(nonzeroJSON(raw), &m); err != nil {
		return raw
	}
	if p, ok := m["password"].(string); ok && p != "" {
		m["password_configured"] = true
		m["password"] = ""
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
