package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Official mita (mieru server) is a two-process design:
//
//	mita run    — long-running management daemon (gRPC under /var/run/mita)
//	mita start  — RPC: begin proxy listen (requires run first)
//	mita apply  — RPC: merge server JSON config
//	mita status — RPC: IDLE / RUNNING / …
//
// Deb/rpm postinst creates user mita, dirs /etc/mita /var/lib/mita /var/run/mita,
// and a systemd unit with ExecStart=/usr/bin/mita run as User=mita.
//
// Panel core-cache used to only drop a bare binary under /var/lib/nft/cores/mieru/mita.
// That path is typically root-owned 0700 → User=mita cannot exec → unit exits immediately
// with "control process exited" / RPC connection errors.
//
// Fix: always install a world-reachable binary at /usr/local/bin/mita, prepare runtime
// like the official package, and only then start `mita run` via systemd.

const (
	mitaUnitPath     = "/etc/systemd/system/mita.service"
	mitaUnitMarker   = "nft-managed"
	mitaRuntimeUser  = "mita"
	mitaSystemBinary = "/usr/local/bin/mita"
)

// prepareMitaRuntime creates the mita system user, runtime directories, a reachable
// binary at /usr/local/bin/mita, and a systemd unit that runs `mita run`.
func prepareMitaRuntime(mitaPath string) (string, error) {
	src := strings.TrimSpace(mitaPath)
	if src == "" {
		return "", fmt.Errorf("mita 路径为空")
	}
	if st, err := os.Stat(src); err != nil || st.IsDir() {
		return "", fmt.Errorf("mita 二进制不可用: %s", src)
	}

	// Canonical install path that User=mita can always exec (unlike /var/lib/nft/*).
	canon, err := installMitaSystemBinary(src)
	if err != nil {
		return "", err
	}
	if err := ensureMitaSystemUser(); err != nil {
		return "", err
	}
	if err := ensureMitaRuntimeDirs(); err != nil {
		return "", err
	}
	if err := ensureMitaSystemdUnit(canon); err != nil {
		return "", err
	}
	return canon, nil
}

// installMitaSystemBinary copies/links src to /usr/local/bin/mita (0755) so the
// unprivileged mita user can execute it. Also keeps/updates the panel cache path.
func installMitaSystemBinary(src string) (string, error) {
	src, _ = filepath.Abs(src)
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("读取 mita 二进制失败: %w", err)
	}
	if len(data) < 64 {
		return "", fmt.Errorf("mita 二进制过小，可能损坏")
	}
	// ELF magic for Linux; refuse obvious garbage (HTML error pages etc.).
	if runtime.GOOS == "linux" && !(len(data) >= 4 && data[0] == 0x7f && data[1] == 'E' && data[2] == 'L' && data[3] == 'F') {
		return "", fmt.Errorf("mita 不是 ELF 可执行文件（可能下载损坏）")
	}

	// Always materialize /usr/local/bin/mita.
	if err := os.MkdirAll(filepath.Dir(mitaSystemBinary), 0o755); err != nil {
		return "", fmt.Errorf("创建 %s 失败: %w", filepath.Dir(mitaSystemBinary), err)
	}
	if needRewrite(mitaSystemBinary, data) {
		if err := atomicWriteExec(mitaSystemBinary, data); err != nil {
			return "", fmt.Errorf("安装 %s 失败: %w", mitaSystemBinary, err)
		}
		log.Printf("mita: installed system binary %s (%d bytes)", mitaSystemBinary, len(data))
	}
	_ = os.Chmod(mitaSystemBinary, 0o755)

	// Keep panel cache path in sync for detectProxyCores / force reinstall.
	cachePath := filepath.Join(coreStateDir(), "mieru", "mita")
	if cachePath != src && cachePath != mitaSystemBinary {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			if needRewrite(cachePath, data) {
				_ = atomicWriteExec(cachePath, data)
			}
			_ = os.Chmod(cachePath, 0o755)
		}
	}
	// If src was already cache path, still ensure 0755.
	_ = os.Chmod(src, 0o755)

	// /var/lib/nft is often 0700; widen traverse bits so any leftover unit
	// pointing at the cache path can still run as User=mita.
	ensurePathTraversableForOthers(
		"/var/lib/nft",
		"/var/lib/nft/cores",
		filepath.Join(coreStateDir(), "mieru"),
	)

	return mitaSystemBinary, nil
}

func needRewrite(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	if len(have) != len(want) {
		return true
	}
	// cheap compare
	for i := range want {
		if have[i] != want[i] {
			return true
		}
	}
	return false
}

func ensurePathTraversableForOthers(dirs ...string) {
	for _, d := range dirs {
		st, err := os.Stat(d)
		if err != nil {
			continue
		}
		mode := st.Mode().Perm()
		// need at least --x--x--x for traverse when binary is under the tree
		if mode&0o111 != 0o111 {
			_ = os.Chmod(d, mode|0o111)
		}
	}
}

func ensureMitaSystemUser() error {
	if _, err := user.Lookup(mitaRuntimeUser); err == nil {
		return nil
	}
	if out, err := runCmdTimeout(15*time.Second, "useradd", "--no-create-home", "--user-group", mitaRuntimeUser); err != nil {
		if out2, err2 := runCmdTimeout(15*time.Second, "adduser", "--system", "--group", "--no-create-home", mitaRuntimeUser); err2 != nil {
			return fmt.Errorf("创建系统用户 mita 失败: useradd %v (%s); adduser %v (%s)",
				err, truncateOut(out), err2, truncateOut(out2))
		}
	}
	return nil
}

func ensureMitaRuntimeDirs() error {
	dirs := []string{"/etc/mita", "/var/lib/mita", "/var/run/mita"}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o775); err != nil {
			return fmt.Errorf("创建 %s 失败: %w", d, err)
		}
		_, _ = runCmdTimeout(5*time.Second, "chown", "-R", "mita:mita", d)
		_ = os.Chmod(d, 0o775)
	}
	return nil
}

func ensureMitaSystemdUnit(mitaPath string) error {
	if mitaRPCReady(mitaPath) {
		return nil
	}
	// Existing non-nft unit that already works: leave it.
	if unitActive("mita") && !unitIsNFTManaged(mitaUnitPath) {
		_, _ = runCmdTimeout(20*time.Second, "systemctl", "start", "mita")
		if mitaRPCReady(mitaPath) {
			return nil
		}
		log.Printf("mita: existing unit active but RPC down; rewriting unit → %s", mitaPath)
	}

	body := mitaSystemdUnit(mitaPath)
	if err := os.WriteFile(mitaUnitPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", mitaUnitPath, err)
	}
	if out, err := runCmdTimeout(20*time.Second, "systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %v (%s)", err, truncateOut(out))
	}
	return nil
}

func mitaSystemdUnit(mitaPath string) string {
	// Mirror official package unit; ExecStart must be `run` (daemon), not `start` (RPC).
	// Binary path must be world-executable (/usr/local/bin/mita).
	return fmt.Sprintf(`[Unit]
Description=Mieru proxy server (%s)
After=network-online.target network.service networking.service NetworkManager.service systemd-networkd.service
Wants=network-online.target
StartLimitBurst=5
StartLimitIntervalSec=60

[Service]
Type=simple
User=mita
Group=mita
AmbientCapabilities=CAP_NET_BIND_SERVICE
Environment=MITA_LOG_NO_TIMESTAMP=true
ExecStartPre=+/usr/bin/mkdir -p /var/run/mita
ExecStartPre=+/usr/bin/chown -R mita:mita /var/run/mita
ExecStartPre=+/usr/bin/chmod 775 /var/run/mita
ExecStartPre=+/usr/bin/test -x %s
ExecStart=%s run
Nice=-10
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, mitaUnitMarker, mitaPath, mitaPath)
}

func unitIsNFTManaged(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), mitaUnitMarker)
}

func unitActive(name string) bool {
	out, err := runCmdTimeout(8*time.Second, "systemctl", "is-active", name)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "active"
}

func mitaRPCReady(mitaPath string) bool {
	_, err := runCmdTimeout(6*time.Second, mitaPath, "status")
	return err == nil
}

// ensureMitaDaemon brings up the mita management daemon (RPC) if needed.
func ensureMitaDaemon(mitaPath string) error {
	// Prefer system binary path once prepared.
	path := strings.TrimSpace(mitaPath)
	if path == "" {
		path = resolveMitaBinary()
	}
	if path == "" {
		return fmt.Errorf("未找到 mita 二进制")
	}
	if mitaRPCReady(path) {
		return nil
	}

	canon, err := prepareMitaRuntime(path)
	if err != nil {
		return fmt.Errorf("准备 mita 运行时失败: %w", err)
	}
	path = canon
	if mitaRPCReady(path) {
		return nil
	}

	// Reset failed state so restart is allowed.
	_, _ = runCmdTimeout(10*time.Second, "systemctl", "reset-failed", "mita")
	_, _ = runCmdTimeout(20*time.Second, "systemctl", "enable", "mita")

	out, err := runCmdTimeout(25*time.Second, "systemctl", "restart", "mita")
	if err != nil {
		out2, err2 := runCmdTimeout(25*time.Second, "systemctl", "start", "mita")
		if err2 != nil {
			// Probe why: can user mita execute the binary?
			probe := diagnoseMitaStart(path)
			// Last resort: root unit (no User=) so RPC comes up; still official binary.
			if err3 := installAndStartMitaRootUnit(path); err3 != nil {
				jout := mitaJournal()
				return fmt.Errorf(
					"无法启动 mita 守护进程（需要 `mita run` 提供本机 RPC，不是 `mita start`）。"+
						"systemctl restart: %v (%s); start: %v (%s); root-fallback: %v。%s journal: %s",
					err, truncateOut(out), err2, truncateOut(out2), err3, probe, truncateOut(jout))
			}
		}
	}

	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if mitaRPCReady(path) {
			return nil
		}
		st, _ := runCmdTimeout(5*time.Second, path, "status")
		last = truncateOut(st)
		if failed, _ := runCmdTimeout(3*time.Second, "systemctl", "is-failed", "mita"); strings.TrimSpace(failed) == "failed" {
			// Try root fallback once mid-wait.
			_ = installAndStartMitaRootUnit(path)
			if mitaRPCReady(path) {
				return nil
			}
			return fmt.Errorf("mita.service 启动失败: status=%s; %s journal: %s",
				last, diagnoseMitaStart(path), truncateOut(mitaJournal()))
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("mita 守护进程已尝试启动但 RPC 仍不可用: %s; %s journal: %s",
		last, diagnoseMitaStart(path), truncateOut(mitaJournal()))
}

func mitaJournal() string {
	out, _ := runCmdTimeout(8*time.Second, "journalctl", "-u", "mita", "-n", "50", "--no-pager")
	return out
}

// diagnoseMitaStart runs short probes so UI errors show the real reason
// (permission denied, not a binary, wrong arch) instead of only systemctl status 1.
func diagnoseMitaStart(mitaPath string) string {
	var parts []string
	if st, err := os.Stat(mitaPath); err != nil {
		parts = append(parts, fmt.Sprintf("stat %s: %v", mitaPath, err))
	} else {
		parts = append(parts, fmt.Sprintf("bin=%s mode=%s", mitaPath, st.Mode()))
	}
	// Can mita user exec?
	if out, err := runCmdTimeout(8*time.Second, "runuser", "-u", "mita", "--", mitaPath, "version"); err != nil {
		if out2, err2 := runCmdTimeout(8*time.Second, "su", "-", "mita", "-s", "/bin/sh", "-c", mitaPath+" version"); err2 != nil {
			parts = append(parts, fmt.Sprintf("as-mita-exec: %v (%s) / %v (%s)",
				err, truncateOut(out), err2, truncateOut(out2)))
		} else {
			parts = append(parts, "as-mita-exec: ok(su)")
		}
	} else {
		parts = append(parts, "as-mita-exec: ok; version="+truncateOut(out))
	}
	// Capture a few seconds of `mita run` stderr as root (foreground with timeout).
	if out, err := runCmdTimeout(3*time.Second, mitaPath, "run"); err != nil {
		// timeout is expected if run stays up; only report if process exits early with output
		if !strings.Contains(err.Error(), "timeout") {
			parts = append(parts, fmt.Sprintf("mita-run-foreground: %v (%s)", err, truncateOut(out)))
		} else if strings.TrimSpace(out) != "" {
			parts = append(parts, "mita-run-log: "+truncateOut(out))
		}
	}
	return "probe: " + strings.Join(parts, "; ")
}

// installAndStartMitaRootUnit is a last-resort unit without User=mita when the
// unprivileged unit cannot exec the binary. Still uses `mita run`.
func installAndStartMitaRootUnit(mitaPath string) error {
	body := fmt.Sprintf(`[Unit]
Description=Mieru proxy server (%s-root)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=MITA_LOG_NO_TIMESTAMP=true
ExecStartPre=+/usr/bin/mkdir -p /var/run/mita /etc/mita /var/lib/mita
ExecStartPre=+/usr/bin/chmod 775 /var/run/mita /etc/mita /var/lib/mita
ExecStart=%s run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, mitaUnitMarker, mitaPath)
	if err := os.WriteFile(mitaUnitPath, []byte(body), 0o644); err != nil {
		return err
	}
	_, _ = runCmdTimeout(15*time.Second, "systemctl", "daemon-reload")
	_, _ = runCmdTimeout(10*time.Second, "systemctl", "reset-failed", "mita")
	out, err := runCmdTimeout(25*time.Second, "systemctl", "restart", "mita")
	if err != nil {
		return fmt.Errorf("%v (%s)", err, truncateOut(out))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mitaRPCReady(mitaPath) {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("root unit started but RPC still down; journal: %s", truncateOut(mitaJournal()))
}

// mitaProxyStart asks a running management daemon to begin proxy listen.
func mitaProxyStart(mitaPath string) error {
	out, err := runCmdTimeout(20*time.Second, mitaPath, "start")
	if err != nil {
		return fmt.Errorf("mita start 失败: %v (%s)", err, truncateOut(out))
	}
	return nil
}

// mitaProxyStop asks the daemon to stop proxy listen (daemon itself stays up).
func mitaProxyStop(mitaPath string) {
	_, _ = runCmdTimeout(15*time.Second, mitaPath, "stop")
}

// mitaProxyListening is true when `mita status` reports the proxy is up.
// Used to skip stop+start on an unchanged republish.
func mitaProxyListening(mitaPath string) bool {
	out, err := runCmdTimeout(6*time.Second, mitaPath, "status")
	if err != nil {
		return false
	}
	return mitaStatusIsRunning(out)
}

func mitaStatusIsRunning(out string) bool {
	s := strings.ToUpper(out)
	if strings.Contains(s, "IDLE") || strings.Contains(s, "STOPPED") {
		return false
	}
	return strings.Contains(s, "RUNNING") || strings.Contains(s, "STARTED") || strings.Contains(s, "LISTENING")
}

// installMitaRuntimeAfterBinary is called after panel core-install drops mita.
func installMitaRuntimeAfterBinary(mitaPath string) {
	if strings.TrimSpace(mitaPath) == "" {
		return
	}
	canon, err := prepareMitaRuntime(mitaPath)
	if err != nil {
		log.Printf("core_install: mita runtime prepare: %v", err)
		return
	}
	if err := ensureMitaDaemon(canon); err != nil {
		log.Printf("core_install: mita daemon start: %v", err)
	}
}

// resolveMitaBinary picks the best available mita binary.
// Prefer /usr/local/bin and /usr/bin (User=mita can exec), then panel cache.
func resolveMitaBinary() string {
	paths := []string{
		mitaSystemBinary,
		"/usr/bin/mita",
		filepath.Join(coreStateDir(), "mieru", "mita"),
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
