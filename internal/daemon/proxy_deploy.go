package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"nft/internal/proxysvc"
	"nft/internal/wsproto"
)

// coreStateDir is where per-instance core configs/pid/logs live.
// Override with NFT_CORE_STATE_DIR (tests / non-standard layouts).
func coreStateDir() string {
	if d := strings.TrimSpace(os.Getenv("NFT_CORE_STATE_DIR")); d != "" {
		return d
	}
	return "/var/lib/nft/cores"
}

// deployXrayVLESS writes a per-instance xray config and (re)starts that instance process.
func deployXrayVLESS(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	// Prefer panel-managed core first. System packages under /usr often ship
	// older Xray that accepts config JSON but cannot complete vlessenc handshakes
	// (client timeout while encryption=none still works).
	panelXray := filepath.Join(coreStateDir(), "xray", "xray")
	xrayPath := findCoreBinary(
		[]string{"xray"},
		[]string{
			panelXray,
			"/var/lib/nft/cores/xray/xray",
			"/usr/local/bin/xray",
			"/usr/bin/xray",
			"/opt/xray/xray",
		},
	)
	if xrayPath == "" {
		return wsproto.ProxyServiceApplyAck{
			OK:     false,
			DryRun: true,
			Error:  "节点未安装 xray。请在面板「系统设置 → 代理核心缓存」下载 xray 后重新发布，或在节点本机安装 xray",
		}
	}
	// VLESS Encryption needs a recent Xray-core (vlessenc). If the selected binary
	// is too old, try the panel-managed path explicitly, then fail with a clear message.
	if proxysvc.NeedsVLESSEnc(req.Config) {
		if !xraySupportsVlessEnc(xrayPath) {
			if xrayPath != panelXray && xraySupportsVlessEnc(panelXray) {
				xrayPath = panelXray
			} else {
				return wsproto.ProxyServiceApplyAck{
					OK: false,
					Error: fmt.Sprintf(
						"节点 xray 不支持 vlessenc（当前 %s · %s）。请在面板「系统设置 → 代理核心缓存」拉取最新 xray，发布时勾选「强制推送核心」后重新发布",
						xrayPath, probeCoreVersion(xrayPath),
					),
				}
			}
		}
	}
	port := req.ListenPort
	if port <= 0 {
		port = proxysvc.ListenPortFromConfig(req.Config)
	}
	dir := filepath.Join(coreStateDir(), "xray")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	pidPath := filepath.Join(dir, fmt.Sprintf("instance-%d.pid", req.InstanceID))
	logPath := filepath.Join(dir, fmt.Sprintf("instance-%d.log", req.InstanceID))
	certPath := filepath.Join(dir, fmt.Sprintf("instance-%d.crt", req.InstanceID))
	keyPath := filepath.Join(dir, fmt.Sprintf("instance-%d.key", req.InstanceID))

	// TLS: write PEM to disk and inject cert_file/key_file for xray tlsSettings.
	buildCfg := req.Config
	if tlsCfg, ok := materializeTLSCerts(req.Config, certPath, keyPath); ok {
		buildCfg = tlsCfg
	} else {
		// Non-TLS or empty PEM: drop stale certs from a previous TLS deploy of this instance.
		_ = os.Remove(certPath)
		_ = os.Remove(keyPath)
	}

	// 3x-ui-style egress: share-protocol outbound, open SOCKS, and/or freedom redirect.
	var socks *proxysvc.OutboundSOCKS
	uri := strings.TrimSpace(req.OutboundSocks)
	share := strings.TrimSpace(req.OutboundShareURI)
	rh := strings.TrimSpace(req.OutboundRedirectHost)
	if share != "" || uri != "" || (rh != "" && req.OutboundRedirectPort > 0) {
		socks = &proxysvc.OutboundSOCKS{
			URI:          uri,
			ShareURI:     share,
			RedirectHost: rh,
			RedirectPort: req.OutboundRedirectPort,
		}
	}
	cfgBytes, err := proxysvc.BuildXrayVLESSConfigOpts(port, buildCfg, socks)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "生成 xray 配置失败: " + err.Error()}
	}
	if patched, perr := proxysvc.ApplyXrayEgressPolicy(cfgBytes, req.BlockEgressV4, req.BlockEgressV6); perr != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "应用出站协议栈失败: " + perr.Error()}
	} else {
		cfgBytes = patched
	}
	// Stats API on loopback so agent can poll inbound traffic.
	if apiPort := reuseOrPickStatsPort(dir, req.InstanceID); apiPort > 0 {
		if injected, ierr := proxysvc.InjectXrayStatsAPI(cfgBytes, apiPort); ierr == nil {
			cfgBytes = injected
			_ = writeStatsPort(dir, req.InstanceID, apiPort)
		} else {
			logStatsInjectOnce("xray inject: " + ierr.Error())
		}
	}
	// Validate JSON shape early.
	if !json.Valid(cfgBytes) {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "xray 配置不是合法 JSON"}
	}
	if sameCoreConfigFile(cfgPath, cfgBytes) && pidFileAlive(pidPath) {
		return wsproto.ProxyServiceApplyAck{
			OK:          true,
			DryRun:      false,
			CoreVersion: probeCoreVersion(xrayPath),
		}
	}
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "写入 xray 配置失败: " + err.Error()}
	}
	if out, err := runCmdTimeout(15*time.Second, xrayPath, "run", "-test", "-c", cfgPath); err != nil {
		// Older xray may use -config; try once more, then soft-skip test if binary rejects -test.
		if out2, err2 := runCmdTimeout(15*time.Second, xrayPath, "-test", "-config", cfgPath); err2 != nil {
			// If both fail with "unknown", still try start; otherwise surface test error.
			msg := truncateOut(out + " | " + string(out2))
			if looksLikeConfigError(out) || looksLikeConfigError(string(out2)) {
				return wsproto.ProxyServiceApplyAck{
					OK:    false,
					Error: fmt.Sprintf("xray 配置校验失败: %v (%s)", err, msg),
				}
			}
			_ = err2
		}
	}
	evictForeignListeners(port)
	if err := restartDetached(pidPath, logPath, xrayPath, "run", "-c", cfgPath); err != nil {
		// Fallback flag form.
		if err2 := restartDetached(pidPath, logPath, xrayPath, "-config", cfgPath); err2 != nil {
			return wsproto.ProxyServiceApplyAck{
				OK:    false,
				Error: fmt.Sprintf("启动 xray 失败: %v; 备选: %v", err, err2),
			}
		}
	}
	if err := waitListenHint(port, 8*time.Second); err != nil {
		tail, _ := os.ReadFile(logPath)
		stopPIDFile(pidPath)
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: fmt.Sprintf("xray 已启动但端口 %d 未监听: %v；日志: %s", port, err, truncateOut(string(tail))),
		}
	}
	return wsproto.ProxyServiceApplyAck{
		OK:          true,
		DryRun:      false,
		CoreVersion: probeCoreVersion(xrayPath),
	}
}

// deploySingBoxSS writes a per-instance sing-box config and (re)starts that instance.
func deploySingBoxSS(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	return deploySingBoxInbound(req, "ss", func(port int, cfg json.RawMessage) ([]byte, error) {
		return proxysvc.BuildSingBoxSSConfig(port, cfg)
	}, false)
}

// deploySingBoxSocks deploys a standard SOCKS5 inbound via sing-box.
func deploySingBoxSocks(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	return deploySingBoxInbound(req, "socks5", func(port int, cfg json.RawMessage) ([]byte, error) {
		return proxysvc.BuildSingBoxSocksConfig(port, cfg)
	}, false)
}

// deploySingBoxAnyTLS deploys anytls inbound (TLS cert required).
func deploySingBoxAnyTLS(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	return deploySingBoxInbound(req, "anytls", func(port int, cfg json.RawMessage) ([]byte, error) {
		return proxysvc.BuildSingBoxAnyTLSConfig(port, cfg)
	}, true)
}

// deploySingBoxNaive deploys naive inbound via sing-box (not Caddy original).
func deploySingBoxNaive(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	return deploySingBoxInbound(req, "naive", func(port int, cfg json.RawMessage) ([]byte, error) {
		return proxysvc.BuildSingBoxNaiveConfig(port, cfg)
	}, true)
}

// deploySingBoxInbound is the shared path for all sing-box-based proxy services.
// needTLS: write cert_pem/key_pem to disk and inject certificate_path/key_path.
func deploySingBoxInbound(req wsproto.ProxyServiceApply, label string, build func(port int, cfg json.RawMessage) ([]byte, error), needTLS bool) wsproto.ProxyServiceApplyAck {
	panelBox := filepath.Join(coreStateDir(), "sing-box", "sing-box")
	boxPath := findCoreBinary(
		[]string{"sing-box", "singbox"},
		[]string{
			panelBox,
			"/var/lib/nft/cores/sing-box/sing-box",
			"/usr/local/bin/sing-box",
			"/usr/bin/sing-box",
		},
	)
	if boxPath == "" {
		return wsproto.ProxyServiceApplyAck{
			OK:     false,
			DryRun: true,
			Error:  "节点未安装 sing-box。请在面板「系统设置 → 代理核心缓存」下载 sing-box 后重新发布，或在节点本机安装 sing-box",
		}
	}
	port := req.ListenPort
	if port <= 0 {
		port = proxysvc.ListenPortFromConfig(req.Config)
	}
	dir := filepath.Join(coreStateDir(), "sing-box")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	pidPath := filepath.Join(dir, fmt.Sprintf("instance-%d.pid", req.InstanceID))
	logPath := filepath.Join(dir, fmt.Sprintf("instance-%d.log", req.InstanceID))
	certPath := filepath.Join(dir, fmt.Sprintf("instance-%d.crt", req.InstanceID))
	keyPath := filepath.Join(dir, fmt.Sprintf("instance-%d.key", req.InstanceID))

	buildCfg := req.Config
	if needTLS {
		if tlsCfg, ok := materializeTLSCertsAny(req.Config, certPath, keyPath); ok {
			buildCfg = tlsCfg
		} else {
			_ = os.Remove(certPath)
			_ = os.Remove(keyPath)
		}
	} else {
		_ = os.Remove(certPath)
		_ = os.Remove(keyPath)
	}

	cfgBytes, err := build(port, buildCfg)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: fmt.Sprintf("生成 sing-box %s 配置失败: %v", label, err)}
	}
	// Rule-scoped egress (3x-ui style):
	//   OutboundShareURI → real protocol outbound (ss/socks)
	//   OutboundSocks → open SOCKS
	//   OutboundRedirect* → fixed dial to exit host:port
	share := strings.TrimSpace(req.OutboundShareURI)
	uri := strings.TrimSpace(req.OutboundSocks)
	rh := strings.TrimSpace(req.OutboundRedirectHost)
	if share != "" {
		patched, perr := proxysvc.InjectSingBoxShareOutbound(cfgBytes, share)
		if perr != nil {
			return wsproto.ProxyServiceApplyAck{OK: false, Error: "注入落地协议出站失败: " + perr.Error()}
		}
		cfgBytes = patched
	} else if uri != "" {
		patched, perr := proxysvc.InjectSingBoxSocksOutbound(cfgBytes, uri)
		if perr != nil {
			return wsproto.ProxyServiceApplyAck{OK: false, Error: "注入 SOCKS 出站失败: " + perr.Error()}
		}
		cfgBytes = patched
	} else if rh != "" && req.OutboundRedirectPort > 0 {
		patched, perr := proxysvc.InjectSingBoxRedirectOutbound(cfgBytes, rh, req.OutboundRedirectPort)
		if perr != nil {
			return wsproto.ProxyServiceApplyAck{OK: false, Error: "注入固定出口失败: " + perr.Error()}
		}
		cfgBytes = patched
	}
	if patched, perr := proxysvc.ApplySingBoxEgressPolicy(cfgBytes, req.BlockEgressV4, req.BlockEgressV6); perr != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "应用出站协议栈失败: " + perr.Error()}
	} else {
		cfgBytes = patched
	}
	// Clash API on loopback for agent traffic sampling.
	if apiPort := reuseOrPickStatsPort(dir, req.InstanceID); apiPort > 0 {
		if injected, ierr := proxysvc.InjectSingBoxClashAPI(cfgBytes, apiPort); ierr == nil {
			cfgBytes = injected
			_ = writeStatsPort(dir, req.InstanceID, apiPort)
		} else {
			logStatsInjectOnce("sing-box inject: " + ierr.Error())
		}
	}
	if sameCoreConfigFile(cfgPath, cfgBytes) && pidFileAlive(pidPath) {
		return wsproto.ProxyServiceApplyAck{
			OK:          true,
			DryRun:      false,
			CoreVersion: probeCoreVersion(boxPath),
		}
	}
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "写入 sing-box 配置失败: " + err.Error()}
	}
	if out, err := runCmdTimeout(15*time.Second, boxPath, "check", "-c", cfgPath); err != nil {
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: fmt.Sprintf("sing-box 配置校验失败: %v (%s)", err, truncateOut(out)),
		}
	}
	evictForeignListeners(port)
	if err := restartDetached(pidPath, logPath, boxPath, "run", "-c", cfgPath); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "启动 sing-box 失败: " + err.Error()}
	}
	if err := waitListenHint(port, 8*time.Second); err != nil {
		tail, _ := os.ReadFile(logPath)
		stopPIDFile(pidPath)
		return wsproto.ProxyServiceApplyAck{
			OK:    false,
			Error: fmt.Sprintf("sing-box 已启动但端口 %d 未监听: %v；日志: %s", port, err, truncateOut(string(tail))),
		}
	}
	return wsproto.ProxyServiceApplyAck{
		OK:          true,
		DryRun:      false,
		CoreVersion: probeCoreVersion(boxPath),
	}
}

func looksLikeConfigError(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "failed") ||
		strings.Contains(s, "invalid") ||
		strings.Contains(s, "error") ||
		strings.Contains(s, "panic") ||
		strings.Contains(s, "unsupported") ||
		strings.Contains(s, "only supports") ||
		strings.Contains(s, "empty ")
}

// stopPIDFile kills a previously started core process if the pid file is still live.
// Also drops the durable runspec so the watchdog will not revive a deliberately
// stopped instance (failed listen / undeploy / replace).
func stopPIDFile(pidPath string) {
	removeRunSpec(pidPath)
	b, err := os.ReadFile(pidPath)
	if err != nil {
		// Still drop TLS material next to a vanished pid (undeploy after crash).
		removeTLSFilesBeside(pidPath)
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		_ = os.Remove(pidPath)
		removeTLSFilesBeside(pidPath)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		removeTLSFilesBeside(pidPath)
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidPath)
	removeTLSFilesBeside(pidPath)
}

// removeTLSFilesBeside deletes instance-N.crt/.key next to instance-N.pid.
// Also drops the statsport file so a dead instance is not polled.
func removeTLSFilesBeside(pidPath string) {
	base := strings.TrimSuffix(pidPath, ".pid")
	if base == pidPath {
		return
	}
	_ = os.Remove(base + ".crt")
	_ = os.Remove(base + ".key")
	_ = os.Remove(base + ".statsport")
	_ = os.Remove(base + ".statsuser")
}

// materializeTLSCerts writes cert_pem/key_pem to certPath/keyPath when security=tls.
// Returns a rewritten config JSON with cert_file/key_file set (and PEM left as-is for DB).
// ok=false means not TLS or incomplete PEM — caller should remove stale cert files.
func materializeTLSCerts(raw json.RawMessage, certPath, keyPath string) (json.RawMessage, bool) {
	var c proxysvc.VLESSConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return raw, false
	}
	if proxysvc.NormalizeSecurity(c.Security) != "tls" {
		return raw, false
	}
	return materializeTLSCertsAny(raw, certPath, keyPath)
}

// materializeTLSCertsAny writes cert_pem/key_pem when present (anytls/naive/vless-tls).
// Injects cert_file/key_file paths for builders.
func materializeTLSCertsAny(raw json.RawMessage, certPath, keyPath string) (json.RawMessage, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, false
	}
	certPEM, _ := m["cert_pem"].(string)
	keyPEM, _ := m["key_pem"].(string)
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" || keyPEM == "" {
		return raw, false
	}
	if err := os.WriteFile(certPath, []byte(certPEM+"\n"), 0o600); err != nil {
		return raw, false
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM+"\n"), 0o600); err != nil {
		_ = os.Remove(certPath)
		return raw, false
	}
	m["cert_file"] = certPath
	m["key_file"] = keyPath
	out, err := json.Marshal(m)
	if err != nil {
		return raw, false
	}
	return out, true
}

// sameCoreConfigFile is true when path already holds the same JSON
// (whitespace-insensitive). Used to skip restarting a live core.
func sameCoreConfigFile(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var a, b any
	if json.Unmarshal(have, &a) != nil || json.Unmarshal(want, &b) != nil {
		return string(have) == string(want)
	}
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

// reuseOrPickStatsPort keeps the previous loopback stats/Clash port so an
// unchanged republish does not rewrite the core config (and bounce the process).
func reuseOrPickStatsPort(dir string, instanceID int64) int {
	if p := readStatsPort(dir, instanceID); p > 0 {
		return p
	}
	p, err := pickLoopbackPort()
	if err != nil {
		return 0
	}
	return p
}

// restartDetached stops any prior instance pid, then starts name args... in
// background and registers a durable runspec so the core-watchdog can restart
// it if the process dies or the agent itself restarts.
func restartDetached(pidPath, logPath, name string, args ...string) error {
	if err := restartDetachedTracked(pidPath, logPath, name, args...); err != nil {
		return err
	}
	writeRunSpec(pidPath, logPath, name, args)
	return nil
}

// restartDetachedTracked is the raw start path used by both deploy and the
// watchdog. It does not write/register the runspec (caller owns that) so a
// failed start cannot leave a half-registered recipe that thrash-restarts.
func restartDetachedTracked(pidPath, logPath, name string, args ...string) error {
	// Kill old pid without dropping runspec when the watchdog is refreshing —
	// only stopPIDFile drops the runspec. Here we only signal the old pid.
	killPIDFileOnly(pidPath)
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Detach from agent so core survives agent restart; agent still tracks pid for redeploy.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		return err
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644)
	// Release process so we don't wait; close log fd in parent after start.
	_ = cmd.Process.Release()
	_ = logF.Close()
	// Brief settle: if process dies immediately, surface log tail.
	time.Sleep(800 * time.Millisecond)
	if !pidAlive(pid) {
		tail, _ := os.ReadFile(logPath)
		return fmt.Errorf("process exited immediately (pid %d): %s", pid, truncateOut(string(tail)))
	}
	return nil
}

// killPIDFileOnly signals the process in pidPath without removing the runspec.
// Used when replacing a live process during restart so the watchdog stays armed.
func killPIDFileOnly(pidPath string) {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(pidPath)
}

func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// waitListenHint best-effort: try to see if something is bound (ss/lsof optional).
func waitListenHint(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if portLikelyListening(port) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("port %d not observed listening within %s", port, d)
}

// evictForeignListeners stops third-party / leftover cores occupying port so
// a panel publish can bind. Panel-managed instance-*.pid processes are kept
// (restartDetached replaces those). Typical leftovers: 3x-ui xray, official
// sing-box unit, another mita, nginx on 443, or a crashed orphan.
func evictForeignListeners(port int) {
	if port < 1 || port > 65535 {
		return
	}
	keep := panelManagedListenPIDs()
	// Competing systemd proxy units (not nft-managed mita).
	for _, unit := range []string{
		"xray", "xray.service",
		"sing-box", "sing-box.service", "singbox",
		"v2ray", "v2ray.service",
		"x-ui", "x-ui.service", "3x-ui", "3x-ui.service",
	} {
		if !unitActive(strings.TrimSuffix(unit, ".service")) && !unitActive(unit) {
			continue
		}
		name := strings.TrimSuffix(unit, ".service")
		_, _ = runCmdTimeout(8*time.Second, "systemctl", "stop", name)
	}
	for _, pid := range pidsListeningOnPort(port) {
		if pid <= 1 {
			continue
		}
		if keep[pid] {
			continue
		}
		// Never kill the agent itself.
		if pid == os.Getpid() {
			continue
		}
		log.Printf("proxy: evicting pid %d from port %d for panel publish", pid, port)
		killPID(pid)
	}
}

func panelManagedListenPIDs() map[int]bool {
	out := map[int]bool{}
	base := coreStateDir()
	for _, sub := range []string{"xray", "sing-box", "mieru"} {
		matches, _ := filepath.Glob(filepath.Join(base, sub, "instance-*.pid"))
		for _, p := range matches {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil || n <= 1 {
				continue
			}
			out[n] = true
		}
	}
	return out
}

func parseSSListenPIDs(ssOut string, port int) []int {
	needle := ":" + strconv.Itoa(port)
	seen := map[int]bool{}
	var pids []int
	for _, line := range strings.Split(ssOut, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		// Avoid matching :443 inside :4430 — require port at addr end or before space.
		if !ssLineHasPort(line, port) {
			continue
		}
		for _, tok := range strings.Split(line, "pid=") {
			if tok == line {
				continue
			}
			num := tok
			if i := strings.IndexAny(num, ",)"); i >= 0 {
				num = num[:i]
			}
			n, err := strconv.Atoi(strings.TrimSpace(num))
			if err != nil || n <= 1 || seen[n] {
				continue
			}
			seen[n] = true
			pids = append(pids, n)
		}
	}
	return pids
}

func ssLineHasPort(line string, port int) bool {
	// ss columns look like 0.0.0.0:8388 or [::]:8388 or *:8388
	want := ":" + strconv.Itoa(port)
	for _, f := range strings.Fields(line) {
		if strings.HasSuffix(f, want) {
			return true
		}
		// [::1]:8388 already covered by HasSuffix
	}
	return false
}

func pidsListeningOnPort(port int) []int {
	seen := map[int]bool{}
	var pids []int
	add := func(n int) {
		if n <= 1 || seen[n] {
			return
		}
		seen[n] = true
		pids = append(pids, n)
	}
	// ss -lntp / -lnup : users:("xray",pid=123,fd=8)
	for _, args := range [][]string{
		{"-lntp"},
		{"-lnup"},
	} {
		out, err := runCmdTimeout(3*time.Second, "ss", args...)
		if err != nil {
			continue
		}
		for _, n := range parseSSListenPIDs(out, port) {
			add(n)
		}
	}
	out, err := runCmdTimeout(3*time.Second, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			n, err := strconv.Atoi(strings.TrimSpace(line))
			if err == nil {
				add(n)
			}
		}
	}
	out, err = runCmdTimeout(3*time.Second, "fuser", fmt.Sprintf("%d/tcp", port))
	if err == nil {
		for _, f := range strings.Fields(out) {
			n, err := strconv.Atoi(f)
			if err == nil {
				add(n)
			}
		}
	}
	return pids
}

func killPID(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
}

// resetMitaStoreForPanelReplace stops the proxy, wipes on-disk mita settings
// that `apply` would otherwise merge with, then brings the RPC daemon back.
// Official apply cannot delete users/ports; wiping the store is the only
// replace. Panel desired.json is applied immediately after this returns.
func resetMitaStoreForPanelReplace(mitaPath string) error {
	if strings.TrimSpace(mitaPath) == "" {
		mitaPath = resolveMitaBinary()
	}
	if mitaPath != "" {
		mitaProxyStop(mitaPath)
	}
	_, _ = runCmdTimeout(15*time.Second, "systemctl", "stop", "mita")
	// Common official / nft-managed store locations. Metrics stay (traffic
	// history); server.json / config blobs hold leftover users+ports.
	globs := []string{
		"/etc/mita/*",
		"/var/lib/mita/*",
		"/var/lib/mita/store/*",
		"/var/lib/mita/config/*",
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, p := range matches {
			base := filepath.Base(p)
			low := strings.ToLower(base)
			if strings.Contains(low, "metrics") {
				continue
			}
			if strings.HasSuffix(low, ".pb") || strings.HasSuffix(low, ".json") ||
				strings.Contains(low, "server") || strings.Contains(low, "config") ||
				strings.Contains(low, "setting") || strings.Contains(low, "store") {
				_ = os.Remove(p)
			}
		}
	}
	// Recreate empty dirs with correct owner so `mita run` can start.
	if err := ensureMitaRuntimeDirs(); err != nil {
		return err
	}
	if mitaPath != "" {
		return ensureMitaDaemon(mitaPath)
	}
	return nil
}

func portLikelyListening(port int) bool {
	// Prefer actual dial — works for both IPv4-only and IPv6-only listeners.
	for _, host := range []string{"127.0.0.1", "::1"} {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 400*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	// Fallback: ss / lsof when dial fails (e.g. firewall on loopback — rare).
	out, err := runCmdTimeout(2*time.Second, "ss", "-lntu")
	if err == nil && strings.Contains(out, ":"+strconv.Itoa(port)) {
		return true
	}
	out, err = runCmdTimeout(2*time.Second, "lsof", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
	if err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	return false
}
