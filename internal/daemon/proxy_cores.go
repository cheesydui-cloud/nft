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
// mieru → mita; vless → xray; shadowsocks → sing-box. Missing core → error (not silent ready).
func handleProxyServiceApply(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	switch proto {
	case "mieru":
		return deployMieru(req)
	case "vless":
		return deployXrayVLESS(req)
	case "shadowsocks", "ss":
		return deploySingBoxSS(req)
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
			Error:  "节点未安装 mita（mieru 服务端）。请在面板「系统设置 → 代理核心缓存」下载 mita 后重新发布，或在节点本机安装 mita",
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

	// mita CLI talks to a local RPC daemon. Official flow is: start daemon → apply config.
	// Applying while the daemon is down yields:
	//   get mita server status failed ... rpc error ... connection error
	// which looks like "core missing" but the binary is present.
	if err := ensureMitaDaemon(mitaPath); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: err.Error()}
	}

	// apply merges into existing mita settings (multiple instances on one host OK).
	if out, err := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", cfgPath); err != nil {
		// Daemon may have died; one more start+apply attempt.
		_ = ensureMitaDaemon(mitaPath)
		if out2, err2 := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", cfgPath); err2 != nil {
			return wsproto.ProxyServiceApplyAck{
				OK: false,
				Error: fmt.Sprintf(
					"mita apply config 失败（二进制已找到，但守护进程 RPC 不可用或配置无效）: %v (%s); 重试: %v (%s)。"+
						"可在节点执行: systemctl status mita; %s start; journalctl -u mita -n 50",
					err, truncateOut(out), err2, truncateOut(out2), mitaPath),
			}
		}
	}

	// Restart so new portBindings take effect (reload only covers users/logging).
	_, _ = runCmdTimeout(15*time.Second, mitaPath, "stop")
	if err := ensureMitaDaemon(mitaPath); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "mita 重启失败: " + err.Error()}
	}

	ver := probeCoreVersion(mitaPath)
	return wsproto.ProxyServiceApplyAck{
		OK:          true,
		DryRun:      false,
		CoreVersion: ver,
	}
}

// ensureMitaDaemon starts the mita server process if RPC is down.
// Order: systemctl start mita → mita start → wait until status succeeds.
func ensureMitaDaemon(mitaPath string) error {
	// Already healthy?
	if out, err := runCmdTimeout(8*time.Second, mitaPath, "status"); err == nil {
		_ = out
		return nil
	}
	// Prefer packaged unit when present.
	_, _ = runCmdTimeout(20*time.Second, "systemctl", "start", "mita")
	_, _ = runCmdTimeout(20*time.Second, "systemctl", "enable", "mita")
	if out, err := runCmdTimeout(8*time.Second, mitaPath, "status"); err == nil {
		_ = out
		return nil
	}
	// CLI start (standalone / panel-pushed binary without unit).
	if out, err := runCmdTimeout(20*time.Second, mitaPath, "start"); err != nil {
		// Last try: systemctl restart
		out2, err2 := runCmdTimeout(20*time.Second, "systemctl", "restart", "mita")
		if err2 != nil {
			return fmt.Errorf(
				"无法启动 mita 守护进程（apply/status 需要本机 RPC）。"+
					"CLI start: %v (%s); systemctl: %v (%s)。"+
					"面板缓存的 mita 二进制在 /var/lib/nft/cores/mieru/mita，但官方安装通常还带 systemd unit；"+
					"可在节点: curl 安装脚本或 systemctl enable --now mita",
				err, truncateOut(out), err2, truncateOut(out2))
		}
	}
	// Wait briefly for socket.
	deadline := time.Now().Add(8 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, err := runCmdTimeout(5*time.Second, mitaPath, "status")
		if err == nil {
			return nil
		}
		last = truncateOut(out)
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("mita 已尝试启动但 status 仍失败: %s", last)
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
	if s == "" {
		return s
	}
	// xray prints a long version banner first; the real error is usually at the end.
	// Prefer the most informative tail so UI shows "Failed to start: …" not the banner.
	useful := extractUsefulCoreErr(s)
	if len(useful) > 480 {
		return useful[len(useful)-480:]
	}
	return useful
}

// extractUsefulCoreErr drops xray version banners and keeps failure lines.
func extractUsefulCoreErr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Prefer lines that look like actual failures.
	lines := strings.Split(s, "\n")
	var keep []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		// Skip pure version banner lines.
		if strings.HasPrefix(t, "Xray ") && strings.Contains(t, "Xray, Penetrates") {
			continue
		}
		if strings.Contains(low, "a unified platform for anti-censorship") {
			continue
		}
		if strings.Contains(low, "failed") ||
			strings.Contains(low, "invalid") ||
			strings.Contains(low, "error") ||
			strings.Contains(low, "panic") ||
			strings.Contains(low, "unsupported") ||
			strings.Contains(low, "empty ") ||
			strings.Contains(low, "please ") ||
			strings.Contains(low, "only supports") {
			keep = append(keep, t)
		}
	}
	if len(keep) > 0 {
		return strings.Join(keep, " | ")
	}
	// Fallback: last non-empty line(s), not the head (banner).
	var nonEmpty []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	if len(nonEmpty) == 0 {
		return s
	}
	if len(nonEmpty) == 1 {
		return nonEmpty[0]
	}
	// last up to 3 lines
	start := len(nonEmpty) - 3
	if start < 0 {
		start = 0
	}
	return strings.Join(nonEmpty[start:], " | ")
}
