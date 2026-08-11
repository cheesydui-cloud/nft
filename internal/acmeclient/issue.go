// Package acmeclient issues TLS certificates via ACME DNS-01 (Cloudflare).
package acmeclient

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"nft/internal/cloudflare"
)

const (
	// LetsEncryptProduction is the public LE directory.
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	// LetsEncryptStaging for dry-run / rate-limit safe tests.
	LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// IssueRequest is one certificate order for a single domain (or IP not supported by LE).
type IssueRequest struct {
	Domain    string // FQDN for SAN + CN
	Email     string // ACME account contact
	Directory string // empty → production LE
	// AccountKey is the ACME account private key (ECDSA). If nil, a new key is generated
	// and returned in Result.AccountKeyPEM for persistence.
	AccountKey *ecdsa.PrivateKey
	// CF client used for DNS-01 TXT challenges.
	CF *cloudflare.Client
	// ZoneHint optional CF zone id; ZoneName default zone name from settings.
	ZoneID   string
	ZoneName string
}

// Result holds issued PEMs and metadata.
type Result struct {
	CertPEM       string
	KeyPEM        string
	AccountKeyPEM string // always set (new or re-encoded existing)
	Issuer        string
	NotBefore     time.Time
	NotAfter      time.Time
	Domain        string
}

// IssueDNS01 obtains a certificate using Cloudflare DNS-01.
func IssueDNS01(ctx context.Context, req IssueRequest) (*Result, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.Domain)), ".")
	if domain == "" {
		return nil, fmt.Errorf("域名不能为空")
	}
	if net.ParseIP(domain) != nil {
		return nil, fmt.Errorf("Let's Encrypt DNS-01 不支持纯 IP，请使用域名")
	}
	if req.CF == nil || strings.TrimSpace(req.CF.Token) == "" {
		return nil, fmt.Errorf("未配置 Cloudflare API Token（系统设置）")
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = "admin@" + domain
	}
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		dir = LetsEncryptProduction
	}

	accountKey := req.AccountKey
	if accountKey == nil {
		var err error
		accountKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("生成 ACME 账户密钥: %w", err)
		}
	}
	accountKeyPEM, err := encodeECPrivateKey(accountKey)
	if err != nil {
		return nil, err
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: dir,
	}

	// Register / ensure account (IgnoreExisting for re-runs with same key).
	acct := &acme.Account{Contact: []string{"mailto:" + email}}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil {
		// Account may already exist.
		if !isAccountExists(err) {
			return nil, fmt.Errorf("ACME 注册账户: %w", err)
		}
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return nil, fmt.Errorf("ACME 创建订单: %w", err)
	}

	// Resolve zone once.
	zoneID, _, err := cloudflare.ResolveZoneForHostname(ctx, req.CF, domain, req.ZoneID, req.ZoneName)
	if err != nil {
		return nil, fmt.Errorf("解析 Cloudflare Zone: %w", err)
	}

	// Fulfil DNS-01 for each pending authz.
	var createdTXT []struct{ name, content string }
	defer func() {
		// Best-effort cleanup of challenge TXT records.
		for _, t := range createdTXT {
			_ = req.CF.DeleteTXTByNameContent(context.Background(), zoneID, t.name, t.content)
		}
	}()

	for _, authzURL := range order.AuthzURLs {
		az, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return nil, fmt.Errorf("获取授权: %w", err)
		}
		if az.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for i := range az.Challenges {
			if az.Challenges[i].Type == "dns-01" {
				chal = az.Challenges[i]
				break
			}
		}
		if chal == nil {
			return nil, fmt.Errorf("ACME 未提供 dns-01 挑战（域名 %s）", domain)
		}
		dnsValue, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return nil, fmt.Errorf("计算 DNS-01 值: %w", err)
		}
		txtName := "_acme-challenge." + domain
		if _, err := req.CF.UpsertTXTRecord(ctx, zoneID, txtName, dnsValue, 120); err != nil {
			return nil, fmt.Errorf("写入 Cloudflare TXT %s: %w", txtName, err)
		}
		createdTXT = append(createdTXT, struct{ name, content string }{txtName, dnsValue})

		// Allow DNS propagation before telling ACME to check.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(8 * time.Second):
		}

		if _, err := client.Accept(ctx, chal); err != nil {
			return nil, fmt.Errorf("接受 DNS-01 挑战: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, authzURL); err != nil {
			return nil, fmt.Errorf("等待授权通过: %w", err)
		}
	}

	// Wait for order ready then finalize with CSR.
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, fmt.Errorf("等待订单就绪: %w", err)
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, certKey)
	if err != nil {
		return nil, fmt.Errorf("生成 CSR: %w", err)
	}

	ders, curl, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return nil, fmt.Errorf("签发证书: %w", err)
	}
	_ = curl
	if len(ders) == 0 {
		return nil, fmt.Errorf("ACME 返回空证书链")
	}

	// Encode leaf + intermediates as cert PEM chain.
	var certPEM strings.Builder
	var leaf *x509.Certificate
	for i, der := range ders {
		certPEM.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		if i == 0 {
			leaf, _ = x509.ParseCertificate(der)
		}
	}
	keyPEM, err := encodeECPrivateKey(certKey)
	if err != nil {
		return nil, err
	}

	res := &Result{
		CertPEM:       certPEM.String(),
		KeyPEM:        keyPEM,
		AccountKeyPEM: accountKeyPEM,
		Issuer:        "letsencrypt",
		Domain:        domain,
	}
	if leaf != nil {
		res.NotBefore = leaf.NotBefore
		res.NotAfter = leaf.NotAfter
		if leaf.Issuer.CommonName != "" {
			res.Issuer = leaf.Issuer.CommonName
		}
	}
	return res, nil
}

func encodeECPrivateKey(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

// ParseAccountKeyPEM loads an ECDSA private key from PEM (EC or PKCS8).
func ParseAccountKeyPEM(pemStr string) (*ecdsa.PrivateKey, error) {
	pemStr = strings.TrimSpace(pemStr)
	if pemStr == "" {
		return nil, nil
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("ACME 账户密钥 PEM 无效")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("ACME 账户密钥须为 ECDSA")
		}
		return ek, nil
	default:
		return nil, fmt.Errorf("不支持的密钥类型 %q", block.Type)
	}
}

// EnsureAccountKey returns existing key or generates a new one; returns PEM always.
func EnsureAccountKey(existingPEM string) (*ecdsa.PrivateKey, string, error) {
	if k, err := ParseAccountKeyPEM(existingPEM); err != nil {
		return nil, "", err
	} else if k != nil {
		pemOut, err := encodeECPrivateKey(k)
		return k, pemOut, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemOut, err := encodeECPrivateKey(k)
	return k, pemOut, err
}

func isAccountExists(err error) bool {
	if err == nil {
		return false
	}
	// acme.Error with status 409 or message contains already
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already been registered") ||
		strings.Contains(s, "account already exists") ||
		strings.Contains(s, "409")
}

// PublicKey is exported for tests / type assert.
var _ crypto.Signer = (*ecdsa.PrivateKey)(nil)
