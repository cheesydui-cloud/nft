package daemon

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultConnectFile is the durable panel connect URL written by install.sh
// and by panel_redirect. When present it overrides systemd --connect so a
// host migration can re-point agents without rewriting unit files.
const DefaultConnectFile = "/etc/nft/panel.connect"

// ReadConnectURL returns the trimmed URL from path, or "" if missing/empty.
func ReadConnectURL(path string) (string, error) {
	if path == "" {
		path = DefaultConnectFile
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// WriteConnectURL atomically writes a single-line connect URL (mode 0600).
func WriteConnectURL(path, raw string) error {
	if path == "" {
		path = DefaultConnectFile
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty connect url")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// NormalizePanelConnectURL turns an operator-facing panel URL into the
// WebSocket endpoint the dialer uses. Accepts https/http/wss/ws; appends
// /v1/agents when missing. Plaintext schemes require allowInsecure (same
// policy as --insecure-connect / install.sh).
func NormalizePanelConnectURL(raw string, allowInsecure bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("panel url 为空")
	}
	// Bare host:port → assume https for redirect (safer default than http).
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("panel url 无法解析: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		u.Scheme = "wss"
	case "http":
		if !allowInsecure {
			return "", fmt.Errorf("拒绝明文 http:// 控制信道；请使用 https://，或 agent 以 --insecure-connect 运行")
		}
		u.Scheme = "ws"
	case "wss":
		// ok
	case "ws":
		if !allowInsecure {
			return "", fmt.Errorf("拒绝明文 ws:// 控制信道；请使用 wss://，或 agent 以 --insecure-connect 运行")
		}
	default:
		return "", fmt.Errorf("panel url 协议必须是 https/wss（当前 %q）", u.Scheme)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" || path == "/" {
		u.Path = "/v1/agents"
	} else if !strings.HasSuffix(path, "/v1/agents") {
		// Keep custom path prefix if any, still ensure agents endpoint.
		if strings.HasSuffix(path, "/v1") {
			u.Path = path + "/agents"
		} else {
			u.Path = path + "/v1/agents"
		}
	} else {
		u.Path = path
	}
	u.RawQuery = ""
	u.Fragment = ""
	out := u.String()
	// url.String may leave empty path host-only forms; ensure path present.
	if u.Path == "" {
		out = strings.TrimRight(out, "/") + "/v1/agents"
	}
	return out, nil
}

// ResolveConnectURL prefers the durable file when set, else the CLI flag.
func ResolveConnectURL(filePath, flagURL string) (string, error) {
	fromFile, err := ReadConnectURL(filePath)
	if err != nil {
		return "", err
	}
	if fromFile != "" {
		return fromFile, nil
	}
	return strings.TrimSpace(flagURL), nil
}
