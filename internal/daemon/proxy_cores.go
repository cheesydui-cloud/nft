package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nft/internal/wsproto"
)

// detectProxyCores scans common install locations and PATH for xray / sing-box / mieru.
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
			name: "mieru",
			bins: []string{"mieru", "mita", "mbox"},
			paths: []string{
				"/usr/local/bin/mieru",
				"/usr/local/bin/mita",
				"/usr/bin/mieru",
				"/usr/bin/mita",
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
	for _, args := range [][]string{{"version"}, {"-version"}, {"--version"}} {
		cmd := exec.Command(path, args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
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

// handleProxyServiceApply is the phase-1 skeleton: acknowledge and dry-run.
// Real core config merge/reload lands in a follow-up once binaries are present.
func handleProxyServiceApply(req wsproto.ProxyServiceApply) wsproto.ProxyServiceApplyAck {
	cores := detectProxyCores()
	want := strings.ToLower(strings.TrimSpace(req.Core))
	if want == "mbox" || want == "mbox (mieru)" {
		want = "mieru"
	}
	var ver string
	found := false
	for _, c := range cores {
		name := strings.ToLower(c.Name)
		if name == want || (want == "mieru" && (name == "mieru" || name == "mita" || name == "mbox")) {
			found = true
			ver = c.Version
			break
		}
	}
	if !found {
		// Still OK dry-run so panel can store URI; operator installs core later.
		return wsproto.ProxyServiceApplyAck{
			OK:     true,
			DryRun: true,
			Error:  "",
			// Leave URI empty — panel builds share URI.
			CoreVersion: "",
		}
	}
	// Core present: phase-1 still dry-runs (no process management yet).
	return wsproto.ProxyServiceApplyAck{
		OK:          true,
		DryRun:      true,
		CoreVersion: ver,
	}
}
