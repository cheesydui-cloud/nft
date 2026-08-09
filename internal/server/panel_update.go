package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"nft/internal/db"
)

// Panel host self-update: check GitHub latest and schedule nft-upgrade so the
// panel does not re-implement install.sh's download/verify/rollback path.

const (
	panelUpgradeScript = "/usr/local/sbin/nft-upgrade"
	panelUpdateStatePath = "/var/lib/nft/panel-update.json"
	panelUpdateGrace     = 15 * time.Minute
)

type panelUpdateState struct {
	State          string `json:"state"` // idle | running | succeeded | failed
	TargetVersion  string `json:"target_version,omitempty"`
	StartedAt      int64  `json:"started_at,omitempty"`
	FinishedAt     int64  `json:"finished_at,omitempty"`
	Error          string `json:"error,omitempty"`
	StartedVersion string `json:"started_version,omitempty"`
}

var (
	panelUpdateMu sync.Mutex
)

func (s *Server) apiGetPanelUpdate(w http.ResponseWriter, r *http.Request) {
	info := s.panelUpdateInfo(true)
	jsonOK(w, info)
}

func (s *Server) apiCheckPanelUpdate(w http.ResponseWriter, r *http.Request) {
	// Same payload as GET; explicit check endpoint for UI "检查更新".
	info := s.panelUpdateInfo(true)
	jsonOK(w, info)
}

func (s *Server) apiGetPanelUpdateStatus(w http.ResponseWriter, r *http.Request) {
	st := loadPanelUpdateState()
	// Heal "running" after restart: if version advanced past started, mark success.
	cur := serverVersion()
	if st.State == "running" && st.StartedVersion != "" && cur != "" && cur != "dev" {
		if cur != st.StartedVersion && (st.TargetVersion == "" || semverGE(cur, st.TargetVersion) || cur == st.TargetVersion) {
			st.State = "succeeded"
			st.FinishedAt = time.Now().Unix()
			st.Error = ""
			_ = savePanelUpdateState(st)
		} else if st.StartedAt > 0 && time.Now().Unix()-st.StartedAt > int64(panelUpdateGrace/time.Second) {
			// Still on old version after grace — likely failed without writing state.
			st.State = "failed"
			st.FinishedAt = time.Now().Unix()
			if st.Error == "" {
				st.Error = "升级超时：面板版本未变化，请查看 journalctl -u nft-server / nft-upgrade 日志"
			}
			_ = savePanelUpdateState(st)
		}
	}
	jsonOK(w, map[string]any{
		"state":           st.State,
		"target_version":  st.TargetVersion,
		"started_at":      st.StartedAt,
		"finished_at":     st.FinishedAt,
		"error":           st.Error,
		"started_version": st.StartedVersion,
		"current_version": cur,
	})
}

func (s *Server) apiApplyPanelUpdate(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	var body struct {
		Release string `json:"release"`
	}
	_ = decodeJSON(r, &body) // empty body ok
	release := strings.TrimSpace(body.Release)

	info := s.panelUpdateInfo(false)
	if !info["can_apply"].(bool) {
		msg, _ := info["message"].(string)
		if msg == "" {
			msg = "当前环境无法执行面板升级"
		}
		jsonErr(w, http.StatusServiceUnavailable, msg)
		return
	}

	panelUpdateMu.Lock()
	defer panelUpdateMu.Unlock()

	st := loadPanelUpdateState()
	if st.State == "running" && st.StartedAt > 0 && time.Now().Unix()-st.StartedAt < int64(panelUpdateGrace/time.Second) {
		jsonErr(w, http.StatusConflict, "已有面板升级任务进行中")
		return
	}

	target := release
	if target == "" {
		if lv, ok := info["latest_version"].(string); ok {
			target = lv
		}
	}
	if target == "" {
		// Still allow nft-upgrade latest without a resolved tag.
		target = "latest"
	}

	cur := serverVersion()
	st = panelUpdateState{
		State:          "running",
		TargetVersion:  target,
		StartedAt:      time.Now().Unix(),
		StartedVersion: cur,
	}
	if err := savePanelUpdateState(st); err != nil {
		jsonErr(w, http.StatusInternalServerError, "无法写入升级状态: "+err.Error())
		return
	}

	if err := schedulePanelUpgrade(release); err != nil {
		st.State = "failed"
		st.FinishedAt = time.Now().Unix()
		st.Error = err.Error()
		_ = savePanelUpdateState(st)
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if u != nil {
		db.WriteAudit(s.DB, u.ID, "system.panel_update", target, fmt.Sprintf("from=%s", cur))
	}

	jsonOK(w, map[string]any{
		"ok":             true,
		"accepted":       true,
		"target_version": target,
		"mode":           "nft-upgrade",
		"hint":           "面板即将重启，请稍后刷新；升级由 nft-upgrade 执行",
	})
}

// panelUpdateInfo builds the check/status summary. fetchLatest controls whether
// GitHub is contacted (false for pre-apply capability probe only).
func (s *Server) panelUpdateInfo(fetchLatest bool) map[string]any {
	cur := serverVersion()
	script := panelUpgradeScript
	scriptOK := fileExecutable(script)
	archOK := runtime.GOARCH == "amd64"
	rootish := os.Geteuid() == 0
	proxy := ghProxyPrefix()

	out := map[string]any{
		"current_version":       cur,
		"latest_version":        "",
		"update_available":      false,
		"channel":               "latest",
		"upgrade_script":        script,
		"upgrade_script_present": scriptOK,
		"install_dir":           "/usr/local/sbin",
		"gh_proxy":              proxy,
		"arch_ok":               archOK,
		"can_apply":             scriptOK && archOK && rootish && cur != "dev",
		"message":               "",
		"repo":                  agentRepo,
	}

	var msgs []string
	if cur == "dev" {
		msgs = append(msgs, "当前为 dev 构建，无法从 release 自更新")
	}
	if !archOK {
		msgs = append(msgs, "仅支持 amd64 主机的一键升级")
	}
	if !scriptOK {
		msgs = append(msgs, "未找到 "+script+"，请先用 install.sh 安装面板")
	}
	if !rootish {
		msgs = append(msgs, "面板未以 root 运行，无法 systemctl 升级")
	}

	if fetchLatest && cur != "dev" {
		rel, err := fetchGitHubRelease(agentRepo, "latest")
		if err != nil {
			msgs = append(msgs, "检查更新失败: "+err.Error())
		} else {
			latest := strings.TrimSpace(rel.TagName)
			out["latest_version"] = latest
			if latest != "" && cur != "" && !semverGE(cur, latest) {
				out["update_available"] = true
			}
		}
	}

	if len(msgs) > 0 {
		out["message"] = strings.Join(msgs, "；")
	}
	st := loadPanelUpdateState()
	out["status"] = st
	return out
}

func fileExecutable(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0o111 != 0
}

func loadPanelUpdateState() panelUpdateState {
	b, err := os.ReadFile(panelUpdateStatePath)
	if err != nil {
		return panelUpdateState{State: "idle"}
	}
	var st panelUpdateState
	if err := json.Unmarshal(b, &st); err != nil || st.State == "" {
		return panelUpdateState{State: "idle"}
	}
	return st
}

func savePanelUpdateState(st panelUpdateState) error {
	if err := os.MkdirAll(filepath.Dir(panelUpdateStatePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := panelUpdateStatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, panelUpdateStatePath)
}

// schedulePanelUpgrade detaches nft-upgrade so the HTTP response can return
// before systemctl restarts this process.
func schedulePanelUpgrade(release string) error {
	script := panelUpgradeScript
	if !fileExecutable(script) {
		return fmt.Errorf("未找到可执行的 %s", script)
	}
	args := []string{script, "update"}
	if strings.TrimSpace(release) != "" && release != "latest" {
		args = append(args, "--release", strings.TrimSpace(release))
	}

	// Prefer systemd-run --no-block so the unit survives panel restart.
	if path, err := exec.LookPath("systemd-run"); err == nil {
		cmdArgs := []string{"--no-block", "--collect", "--unit=nft-panel-upgrade.service", "--"}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(path, cmdArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			// unit name may already exist from a previous run — try unique name
			unit := fmt.Sprintf("nft-panel-upgrade-%d.service", time.Now().Unix())
			cmdArgs = []string{"--no-block", "--collect", "--unit=" + unit, "--"}
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command(path, cmdArgs...)
			if out2, err2 := cmd.CombinedOutput(); err2 != nil {
				return fmt.Errorf("systemd-run 启动升级失败: %v (%s; retry %s)", err2, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
			}
		}
		return nil
	}

	// Fallback: setsid-detached process.
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 nft-upgrade 失败: %w", err)
	}
	// Don't wait; reparent via Start only (best-effort without systemd).
	go func() { _ = cmd.Wait() }()
	return nil
}


// normalizeKomariURL accepts empty (disabled) or an absolute http(s) URL.
func normalizeKomariURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	low := strings.ToLower(raw)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return "", fmt.Errorf("Komari 地址须为 http:// 或 https://")
	}
	// strip trailing slash for stable compare
	return strings.TrimRight(raw, "/"), nil
}
