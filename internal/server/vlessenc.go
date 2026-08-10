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
// auth: "x25519" (default, short keys, matches Weir / most clients) or "mlkem" (PQ, long).
// Requires a cached xray binary that supports the vlessenc subcommand.
func generateVlessEncPair(auth string) (encryption, decryption, version string, err error) {
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
	preferPQ := strings.EqualFold(strings.TrimSpace(auth), "mlkem") ||
		strings.EqualFold(strings.TrimSpace(auth), "pq") ||
		strings.EqualFold(strings.TrimSpace(auth), "mlkem768")
	enc, dec, ok := parseVlessEncOutputPrefer(out, preferPQ)
	if !ok {
		return "", "", version, fmt.Errorf("无法解析 xray vlessenc 输出（version=%s）:\n%s", version, truncateRunes(out, 400))
	}
	// Final role guard: 0rtt = client encryption, 600s = server decryption.
	enc, dec = alignVlessEncRoles(enc, dec)
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

// parseVlessEncOutput extracts client encryption + server decryption from
// `xray vlessenc` stdout.
//
// Real xray output (XTLS/Xray-core main/commands/all/vlessenc.go):
//
//	Authentication: X25519, not Post-Quantum
//	"decryption": "mlkem768x25519plus.native.600s...."
//	"encryption": "mlkem768x25519plus.native.0rtt...."
//
//	Authentication: ML-KEM-768, Post-Quantum
//	"decryption": "..."
//	"encryption": "..."
//
// parseVlessEncOutput defaults to X25519 short keys (Weir-compatible).
func parseVlessEncOutput(out string) (encryption, decryption string, ok bool) {
	return parseVlessEncOutputPrefer(out, false)
}

// parseVlessEncOutputPrefer extracts client encryption + server decryption from
// `xray vlessenc` stdout.
//
// Real xray output (XTLS/Xray-core main/commands/all/vlessenc.go):
//
//	Authentication: X25519, not Post-Quantum
//	"decryption": "mlkem768x25519plus.native.600s...."   // 32-byte key (~43 chars)
//	"encryption": "mlkem768x25519plus.native.0rtt...."
//
//	Authentication: ML-KEM-768, Post-Quantum
//	"decryption": "..."  // 64-byte seed
//	"encryption": "..."  // 1184-byte pubkey — breaks many clients / share URIs
//
// Default preferPQ=false → first (X25519) pair. Weir and most mobile clients
// use the short pair; PQ material produces multi-KB encryption that Clash /
// Shadowrocket / URI import often drop or fail to handshake.
// Never trust token order alone — assign roles via 0rtt / 600s heuristics.
type vlessEncPair struct{ enc, dec string }

func parseVlessEncOutputPrefer(out string, preferPQ bool) (encryption, decryption string, ok bool) {
	// 1) Exact xray JSON-ish lines: "decryption": "..." / "encryption": "..."
	//    Also accept unquoted keys: decryption: ...
	keyValRe := regexp.MustCompile(`(?i)["']?(decryption|encryption)["']?\s*:\s*["']?([mlkemvlessenc][0-9A-Za-z+./_=-]+)["']?`)
	// 2) Human labels: Encryption (client): ... / Decryption (server): ...
	labelRe := regexp.MustCompile(`(?i)\b(encryption|decryption)\b(?:\s*\([^)]*\))?\s*:\s*["']?([mlkemvlessenc][0-9A-Za-z+./_=-]+)["']?`)
	// 3) Chinese labels
	zhRe := regexp.MustCompile(`(?i)(客户端|服务端|client|server)\s*[:：]\s*["']?([mlkemvlessenc][0-9A-Za-z+./_=-]+)["']?`)

	var pairs []vlessEncPair
	cur := vlessEncPair{}
	flush := func() {
		if cur.enc != "" && cur.dec != "" {
			pairs = append(pairs, cur)
			cur = vlessEncPair{}
		}
	}

	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			flush()
			continue
		}
		// New authentication block → previous pair is complete if both set.
		if strings.Contains(strings.ToLower(ln), "authentication:") {
			flush()
			continue
		}

		matched := false
		for _, re := range []*regexp.Regexp{keyValRe, labelRe} {
			if m := re.FindStringSubmatch(ln); len(m) == 3 {
				role := strings.ToLower(m[1])
				val := stripEncQuotes(m[2])
				if val == "" {
					continue
				}
				if role == "encryption" {
					cur.enc = val
				} else if role == "decryption" {
					cur.dec = val
				}
				matched = true
				break
			}
		}
		if matched {
			if cur.enc != "" && cur.dec != "" {
				flush()
			}
			continue
		}
		if m := zhRe.FindStringSubmatch(ln); len(m) == 3 {
			role := strings.ToLower(m[1])
			val := stripEncQuotes(m[2])
			if strings.Contains(role, "client") || strings.Contains(role, "客户端") {
				cur.enc = val
			} else if strings.Contains(role, "server") || strings.Contains(role, "服务端") {
				cur.dec = val
			}
			if cur.enc != "" && cur.dec != "" {
				flush()
			}
		}
	}
	flush()

	if len(pairs) > 0 {
		p := pickVlessEncPair(pairs, preferPQ)
		encryption, decryption = alignVlessEncRoles(p.enc, p.dec)
		return encryption, decryption, true
	}

	// Fallback: collect all mlkem tokens and assign by 0rtt/600s role, not order.
	tokenRe := regexp.MustCompile(`(?i)\b(mlkem768x25519plus\.[0-9A-Za-z+./_=-]+)`)
	var tokens []string
	seen := map[string]bool{}
	for _, ln := range lines {
		for _, m := range tokenRe.FindAllString(ln, -1) {
			m = stripEncQuotes(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			tokens = append(tokens, m)
		}
	}
	if len(tokens) < 2 {
		// last-ditch: any mlkem-looking tokens
		looseRe := regexp.MustCompile(`(?i)\b(mlkem[0-9A-Za-z+./_=-]{20,})`)
		for _, ln := range lines {
			for _, m := range looseRe.FindAllString(ln, -1) {
				m = stripEncQuotes(m)
				if m == "" || seen[m] {
					continue
				}
				seen[m] = true
				tokens = append(tokens, m)
			}
		}
	}
	if len(tokens) < 2 {
		return "", "", false
	}

	// Prefer short (X25519) pair: first two tokens when not preferPQ; last two when PQ.
	var a, b string
	if preferPQ || len(tokens) < 4 {
		a, b = tokens[len(tokens)-2], tokens[len(tokens)-1]
	} else {
		// tokens order: dec0, enc0, dec1, enc1 — take first pair then align roles
		a, b = tokens[0], tokens[1]
	}
	encryption, decryption = alignVlessEncRoles(a, b)
	if encryption == "" || decryption == "" {
		return "", "", false
	}
	return encryption, decryption, true
}

// pickVlessEncPair chooses X25519 (short) or ML-KEM (long) from parsed pairs.
// xray prints X25519 first, PQ second. Short encryption key ≈ 40–80 chars after prefix.
func pickVlessEncPair(pairs []vlessEncPair, preferPQ bool) vlessEncPair {
	if len(pairs) == 1 {
		return pairs[0]
	}
	if preferPQ {
		return pairs[len(pairs)-1]
	}
	// Prefer shortest client encryption (X25519 public ≈ 43 chars; PQ ≈ 1500+).
	best := pairs[0]
	bestLen := len(best.enc)
	for _, p := range pairs[1:] {
		if len(p.enc) < bestLen {
			best = p
			bestLen = len(p.enc)
		}
	}
	return best
}

// alignVlessEncRoles ensures encryption is the client (0rtt) string and
// decryption is the server (600s / ticket lifetime) string.
// xray vlessenc always prints decryption first then encryption; naive
// "first token = encryption" fallback used to swap them and break handshakes.
func alignVlessEncRoles(a, b string) (encryption, decryption string) {
	a = stripEncQuotes(a)
	b = stripEncQuotes(b)
	if a == "" || b == "" {
		return a, b
	}
	aClient := vlessEncLooksClient(a)
	bClient := vlessEncLooksClient(b)
	aServer := vlessEncLooksServer(a)
	bServer := vlessEncLooksServer(b)

	switch {
	case aClient && bServer:
		return a, b
	case bClient && aServer:
		return b, a
	case aServer && !bServer:
		return b, a
	case bServer && !aServer:
		return a, b
	case aClient && !bClient:
		return a, b
	case bClient && !aClient:
		return b, a
	default:
		// Unknown shape: keep given order (enc, dec) if caller already labeled;
		// when both unlabeled tokens, prefer second as client only if first has more dots/server-ish.
		return a, b
	}
}

func vlessEncLooksClient(s string) bool {
	parts := strings.Split(strings.ToLower(s), ".")
	if len(parts) < 3 {
		return false
	}
	// Client third segment is 0rtt or 1rtt (xray outbound).
	return parts[2] == "0rtt" || parts[2] == "1rtt"
}

func vlessEncLooksServer(s string) bool {
	parts := strings.Split(strings.ToLower(s), ".")
	if len(parts) < 3 {
		return false
	}
	// Server third segment is ticket lifetime, e.g. 600s or 100-500s.
	seg := parts[2]
	if seg == "0rtt" || seg == "1rtt" {
		return false
	}
	if strings.HasSuffix(seg, "s") {
		return true
	}
	// range form 100-500s already covered by suffix; bare digits+s handled above
	return strings.Contains(seg, "-")
}

// stripEncQuotes removes surrounding " / ' that some xray builds print around material.
func stripEncQuotes(s string) string {
	s = strings.TrimSpace(s)
	for {
		if len(s) >= 2 {
			if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
				s = strings.TrimSpace(s[1 : len(s)-1])
				continue
			}
		}
		if strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") {
			s = strings.TrimSpace(s[1:])
			continue
		}
		if strings.HasSuffix(s, "\"") || strings.HasSuffix(s, "'") {
			s = strings.TrimSpace(s[:len(s)-1])
			continue
		}
		// Trailing comma / JSON junk
		if strings.HasSuffix(s, ",") {
			s = strings.TrimSpace(s[:len(s)-1])
			continue
		}
		break
	}
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
