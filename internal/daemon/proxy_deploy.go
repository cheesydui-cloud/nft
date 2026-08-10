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
	xrayPath := findCoreBinary(
		[]string{"xray"},
		[]string{
			"/usr/local/bin/xray",
			"/usr/bin/xray",
			"/opt/xray/xray",
			"/var/lib/nft/cores/xray/xray",
		},
	)
	if xrayPath == "" {
		return wsproto.ProxyServiceApplyAck{
			OK:     false,
			DryRun: true,
			Error:  "节点未安装 xray。请在面板「系统设置 → 代理核心缓存」下载 xray 后重新发布，或在节点本机安装 xray",
		}
	}
	port := req.ListenPort
	if port <= 0 {
		port = proxysvc.ListenPortFromConfig(req.Config)
	}
	cfgBytes, err := proxysvc.BuildXrayVLESSConfig(port, req.Config)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "生成 xray 配置失败: " + err.Error()}
	}
	// Validate JSON shape early.
	if !json.Valid(cfgBytes) {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "xray 配置不是合法 JSON"}
	}
	// Prefer xray run -test when available.
	dir := filepath.Join(coreStateDir(), "xray")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	pidPath := filepath.Join(dir, fmt.Sprintf("instance-%d.pid", req.InstanceID))
	logPath := filepath.Join(dir, fmt.Sprintf("instance-%d.log", req.InstanceID))
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
	boxPath := findCoreBinary(
		[]string{"sing-box", "singbox"},
		[]string{
			"/usr/local/bin/sing-box",
			"/usr/bin/sing-box",
			"/var/lib/nft/cores/sing-box/sing-box",
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
	cfgBytes, err := proxysvc.BuildSingBoxSSConfig(port, req.Config)
	if err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "生成 sing-box 配置失败: " + err.Error()}
	}
	dir := filepath.Join(coreStateDir(), "sing-box")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wsproto.ProxyServiceApplyAck{OK: false, Error: "创建配置目录失败: " + err.Error()}
	}
	cfgPath := filepath.Join(dir, fmt.Sprintf("instance-%d.json", req.InstanceID))
	pidPath := filepath.Join(dir, fmt.Sprintf("instance-%d.pid", req.InstanceID))
	logPath := filepath.Join(dir, fmt.Sprintf("instance-%d.log", req.InstanceID))
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
func stopPIDFile(pidPath string) {
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

// restartDetached stops any prior instance pid, then starts name args... in background.
func restartDetached(pidPath, logPath, name string, args ...string) error {
	stopPIDFile(pidPath)
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
