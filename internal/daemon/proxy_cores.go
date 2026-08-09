package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nft/internal/proxysvc"
	"nft/internal/wsproto"
)

// detectProxyCores scans common install locations and PATH for xray / sing-box / mieru(mita).
// Used in hello so the panel can filter nodes when publishing proxy services.
func detectProxyCores() []wsproto.CoreInfo {
	specs := []struct {
		name  string
		bins  []string
		paths []string
	}{
		{
			name: "xray",
			bins: []string{"xray"},
			paths: []string{
				"/usr/local/bin/xray",
				"/usr/bin/xray",
				"/opt/xray/xray",
				"/var/lib/nft/cores/xray/xray",
			},
		},
		{
			name: "sing-box",
			bins: []string{"sing-box", "singbox"},
			paths: []string{
				"/usr/local/bin/sing-box",
				"/usr/bin/sing-box",
				"/var/lib/nft/cores/sing-box/sing-box",
			},
		},
		{
			// Server binary is mita; mieru is the client. Prefer mita for deploy.
			name: "mieru",
			bins: []string{"mita", "mieru", "mbox"},
			paths: []string{
				"/usr/local/bin/mita",
				"/usr/bin/mita",
				"/usr/local/bin/mieru",
				"/usr/bin/mieru",
				"/var/lib/nft/cores/mieru/mita",
				"/var/lib/nft/cores/mieru/mieru",
			},
		},
	}
	var out []wsproto.CoreInfo
	seen := map[string]bool{}
	for _, sp := range specs {
		if seen[sp.name] {
			continue
		}
		path := findCoreBinary(sp.bins, sp.paths)
		if path == "" {
			continue
		}
		ver := probeCoreVersion(path)
		out = append(out, wsproto.CoreInfo{Name: sp.name, Version: ver, Path: path})
		seen[sp.name] = true
	}
	return out
}

func findCoreBinary(names, absPaths []string) string {
	for _, p := range absPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	// Also check relative under /etc/nft/cores/*/bin
	entries, _ := filepath.Glob("/etc/nft/cores/*/bin")
	for _, p := range entries {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			base := filepath.Base(filepath.Dir(p))
			for _, n := range names {
				if base == n || strings.Contains(base, n) {
					return p
				}
			}
		}
	}
	return ""
}

func probeCoreVersion(path string) string {
	// Best-effort; failures leave version empty.
	for _, args := range [][]string{{"version"}, {"-version"}, {"--version"}, {"status"}} {
		cmd := exec.Command(path, args...)
		b, err := cmd.CombinedOutput()
		if err != nil && len(b) == 0 {
			continue
		}
		line := strings.TrimSpace(string(b))
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, '\n'); i > 0 {
			line = line[:i]
		}
		if len(line) > 80 {
			line = line[:80]
		}
		return line
	}
	return ""
}

// handleProxyServiceApply deploys one proxy-service instance on this host.
// mieru: real mita apply+start when mita is installed.
// vless / shadowsocks: still dry-run until core merge is wired (return clear error).
func handleProxyServiceApply(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	switch proto {
	case "mieru":
		return deployMieru(req)
	case "vless", "shadowsocks", "ss":
		return wsproto.ProxyServiceApplyAck{
			OK:     true,
			DryRun: true,
			Error:  "一期仅生成 URI；" + proto + " 真实部署尚未接入（需 xray/sing-box 配置合并）",
		}
	default:
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: "不支持的协议: " + req.Protocol,
		}
	}
}

// deployMieru writes a mita server config fragment and applies it via the mita CLI.
// Requires the mita server binary (not the mieru client).
func deployMieru(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	mitaPath := findMitaBinary()
	if mitaPath == "" {
		return wsproto.ProxyServiceApplyAck{
			OK:     false,
			DryRun: true,
			Error:  "节点未安装 mita（mieru 服务端）。请先安装 mita 后再发布，否则链接可导入但无服务监听",
		}
	}
	port := req.ListenPort
	if port <= 0 {
		port = proxysvc.ListenPortFromConfig(req.Config)
	}
	if port <= 0 || port > 65535 {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: fmt.Sprintf("无效监听端口: %d", port)}
	}

	var cfg proxysvc.MieruConfig
	if len(req.Config) > 0 {
		if err := json.Unmarshal(req.Config, &cfg); err != nil {
			return wsproto.ProxyServiceApplyAck{OK: false, Error: "解析 mieru 配置失败: " + err.Error()}
		}
	}
	if cfg.Username == "" || cfg.Password == "" {
		// Secrets should already be filled by panel EnsureSecrets; refuse empty.
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "mieru 用户名/密码为空，请重新发布"}
	}
	transports := cfg.Transports
	if len(transports) == 0 {
		transports = []string{"TCP", "UDP"}
	}

	serverCfg := map[string]any{
		"portBindings": mitaPortBindings(port, transports),
		"users": []map[string]string{
			{"name": cfg.Username, "password": cfg.Password},
		},
		"loggingLevel": "INFO",
	}

	dir := "/var/lib/nft/cores/mieru"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	raw, err := json.MarshalIndent(serverCfg, "", "  ")
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: err.Error()}
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "写入 mita 配置失败: " + err.Error()}
	}

	// apply merges into existing mita settings (multiple instances on one host OK).
	if out, err := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", cfgPath); err != nil {
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: fmt.Sprintf("mita apply config 失败: %v (%s)", err, truncateOut(out)),
		}
	}

	// Restart so new portBindings take effect (reload only covers users/logging).
	_, _ = runCmdTimeout(15*time.Second, mitaPath, "stop")
	if out, err := runCmdTimeout(20*time.Second, mitaPath, "start"); err != nil {
		// systemctl unit is often named mita — fall back if CLI start fails.
		if out2, err2 := runCmdTimeout(20*time.Second, "systemctl", "restart", "mita"); err2 != nil {
			return wsproto.ProxyServiceApplyAck{
				OK:    false,
				Error: fmt.Sprintf("mita start 失败: %v (%s); systemctl: %v (%s)", err, truncateOut(out), err2, truncateOut(out2)),
			}
		}
	}

	ver := probeCoreVersion(mitaPath)
	return wsproto.ProxyServiceApplyAck{
		OK:          true,
		DryRun:      false,
		CoreVersion: ver,
	}
}

func mitaPortBindings(port int, transports []string) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	for _, t := range transports {
		proto := strings.ToUpper(strings.TrimSpace(t))
		if proto == "" {
			continue
		}
		if seen[proto] {
			continue
		}
		seen[proto] = true
		out = append(out, map[string]any{"port": port, "protocol": proto})
	}
	if len(out) == 0 {
		out = []map[string]any{
			{"port": port, "protocol": "TCP"},
			{"port": port, "protocol": "UDP"},
		}
	}
	return out
}

// findMitaBinary returns the mita server binary path (not the mieru client).
func findMitaBinary() string {
	paths := []string{
		"/usr/local/bin/mita",
		"/usr/bin/mita",
		"/var/lib/nft/cores/mieru/mita",
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("mita"); err == nil {
		return p
	}
	return ""
}

func runCmdTimeout(d time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// mita often needs root; agent runs as root under systemd.
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timeout after %s", d)
	}
	return string(out), err
}

func truncateOut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}
