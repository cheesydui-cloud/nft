package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// generateVlessEncPair runs `xray vlessenc` from the panel core cache and
// returns client encryption + server decryption strings.
// Requires a cached xray binary that supports the vlessenc subcommand.
func generateVlessEncPair() (encryption, decryption, version string, err error) {
	bin, version, err := resolveLocalXrayBinary()
	if err != nil {
		return "", "", "", err
	}
	out, err := runXrayCmd(bin, 15*time.Second, "vlessenc")
	if err != nil {
		out2, err2 := runXrayCmd(bin, 15*time.Second, "mlkem768x25519")
		if err2 != nil {
			return "", "", version, fmt.Errorf(
				"xray 无法生成 vlessenc（需要支持 vlessenc 的 Xray-core）。请在「代理核心缓存」拉取最新 xray（%s）。详情: %v",
				version, err)
		}
		out = out2
	}
	enc, dec, ok := parseVlessEncOutput(out)
	if !ok {
		return "", "", version, fmt.Errorf("无法解析 xray vlessenc 输出（version=%s）:\n%s", version, truncateRunes(out, 400))
	}
	return enc, dec, version, nil
}

func resolveLocalXrayBinary() (path, version string, err error) {
	arch := sanitizeArch(runtime.GOARCH)
	candidates := []string{arch, "amd64", "arm64"}
	seen := map[string]bool{}
	for _, a := range candidates {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		e, rerr := readCoreMeta("xray", a)
		if rerr != nil || e == nil || e.Path == "" {
			continue
		}
		if st, serr := os.Stat(e.Path); serr != nil || st.IsDir() {
			continue
		}
		_ = os.Chmod(e.Path, 0o755)
		return e.Path, e.Version, nil
	}
	if p, lerr := exec.LookPath("xray"); lerr == nil {
		return p, "path", nil
	}
	return "", "", fmt.Errorf("未找到 xray 二进制：请在系统设置 → 代理核心缓存 拉取 xray（%s 或 amd64）后再生成 vlessenc", arch)
}

func runXrayCmd(bin string, timeout time.Duration, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+filepath.Dir(bin))
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		if err != nil {
			return string(out), fmt.Errorf("%w: %s", err, truncateRunes(string(out), 200))
		}
		return string(out), nil
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return "", fmt.Errorf("xray %v timeout", args)
	}
}

// parseVlessEncOutput extracts Encryption / Decryption lines from `xray vlessenc`.
func parseVlessEncOutput(out string) (encryption, decryption string, ok bool) {
	lines := strings.Split(out, "\n")
	encRe := regexp.MustCompile(`(?i)encryption[^:]*:\s*(\S+)`)
	decRe := regexp.MustCompile(`(?i)decryption[^:]*:\s*(\S+)`)
	clientRe := regexp.MustCompile(`(?i)(?:client|客户端)[^:]*:\s*(\S+)`)
	serverRe := regexp.MustCompile(`(?i)(?:server|服务端)[^:]*:\s*(\S+)`)
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if m := encRe.FindStringSubmatch(ln); len(m) == 2 && encryption == "" {
			encryption = m[1]
		}
		if m := decRe.FindStringSubmatch(ln); len(m) == 2 && decryption == "" {
			decryption = m[1]
		}
	}
	if encryption == "" || decryption == "" {
		for _, ln := range lines {
			ln = strings.TrimSpace(ln)
			if encryption == "" {
				if m := clientRe.FindStringSubmatch(ln); len(m) == 2 {
					encryption = m[1]
				}
			}
			if decryption == "" {
				if m := serverRe.FindStringSubmatch(ln); len(m) == 2 {
					decryption = m[1]
				}
			}
		}
	}
	if encryption == "" || decryption == "" {
		tokenRe := regexp.MustCompile(`(?i)\b(mlkem[0-9a-z+./_=-]{20,}|vlessenc[0-9a-z+./_=-]{8,})`)
		var tokens []string
		for _, ln := range lines {
			for _, m := range tokenRe.FindAllString(ln, -1) {
				tokens = append(tokens, m)
			}
		}
		if encryption == "" && len(tokens) >= 1 {
			encryption = tokens[0]
		}
		if decryption == "" && len(tokens) >= 2 {
			decryption = tokens[1]
		}
	}
	encryption = strings.TrimSpace(encryption)
	decryption = strings.TrimSpace(decryption)
	if encryption == "" || decryption == "" {
		return "", "", false
	}
	return encryption, decryption, true
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
