package daemon

import (
	"encoding/json"
	"fmt"
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

	var socks *proxysvc.OutboundSOCKS
	if uri := strings.TrimSpace(req.OutboundSocks); uri != "" {
		socks = &proxysvc.OutboundSOCKS{
			URI:          uri,
			RedirectHost: strings.TrimSpace(req.OutboundRedirectHost),
			RedirectPort: req.OutboundRedirectPort,
		}
	}
	cfgBytes, err := proxysvc.BuildXrayVLESSConfigOpts(port, buildCfg, socks)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "生成 xray 配置失败: " + err.Error()}
	}
	// Stats API on loopback so agent can poll inbound traffic.
	if apiPort, perr := pickLoopbackPort(); perr == nil {
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
	// Rule-scoped SK5 exit: open SOCKS outbound (client destinations pass through).
	if uri := strings.TrimSpace(req.OutboundSocks); uri != "" {
		patched, perr := proxysvc.InjectSingBoxSocksOutbound(cfgBytes, uri)
		if perr != nil {
			return wsproto.ProxyServiceApplyAck{OK: false, Error: "注入 SOCKS 出站失败: " + perr.Error()}
		}
		cfgBytes = patched
	}
	// Clash API on loopback for agent traffic sampling.
	if apiPort, perr := pickLoopbackPort(); perr == nil {
		if injected, ierr := proxysvc.InjectSingBoxClashAPI(cfgBytes, apiPort); ierr == nil {
			cfgBytes = injected
			_ = writeStatsPort(dir, req.InstanceID, apiPort)
		} else {
			logStatsInjectOnce("sing-box inject: " + ierr.Error())
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
