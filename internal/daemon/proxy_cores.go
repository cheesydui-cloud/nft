package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
			// Panel-managed binary first so hello/version matches deploy selection.
			paths: []string{
				"/var/lib/nft/cores/xray/xray",
				"/usr/local/bin/xray",
				"/usr/bin/xray",
				"/opt/xray/xray",
			},
		},
		{
			name: "sing-box",
			bins: []string{"sing-box", "singbox"},
			paths: []string{
				"/var/lib/nft/cores/sing-box/sing-box",
				"/usr/local/bin/sing-box",
				"/usr/bin/sing-box",
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

// xraySupportsVlessEnc reports whether the binary understands `xray vlessenc`
// (VLESS Encryption). Older packages accept decryption=none but fail handshakes
// when decryption is mlkem768x25519plus… — clients see connect timeout.
func xraySupportsVlessEnc(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	out, err := runCmdTimeout(8*time.Second, path, "vlessenc")
	if err != nil {
		// Some builds print help to stdout and exit non-zero; still require material.
		if !strings.Contains(strings.ToLower(out), "encryption") &&
			!strings.Contains(strings.ToLower(out), "decryption") &&
			!strings.Contains(strings.ToLower(out), "mlkem") {
			return false
		}
	}
	low := strings.ToLower(out)
	return strings.Contains(low, "encryption") ||
		strings.Contains(low, "decryption") ||
		strings.Contains(low, "mlkem768")
}

// handleProxyServiceApply deploys one proxy-service instance on this host.
// mieru → mita; vless → xray; ss/socks5/anytls/naive → sing-box.
func handleProxyServiceApply(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	// Same VPS: panel publish owns the listen port. Foreign xray/sing-box/
	// 3x-ui / leftover mita must yield so the new inbound actually binds
	// and old share links on that port die.
	if p := req.ListenPort; p > 0 {
		evictForeignListeners(p)
	} else if p := proxysvc.ListenPortFromConfig(req.Config); p > 0 {
		evictForeignListeners(p)
	}
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	var ack wsproto.ProxyServiceApplyAck
	switch proto {
	case "mieru":
		ack = deployMieru(req)
	case "vless":
		ack = deployXrayVLESS(req)
	case "shadowsocks", "ss":
		ack = deploySingBoxSS(req)
	case "socks5", "socks":
		ack = deploySingBoxSocks(req)
	case "anytls":
		ack = deploySingBoxAnyTLS(req)
	case "naive", "naiveproxy":
		ack = deploySingBoxNaive(req)
	default:
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: "不支持的协议: " + req.Protocol,
		}
	}
	if ack.OK {
		syncProxyFirewallPorts()
	}
	return ack
}

// deployMieru writes a mita server config fragment and applies it via the mita CLI.
// Requires the mita server binary (not the mieru client) plus a running `mita run`
// management daemon (installed automatically from a bare panel-pushed binary).
func deployMieru(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	port := req.ListenPort
	if port <= 0 {
		port = proxysvc.ListenPortFromConfig(req.Config)
	}
	// Official mita: portBindings.port must be 1025–65535. 443 used to
	// look "ready" in the panel then fail at `mita apply` / stay silent
	// on older agents that never checked.
	if port < 1025 || port > 65535 {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: fmt.Sprintf("mieru 监听端口须为 1025–65535（当前 %d）。官方 mita 拒绝 443 等特权端口", port)}
	}

	mitaPath := findMitaBinary()
	if mitaPath == "" {
		return wsproto.ProxyServiceApplyAck{
			OK:     false,
			DryRun: true,
			Error:  "节点未安装 mita（mieru 服务端）。请在面板「系统设置 → 代理核心缓存」下载 mita 后重新发布，或在节点本机安装 mita",
		}
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
		transports = []string{"TCP"}
	}

	serverCfg := map[string]any{
		"portBindings": mitaPortBindings(port, transports),
		"users": []map[string]string{
			{"name": cfg.Username, "password": cfg.Password},
		},
		"loggingLevel": "INFO",
	}
	if ds := proxysvc.EgressMitaDualStack(req.BlockEgressV4, req.BlockEgressV6); ds != "" {
		serverCfg["dns"] = map[string]any{"dualStack": ds}
	}

	dir := filepath.Join(coreStateDir(), "mieru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	raw, err := json.MarshalIndent(serverCfg, "", "  ")
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: err.Error()}
	}
	cfgUnchanged := sameCoreConfigFile(cfgPath, raw)
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "写入 mita 配置失败: " + err.Error()}
	}
	// mita apply merges a single fragment into daemon state. Applying one
	// instance file leaves other instances' users/ports to chance (and
	// same username last-write-wins). Desired state is the union of every
	// instance-*.json on this host.
	unionRaw, err := mergeMitaInstanceConfigs(dir)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "合并 mita 实例配置失败: " + err.Error()}
	}
	unionPath := filepath.Join(dir, "desired.json")
	unionUnchanged := sameCoreConfigFile(unionPath, unionRaw)
	if err := os.WriteFile(unionPath, unionRaw, 0o600); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "写入 mita 合并配置失败: " + err.Error()}
	}
	cfgUnchanged = cfgUnchanged && unionUnchanged
	raw = unionRaw
	cfgPath = unionPath
	// Username key for `mita get users` traffic sampling (per-instance).
	_ = writeStatsUser(dir, req.InstanceID, cfg.Username)
	// mita process runs as user mita; make instance + desired JSON readable.
	instPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	_, _ = runCmdTimeout(5*time.Second, "chown", "mita:mita", instPath, cfgPath)
	_ = os.Chmod(instPath, 0o640)
	_ = os.Chmod(cfgPath, 0o640)

	// After prepare, always talk to the system binary.
	if p := resolveMitaBinary(); p != "" {
		mitaPath = p
	}

	// Leftover official users/ports survive `apply` (merge). If the live
	// daemon still has accounts the panel does not own, force replace.
	needReplace := mitaHasForeignUsers(mitaPath, unionUserNames(unionRaw))

	// Unchanged republish must not touch a live daemon — unless foreign
	// leftovers are still serving (old share links would stay valid).
	if cfgUnchanged && !needReplace && mitaProxyListening(mitaPath) {
		return wsproto.ProxyServiceApplyAck{
			OK:          true,
			DryRun:      false,
			CoreVersion: probeCoreVersion(mitaPath),
		}
	}

	// Official order: mita run (daemon/RPC) → apply config → mita start (listen).
	// prepare installs /usr/local/bin/mita + user/dirs/unit (bare cache path is not enough).
	if err := ensureMitaDaemon(mitaPath); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: err.Error()}
	}
	if p := resolveMitaBinary(); p != "" {
		mitaPath = p
	}

	// Config file unchanged and no leftovers: only make sure listen is up.
	if cfgUnchanged && !needReplace {
		if mitaProxyListening(mitaPath) {
			return wsproto.ProxyServiceApplyAck{
				OK:          true,
				DryRun:      false,
				CoreVersion: probeCoreVersion(mitaPath),
			}
		}
		if err := mitaProxyStart(mitaPath); err != nil {
			return wsproto.ProxyServiceApplyAck{
				OK:    false,
				Error: "mita 配置未变但未能进入监听: " + err.Error() + "; " + diagnoseMitaStart(mitaPath),
			}
		}
		return wsproto.ProxyServiceApplyAck{
			OK:          true,
			DryRun:      false,
			CoreVersion: probeCoreVersion(mitaPath),
		}
	}

	// Official `mita apply` MERGES. Leftover users/ports from a previous
	// official install stay valid unless we wipe store then apply the
	// panel union as the first (only) config.
	if err := resetMitaStoreForPanelReplace(mitaPath); err != nil {
		log.Printf("mita: reset store before apply: %v", err)
	}

	// apply the union of all local instance fragments (desired state).
	if out, err := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", cfgPath); err != nil {
		// Daemon may have died; one more prepare+apply attempt.
		_ = ensureMitaDaemon(mitaPath)
		if p := resolveMitaBinary(); p != "" {
			mitaPath = p
		}
		if out2, err2 := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", cfgPath); err2 != nil {
			return wsproto.ProxyServiceApplyAck{
				OK: false,
				Error: fmt.Sprintf(
					"mita apply config 失败: %v (%s); 重试: %v (%s)。%s journal: %s",
					err, truncateOut(out), err2, truncateOut(out2),
					diagnoseMitaStart(mitaPath), truncateOut(mitaJournal())),
			}
		}
	}

	// portBindings / non-user changes need stop → start (proxy listen), daemon stays up.
	mitaProxyStop(mitaPath)
	if err := mitaProxyStart(mitaPath); err != nil {
		// Daemon may have flapped; re-ensure run daemon then start listen.
		_ = ensureMitaDaemon(mitaPath)
		if p := resolveMitaBinary(); p != "" {
			mitaPath = p
		}
		if err2 := mitaProxyStart(mitaPath); err2 != nil {
			return wsproto.ProxyServiceApplyAck{
				OK:    false,
				Error: "mita 应用配置后无法进入监听: " + err2.Error() + "; " + diagnoseMitaStart(mitaPath),
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
		}
	}
	return out
}

// findMitaBinary returns the mita server binary path (not the mieru client).
func findMitaBinary() string {
	return resolveMitaBinary()
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

// handleProxyServiceStop stops a core instance previously started by apply.
// Used for rule-scoped VLESS+SK5 planes (instance id = synthetic rule id space).
func handleProxyServiceStop(req wsproto.ProxyServiceStop) wsproto.ProxyServiceStopAck {
	proto := strings.ToLower(strings.TrimSpace(req.Protocol))
	core := strings.ToLower(strings.TrimSpace(req.Core))
	if req.InstanceID <= 0 {
		return wsproto.ProxyServiceStopAck{OK: false, Error: "instance_id 无效"}
	}
	// Map protocol/core → state subdir (same layout as deploy).
	dirName := ""
	switch {
	case proto == "vless" || core == "xray":
		dirName = "xray"
	case proto == "mieru" || core == "mieru":
		dirName = "mieru"
	default:
		// ss/socks5/anytls/naive → sing-box
		dirName = "sing-box"
	}
	pidPath := filepath.Join(coreStateDir(), dirName, fmt.Sprintf("instance-%d.pid", req.InstanceID))
	cfgPath := filepath.Join(coreStateDir(), dirName, fmt.Sprintf("instance-%d.json", req.InstanceID))
	logPath := filepath.Join(coreStateDir(), dirName, fmt.Sprintf("instance-%d.log", req.InstanceID))
	stopPIDFile(pidPath)
	_ = os.Remove(cfgPath)
	_ = os.Remove(logPath)
	if dirName == "mieru" {
		// Drop this instance from mita by re-applying the remaining union.
		// A lone `mita apply` of one fragment cannot delete users/ports.
		reapplyMitaAfterInstanceRemoved(filepath.Join(coreStateDir(), "mieru"))
	}
	syncProxyFirewallPorts()
	return wsproto.ProxyServiceStopAck{OK: true}
}

// mergeMitaInstanceConfigs unions every instance-*.json under dir into one
// mita server config (portBindings + users). Same username keeps the last
// password seen (deterministic by filename sort).
func unionUserNames(unionRaw []byte) map[string]bool {
	var peek struct {
		Users []map[string]string `json:"users"`
	}
	_ = json.Unmarshal(unionRaw, &peek)
	out := map[string]bool{}
	for _, u := range peek.Users {
		if n := strings.TrimSpace(u["name"]); n != "" {
			out[n] = true
		}
	}
	return out
}

// mitaHasForeignUsers is true when `mita get users` lists an account that
// is not in the panel union (leftover official / other-panel install).
func mitaHasForeignUsers(mitaPath string, want map[string]bool) bool {
	if mitaPath == "" || len(want) == 0 {
		return false
	}
	out, err := runCmdTimeout(6*time.Second, mitaPath, "get", "users")
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	live := parseMitaUsersTable(out)
	if len(live) == 0 {
		// describe config as fallback (some builds print users there).
		desc, err := runCmdTimeout(6*time.Second, mitaPath, "describe", "config")
		if err != nil {
			return false
		}
		return mitaDescribeHasForeignUser(desc, want)
	}
	for name := range live {
		if !want[name] {
			return true
		}
	}
	return false
}

func mitaDescribeHasForeignUser(desc string, want map[string]bool) bool {
	// Heuristic: lines like `name: olduser` / `"name": "olduser"`.
	for _, line := range strings.Split(desc, "\n") {
		s := strings.TrimSpace(line)
		low := strings.ToLower(s)
		if !strings.Contains(low, "name") {
			continue
		}
		// skip hostnames / file names
		if strings.Contains(low, "server") || strings.Contains(low, "file") {
			continue
		}
		for _, sep := range []string{`": "`, `":"`, ": "} {
			if i := strings.Index(s, sep); i >= 0 {
				name := strings.Trim(s[i+len(sep):], `" ,`)
				name = strings.TrimSpace(name)
				if name != "" && !want[name] && !strings.Contains(name, ".") && !strings.Contains(name, "/") {
					// only treat as user if it looks like an account token
					if len(name) >= 2 && len(name) <= 64 {
						return true
					}
				}
			}
		}
	}
	return false
}

func mergeMitaInstanceConfigs(dir string) ([]byte, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "instance-*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	type frag struct {
		PortBindings []map[string]any    `json:"portBindings"`
		Users        []map[string]string `json:"users"`
		LoggingLevel string              `json:"loggingLevel"`
		DNS          map[string]any      `json:"dns,omitempty"`
	}
	var ports []map[string]any
	seenPort := map[string]bool{}
	users := map[string]string{}
	userOrder := []string{}
	logging := "INFO"
	var dns map[string]any
	for _, p := range matches {
		base := filepath.Base(p)
		if !strings.HasPrefix(base, "instance-") || !strings.HasSuffix(base, ".json") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var f frag
		if err := json.Unmarshal(b, &f); err != nil {
			continue
		}
		for _, pb := range f.PortBindings {
			key := fmt.Sprintf("%v/%v", pb["port"], pb["protocol"])
			if seenPort[key] {
				continue
			}
			seenPort[key] = true
			ports = append(ports, pb)
		}
		for _, u := range f.Users {
			name := strings.TrimSpace(u["name"])
			if name == "" {
				continue
			}
			if _, ok := users[name]; !ok {
				userOrder = append(userOrder, name)
			}
			users[name] = u["password"]
		}
		if strings.TrimSpace(f.LoggingLevel) != "" {
			logging = f.LoggingLevel
		}
		if len(f.DNS) > 0 {
			dns = f.DNS
		}
	}
	outUsers := make([]map[string]string, 0, len(userOrder))
	for _, name := range userOrder {
		outUsers = append(outUsers, map[string]string{"name": name, "password": users[name]})
	}
	if len(ports) == 0 {
		ports = []map[string]any{}
	}
	serverCfg := map[string]any{
		"portBindings": ports,
		"users":        outUsers,
		"loggingLevel": logging,
	}
	if len(dns) > 0 {
		serverCfg["dns"] = dns
	}
	return json.MarshalIndent(serverCfg, "", "  ")
}

func reapplyMitaAfterInstanceRemoved(dir string) {
	unionRaw, err := mergeMitaInstanceConfigs(dir)
	if err != nil {
		return
	}
	var peek struct {
		Users        []map[string]string `json:"users"`
		PortBindings []map[string]any    `json:"portBindings"`
	}
	_ = json.Unmarshal(unionRaw, &peek)
	mitaPath := findMitaBinary()
	if mitaPath == "" {
		return
	}
	if len(peek.Users) == 0 && len(peek.PortBindings) == 0 {
		_ = os.Remove(filepath.Join(dir, "desired.json"))
		mitaProxyStop(mitaPath)
		_ = resetMitaStoreForPanelReplace(mitaPath)
		return
	}
	unionPath := filepath.Join(dir, "desired.json")
	if err := os.WriteFile(unionPath, unionRaw, 0o600); err != nil {
		return
	}
	_, _ = runCmdTimeout(5*time.Second, "chown", "mita:mita", unionPath)
	_ = os.Chmod(unionPath, 0o640)
	if err := resetMitaStoreForPanelReplace(mitaPath); err != nil {
		log.Printf("mita: reset store after instance removed: %v", err)
	}
	if err := ensureMitaDaemon(mitaPath); err != nil {
		return
	}
	if p := resolveMitaBinary(); p != "" {
		mitaPath = p
	}
	if _, err := runCmdTimeout(20*time.Second, mitaPath, "apply", "config", unionPath); err != nil {
		return
	}
	mitaProxyStop(mitaPath)
	_ = mitaProxyStart(mitaPath)
}
