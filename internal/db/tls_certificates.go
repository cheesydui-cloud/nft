package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TLS certificate sources stored in tls_certificates.source.
const (
	TLSCertSourceACME       = "acme"
	TLSCertSourceUpload     = "upload"
	TLSCertSourceSelfSigned = "selfsigned"
)

// TLSCertificate is one entry in the panel certificate vault.
type TLSCertificate struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Domain       string `json:"domain"`
	CertPEM      string `json:"cert_pem,omitempty"`
	KeyPEM       string `json:"key_pem,omitempty"`
	Source       string `json:"source"`
	ACMEEnabled  bool   `json:"acme_enabled"`
	ACMEProvider string `json:"acme_provider,omitempty"`
	ACMEIssuer   string `json:"acme_issuer,omitempty"`
	NotBefore    string `json:"not_before,omitempty"`
	NotAfter     string `json:"not_after,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	LastRenewAt  string `json:"last_renew_at,omitempty"`
	Note         string `json:"note,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	// Computed / join fields for API views (not always loaded).
	CertConfigured bool `json:"cert_configured,omitempty"`
	KeyConfigured  bool `json:"key_configured,omitempty"`
	// RefCount is number of proxy_services whose config_json references this id.
	RefCount int `json:"ref_count,omitempty"`
}

const tlsCertificateCols = `id, name, domain, cert_pem, key_pem, source, acme_enabled,
  acme_provider, acme_issuer, not_before, not_after, fingerprint, last_error, last_renew_at,
  note, created_at, updated_at`

func scanTLSCertificate(r rowScanner) (*TLSCertificate, error) {
	c := &TLSCertificate{}
	var acme int
	if err := r.Scan(
		&c.ID, &c.Name, &c.Domain, &c.CertPEM, &c.KeyPEM, &c.Source, &acme,
		&c.ACMEProvider, &c.ACMEIssuer, &c.NotBefore, &c.NotAfter, &c.Fingerprint,
		&c.LastError, &c.LastRenewAt, &c.Note, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	c.ACMEEnabled = acme == 1
	c.CertConfigured = strings.TrimSpace(c.CertPEM) != ""
	c.KeyConfigured = strings.TrimSpace(c.KeyPEM) != ""
	return c, nil
}

// CreateTLSCertificate inserts a vault certificate row.
func CreateTLSCertificate(d *sql.DB, c *TLSCertificate) (*TLSCertificate, error) {
	if c == nil {
		return nil, fmt.Errorf("nil certificate")
	}
	now := time.Now().Unix()
	name := strings.TrimSpace(c.Name)
	domain := strings.TrimSpace(c.Domain)
	if name == "" {
		name = domain
	}
	if name == "" {
		name = "certificate"
	}
	source := strings.ToLower(strings.TrimSpace(c.Source))
	if source == "" {
		source = TLSCertSourceUpload
	}
	acme := 0
	if c.ACMEEnabled {
		acme = 1
	}
	res, err := d.Exec(`INSERT INTO tls_certificates(
		name, domain, cert_pem, key_pem, source, acme_enabled,
		acme_provider, acme_issuer, not_before, not_after, fingerprint,
		last_error, last_renew_at, note, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		name, domain, c.CertPEM, c.KeyPEM, source, acme,
		c.ACMEProvider, c.ACMEIssuer, c.NotBefore, c.NotAfter, c.Fingerprint,
		c.LastError, c.LastRenewAt, c.Note, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return GetTLSCertificate(d, id)
}

// UpdateTLSCertificateMeta updates non-secret metadata (name/note/domain label).
func UpdateTLSCertificateMeta(d *sql.DB, id int64, name, domain, note string) error {
	now := time.Now().Unix()
	name = strings.TrimSpace(name)
	domain = strings.TrimSpace(domain)
	_, err := d.Exec(`UPDATE tls_certificates SET name=?, domain=?, note=?, updated_at=? WHERE id=?`,
		name, domain, note, now, id)
	return err
}

// UpdateTLSCertificateMaterial replaces PEM material and derived fields.
func UpdateTLSCertificateMaterial(d *sql.DB, id int64, certPEM, keyPEM, source string, acmeEnabled bool,
	provider, issuer, notBefore, notAfter, fingerprint, lastErr, lastRenew string) error {
	now := time.Now().Unix()
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = TLSCertSourceUpload
	}
	acme := 0
	if acmeEnabled {
		acme = 1
	}
	_, err := d.Exec(`UPDATE tls_certificates SET
		cert_pem=?, key_pem=?, source=?, acme_enabled=?,
		acme_provider=?, acme_issuer=?, not_before=?, not_after=?, fingerprint=?,
		last_error=?, last_renew_at=?, updated_at=?
		WHERE id=?`,
		certPEM, keyPEM, source, acme,
		provider, issuer, notBefore, notAfter, fingerprint,
		lastErr, lastRenew, now, id,
	)
	return err
}

// PatchTLSCertificateError records a renew/issue failure without wiping PEM.
func PatchTLSCertificateError(d *sql.DB, id int64, lastErr string) error {
	now := time.Now().Unix()
	_, err := d.Exec(`UPDATE tls_certificates SET last_error=?, updated_at=? WHERE id=?`, lastErr, now, id)
	return err
}

// GetTLSCertificate returns one vault row by id (with PEMs).
func GetTLSCertificate(d *sql.DB, id int64) (*TLSCertificate, error) {
	row := d.QueryRow(`SELECT `+tlsCertificateCols+` FROM tls_certificates WHERE id=?`, id)
	c, err := scanTLSCertificate(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("certificate not found")
	}
	return c, err
}

// ListTLSCertificates returns all vault certificates ordered by domain/id.
func ListTLSCertificates(d *sql.DB) ([]*TLSCertificate, error) {
	rows, err := d.Query(`SELECT ` + tlsCertificateCols + ` FROM tls_certificates ORDER BY domain ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TLSCertificate
	for rows.Next() {
		c, err := scanTLSCertificate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListTLSCertificatesACMEEnabled returns ACME-managed certs for renew enforcer.
func ListTLSCertificatesACMEEnabled(d *sql.DB) ([]*TLSCertificate, error) {
	rows, err := d.Query(`SELECT `+tlsCertificateCols+` FROM tls_certificates WHERE acme_enabled=1 ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TLSCertificate
	for rows.Next() {
		c, err := scanTLSCertificate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTLSCertificate removes a vault row.
func DeleteTLSCertificate(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM tls_certificates WHERE id=?`, id)
	return err
}

// CountProxyServicesByCertID counts services whose config_json.cert_id equals id.
// SQLite json_extract works on modernc; fall back to scan if unavailable.
func CountProxyServicesByCertID(d *sql.DB, certID int64) (int, error) {
	if certID <= 0 {
		return 0, nil
	}
	// Prefer json_extract (SQLite JSON1).
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM proxy_services
		WHERE CAST(json_extract(config_json, '$.cert_id') AS INTEGER) = ?`, certID).Scan(&n)
	if err == nil {
		return n, nil
	}
	// Fallback: load and parse.
	list, err2 := ListProxyServices(d)
	if err2 != nil {
		return 0, err
	}
	for _, svc := range list {
		if svc == nil {
			continue
		}
		if CertIDFromConfigJSON(svc.ConfigJSON) == certID {
			n++
		}
	}
	return n, nil
}

// ListProxyServiceIDsByCertID returns service ids referencing cert_id.
func ListProxyServiceIDsByCertID(d *sql.DB, certID int64) ([]int64, error) {
	if certID <= 0 {
		return nil, nil
	}
	rows, err := d.Query(`SELECT id FROM proxy_services
		WHERE CAST(json_extract(config_json, '$.cert_id') AS INTEGER) = ?`, certID)
	if err != nil {
		// Fallback scan.
		list, err2 := ListProxyServices(d)
		if err2 != nil {
			return nil, err
		}
		var out []int64
		for _, svc := range list {
			if svc != nil && CertIDFromConfigJSON(svc.ConfigJSON) == certID {
				out = append(out, svc.ID)
			}
		}
		return out, nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CertIDFromConfigJSON extracts config_json.cert_id (float64/int/string tolerant).
func CertIDFromConfigJSON(raw []byte) int64 {
	if len(raw) == 0 {
		return 0
	}
	return certIDFromJSONBytes(raw)
}

func certIDFromJSONBytes(raw []byte) int64 {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return 0
	}
	return anyToInt64(m["cert_id"])
}

func anyToInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case int:
		return int64(t)
	case int64:
		return t
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	default:
		return 0
	}
}
