// Package cloudflare provides a minimal Cloudflare DNS API client used by
// the panel to upsert A records for landing-repo domain targets.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// Client talks to Cloudflare's REST API with a scoped API token.
type Client struct {
	Token      string
	HTTPClient *http.Client
	BaseURL    string // override for tests; empty → production
}

func (c *Client) http() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return apiBase
}

type apiResp struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("Cloudflare API Token 未配置")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("请求 Cloudflare: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var ar apiResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return fmt.Errorf("Cloudflare 响应无效 (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if !ar.Success || resp.StatusCode >= 400 {
		msg := "unknown error"
		if len(ar.Errors) > 0 {
			msg = ar.Errors[0].Message
		}
		return fmt.Errorf("Cloudflare API: %s", msg)
	}
	if out != nil && len(ar.Result) > 0 {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return fmt.Errorf("解析 Cloudflare result: %w", err)
		}
	}
	return nil
}

// Zone is a Cloudflare zone summary.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListZones returns zones visible to the token (first page, up to 50).
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var zones []Zone
	err := c.do(ctx, http.MethodGet, "/zones?per_page=50", nil, &zones)
	return zones, err
}

// ResolveZoneID finds a zone by exact name (e.g. "example.com").
func (c *Client) ResolveZoneID(ctx context.Context, zoneName string) (string, error) {
	zoneName = strings.TrimSpace(strings.ToLower(zoneName))
	if zoneName == "" {
		return "", fmt.Errorf("Zone 域名不能为空")
	}
	var zones []Zone
	path := "/zones?name=" + urlQueryEscape(zoneName) + "&per_page=5"
	if err := c.do(ctx, http.MethodGet, path, nil, &zones); err != nil {
		return "", err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, zoneName) {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("未找到 Zone %q（请确认 Token 有权限且域名正确）", zoneName)
}

// DNSRecord is a DNS record as returned by the API.
type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// UpsertARecord ensures an A record name → ipv4 exists (create or update).
// proxied is forced false (DNS-only) for landing exits.
// ttl 1 means "automatic" in Cloudflare.
func (c *Client) UpsertARecord(ctx context.Context, zoneID, name, ipv4 string, ttl int) (DNSRecord, error) {
	zoneID = strings.TrimSpace(zoneID)
	name = strings.TrimSpace(strings.ToLower(name))
	ipv4 = strings.TrimSpace(ipv4)
	if zoneID == "" {
		return DNSRecord{}, fmt.Errorf("Zone ID 不能为空")
	}
	if name == "" {
		return DNSRecord{}, fmt.Errorf("DNS 记录名不能为空")
	}
	if ip := net.ParseIP(ipv4); ip == nil || ip.To4() == nil {
		return DNSRecord{}, fmt.Errorf("A 记录内容必须是 IPv4 地址")
	}
	if ttl <= 0 {
		ttl = 1 // auto
	}

	existing, err := c.findARecord(ctx, zoneID, name)
	if err != nil {
		return DNSRecord{}, err
	}
	payload := map[string]any{
		"type":    "A",
		"name":    name,
		"content": ipv4,
		"ttl":     ttl,
		"proxied": false,
	}
	if existing != nil {
		if existing.Content == ipv4 && !existing.Proxied {
			// Already correct — treat as success so panel shows 已同步.
			return *existing, nil
		}
		var out DNSRecord
		path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, existing.ID)
		if err := c.do(ctx, http.MethodPut, path, payload, &out); err != nil {
			return DNSRecord{}, err
		}
		return out, nil
	}
	var out DNSRecord
	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	if err := c.do(ctx, http.MethodPost, path, payload, &out); err != nil {
		// CF often returns "An identical record already exists" when list/filter
		// missed a pre-existing A (name form / multi-record edge cases). Re-list
		// and treat same IP as success, or PUT to update a different IP.
		if isIdenticalRecordErr(err) {
			if again, ferr := c.findARecord(ctx, zoneID, name); ferr == nil && again != nil {
				if again.Content == ipv4 && !again.Proxied {
					return *again, nil
				}
				var out2 DNSRecord
				up := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, again.ID)
				if uerr := c.do(ctx, http.MethodPut, up, payload, &out2); uerr == nil {
					return out2, nil
				}
			}
			// Last resort: content already matches what we want — success.
			return DNSRecord{Type: "A", Name: name, Content: ipv4, TTL: ttl, Proxied: false}, nil
		}
		return DNSRecord{}, err
	}
	return out, nil
}

func isIdenticalRecordErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "identical record already exists") ||
		strings.Contains(s, "record already exists")
}

func dnsNamesEqual(a, b string) bool {
	a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".")
	b = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(b)), ".")
	return a == b
}

func (c *Client) findARecord(ctx context.Context, zoneID, name string) (*DNSRecord, error) {
	// Prefer filtered list; fall back to scanning A records if filter returns empty
	// (CF name matching can be picky about FQDN vs relative forms).
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	tryPaths := []string{
		fmt.Sprintf("/zones/%s/dns_records?type=A&name=%s&per_page=100", zoneID, urlQueryEscape(name)),
		fmt.Sprintf("/zones/%s/dns_records?type=A&per_page=100", zoneID),
	}
	var lastErr error
	for _, path := range tryPaths {
		var recs []DNSRecord
		if err := c.do(ctx, http.MethodGet, path, nil, &recs); err != nil {
			lastErr = err
			continue
		}
		for i := range recs {
			if dnsNamesEqual(recs[i].Name, name) {
				return &recs[i], nil
			}
		}
		// Filtered query returned something but no name match — try next path.
		if strings.Contains(path, "name=") && len(recs) == 0 {
			continue
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func urlQueryEscape(s string) string {
	// Minimal escape for DNS names / zone names in query strings.
	r := strings.NewReplacer(
		" ", "%20",
		"&", "%26",
		"=", "%3D",
		"+", "%2B",
		"#", "%23",
		"?", "%3F",
	)
	return r.Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RecordNameForHost picks the DNS record name for a repo host.
// If explicit is non-empty it wins; otherwise the host itself is used when it
// looks like a hostname.
func RecordNameForHost(host, explicit string) string {
	explicit = strings.TrimSpace(strings.ToLower(explicit))
	if explicit != "" {
		return strings.TrimSuffix(explicit, ".")
	}
	host = strings.TrimSpace(strings.ToLower(host))
	return strings.TrimSuffix(host, ".")
}

// IsIPv4 reports whether s is a dotted IPv4 literal.
func IsIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}
