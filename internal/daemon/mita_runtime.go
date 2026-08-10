package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// Official mita (mieru server) is a two-process design:
//
//	mita run    — long-running management daemon (gRPC on unix socket under /var/run/mita)
//	mita start  — RPC to that daemon: begin proxy listen (requires run first)
//	mita apply  — RPC: merge server JSON config
//	mita status — RPC: IDLE / RUNNING / …
//
// Deb/rpm postinst creates user mita, dirs /etc/mita /var/lib/mita /var/run/mita,
// and a systemd unit with ExecStart=/usr/bin/mita run.
//
// Panel core-cache only drops a bare binary under /var/lib/nft/cores/mieru/mita.
// Without user/dirs/unit, `mita start` fails with the same RPC connection error
// as a missing daemon — which is what users hit after v6.6.15.

const (
	mitaUnitPath    = "/etc/systemd/system/mita.service"
	mitaUnitMarker  = "nft-managed"
	mitaRuntimeUser = "mita"
)

// prepareMitaRuntime creates the mita system user, runtime directories, and a
// systemd unit that runs `mita run` (if no healthy unit already exists).
func prepareMitaRuntime(mitaPath string) error {
	mitaPath = strings.TrimSpace(mitaPath)
	if mitaPath == "" {
		return fmt.Errorf("mita 路径为空")
	}
	if st, err := os.Stat(mitaPath); err != nil || st.IsDir() {
		return fmt.Errorf("mita 二进制不可用: %s", mitaPath)
	}
	if err := ensureMitaSystemUser(); err != nil {
		return err
	}
	if err := ensureMitaRuntimeDirs(); err != nil {
		return err
	}
	if err := ensureMitaSystemdUnit(mitaPath); err != nil {
		return err
	}
	return nil
}

func ensureMitaSystemUser() error {
	if _, err := user.Lookup(mitaRuntimeUser); err == nil {
		return nil
	}
	// Prefer useradd (matches official postinst); fall back to adduser.
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
		// Best-effort ownership; unit ExecStartPre also chowns /var/run/mita.
		_, _ = runCmdTimeout(5*time.Second, "chown", "-R", "mita:mita", d)
		_ = os.Chmod(d, 0o775)
	}
	return nil
}

func ensureMitaSystemdUnit(mitaPath string) error {
	// If an existing unit already keeps RPC healthy, leave it alone.
	if mitaRPCReady(mitaPath) {
		return nil
	}
	// Prefer not to overwrite a non-nft unit that is currently active.
	if unitActive("mita") && !unitIsNFTManaged(mitaUnitPath) {
		// Try start once; if it works after dirs/user, done without rewrite.
		_, _ = runCmdTimeout(20*time.Second, "systemctl", "start", "mita")
		if mitaRPCReady(mitaPath) {
			return nil
		}
		// Active unit but RPC still dead (e.g. ExecStart points at missing /usr/bin/mita).
		// Fall through and install nft-managed unit that uses the real binary path.
		log.Printf("mita: existing unit active but RPC down; installing nft-managed unit → %s", mitaPath)
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
	return fmt.Sprintf(`[Unit]
Description=Mieru proxy server (%s)
After=network-online.target network.service networking.service NetworkManager.service systemd-networkd.service
Wants=network-online.target
StartLimitBurst=5
StartLimitIntervalSec=60

[Service]
Type=exec
User=mita
Group=mita
AmbientCapabilities=CAP_NET_BIND_SERVICE
Environment=MITA_LOG_NO_TIMESTAMP=true
ExecStartPre=+/usr/bin/mkdir -p /var/run/mita
ExecStartPre=+/usr/bin/chown -R mita:mita /var/run/mita
ExecStartPre=+/usr/bin/chmod 775 /var/run/mita
ExecStart=%s run
Nice=-10
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, mitaUnitMarker, mitaPath)
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
	// status succeeds (exit 0) for IDLE and RUNNING once the run-daemon is up.
	_, err := runCmdTimeout(6*time.Second, mitaPath, "status")
	return err == nil
}

// ensureMitaDaemon brings up the mita management daemon (RPC) if needed.
// Order: prepare runtime → systemctl enable/start mita (mita run) → wait status.
// Does NOT call `mita start` here — that is the proxy listen RPC and needs config.
func ensureMitaDaemon(mitaPath string) error {
	if mitaRPCReady(mitaPath) {
		return nil
	}
	if err := prepareMitaRuntime(mitaPath); err != nil {
		return fmt.Errorf("准备 mita 运行时失败: %w", err)
	}
	if mitaRPCReady(mitaPath) {
		return nil
	}

	// Enable + start the long-running `mita run` unit.
	_, _ = runCmdTimeout(20*time.Second, "systemctl", "enable", "mita")
	if out, err := runCmdTimeout(25*time.Second, "systemctl", "restart", "mita"); err != nil {
		// restart may fail if unit was never loaded; try start
		if out2, err2 := runCmdTimeout(25*time.Second, "systemctl", "start", "mita"); err2 != nil {
			// Last resort: launch `mita run` under systemd-run so we still get a supervised process
			// without a permanent unit (should be rare after ensureMitaSystemdUnit).
			out3, err3 := runCmdTimeout(25*time.Second, "systemd-run", "--unit=mita", "--property=User=mita",
				"--property=Group=mita", "--property=Restart=on-failure", mitaPath, "run")
			if err3 != nil && !mitaRPCReady(mitaPath) {
				jout, _ := runCmdTimeout(8*time.Second, "journalctl", "-u", "mita", "-n", "30", "--no-pager")
				return fmt.Errorf(
					"无法启动 mita 守护进程（需要 `mita run` 提供本机 RPC，不是 `mita start`）。"+
						"systemctl restart: %v (%s); start: %v (%s); systemd-run: %v (%s)。"+
						"journal: %s",
					err, truncateOut(out), err2, truncateOut(out2), err3, truncateOut(out3), truncateOut(jout))
			}
		}
	}

	deadline := time.Now().Add(12 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if mitaRPCReady(mitaPath) {
			return nil
		}
		out, _ := runCmdTimeout(5*time.Second, mitaPath, "status")
		last = truncateOut(out)
		// Also surface unit failure quickly.
		if st, _ := runCmdTimeout(3*time.Second, "systemctl", "is-failed", "mita"); strings.TrimSpace(st) == "failed" {
			jout, _ := runCmdTimeout(8*time.Second, "journalctl", "-u", "mita", "-n", "40", "--no-pager")
			return fmt.Errorf("mita.service 启动失败: status=%s journal=%s", last, truncateOut(jout))
		}
		time.Sleep(400 * time.Millisecond)
	}
	jout, _ := runCmdTimeout(8*time.Second, "journalctl", "-u", "mita", "-n", "40", "--no-pager")
	return fmt.Errorf("mita 守护进程已尝试启动但 RPC 仍不可用: %s; journal: %s", last, truncateOut(jout))
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

// installMitaRuntimeAfterBinary is called after panel core-install drops mita.
// Best-effort: never fails the install ack if systemd is unavailable (e.g. tests).
func installMitaRuntimeAfterBinary(mitaPath string) {
	if strings.TrimSpace(mitaPath) == "" {
		return
	}
	if err := prepareMitaRuntime(mitaPath); err != nil {
		log.Printf("core_install: mita runtime prepare: %v", err)
		return
	}
	// Start management daemon so the next publish can apply immediately.
	if err := ensureMitaDaemon(mitaPath); err != nil {
		log.Printf("core_install: mita daemon start: %v", err)
	}
}

// preferSystemMita returns true if path looks like a packaged install.
func preferSystemMita(path string) bool {
	return path == "/usr/bin/mita" || path == "/usr/local/bin/mita"
}

// resolveMitaBinary picks the best available mita binary, preferring packaged paths
// when present so we don't fight an official unit pointing at /usr/bin/mita.
func resolveMitaBinary() string {
	// Packaged first, then panel cache, then PATH.
	paths := []string{
		"/usr/bin/mita",
		"/usr/local/bin/mita",
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
