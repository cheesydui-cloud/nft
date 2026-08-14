package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nft/internal/wsproto"
)

var xrayStatPairRe = regexp.MustCompile(`(?is)inbound>>>([^>\s"]+)>>>traffic>>>(uplink|downlink)"[^"]{0,80}"value"\s*:\s*"?(-?\d+)`)
var xrayStatLineRe = regexp.MustCompile(`inbound>>>([^>\s"]+)>>>traffic>>>(uplink|downlink)\D+(-?\d+)`)

// proxyStats tracks cumulative up/down per instance for delta samples.
type proxyStatsState struct {
	mu        sync.Mutex
	last      map[int64][2]int64 // instanceID → {up, down} cumulative
	baselined map[int64]bool     // first sample only arms the cursor (no delta)
	lastAt    time.Time
	pending   map[int64][2]int64 // reAdd park
}

var globalProxyStats = &proxyStatsState{
	last:      map[int64][2]int64{},
	baselined: map[int64]bool{},
	pending:   map[int64][2]int64{},
}

// statsPortPath returns the file that stores the loopback stats/API port for
// instanceID under coreDir (e.g. .../xray or .../sing-box).
func statsPortPath(coreDir string, instanceID int64) string {
	return filepath.Join(coreDir, fmt.Sprintf("instance-%d.statsport", instanceID))
}

func writeStatsPort(coreDir string, instanceID int64, port int) error {
	return os.WriteFile(statsPortPath(coreDir, instanceID), []byte(strconv.Itoa(port)+"\n"), 0o600)
}

func readStatsPort(coreDir string, instanceID int64) int {
	b, err := os.ReadFile(statsPortPath(coreDir, instanceID))
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

// statsUserPath stores the mieru/mita username used to match `mita get users`.
func statsUserPath(coreDir string, instanceID int64) string {
	return filepath.Join(coreDir, fmt.Sprintf("instance-%d.statsuser", instanceID))
}

func writeStatsUser(coreDir string, instanceID int64, user string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil
	}
	return os.WriteFile(statsUserPath(coreDir, instanceID), []byte(user+"\n"), 0o600)
}

func readStatsUser(coreDir string, instanceID int64) string {
	b, err := os.ReadFile(statsUserPath(coreDir, instanceID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pickLoopbackPort binds :0 on 127.0.0.1 and returns the assigned port.
func pickLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// proxyCounterSamples scans live proxy instances and returns per-instance
// deltas since the previous call (for dialer → panel proxy_counters).
func (d *Daemon) proxyCounterSamples() []wsproto.ProxyCounterSample {
	return globalProxyStats.sample()
}

func (d *Daemon) reAddProxyCounters(samples []wsproto.ProxyCounterSample) {
	globalProxyStats.reAdd(samples)
}

func (s *proxyStatsState) reAdd(samples []wsproto.ProxyCounterSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[int64][2]int64{}
	}
	if s.last == nil {
		s.last = map[int64][2]int64{}
	}
	for _, sm := range samples {
		last, ok := s.last[sm.InstanceID]
		if !ok {
			p := s.pending[sm.InstanceID]
			p[0] += sm.BytesUp
			p[1] += sm.BytesDown
			s.pending[sm.InstanceID] = p
			continue
		}
		up := last[0] - sm.BytesUp
		down := last[1] - sm.BytesDown
		var parkUp, parkDown int64
		if up < 0 {
			parkUp = -up
			up = 0
		}
		if down < 0 {
			parkDown = -down
			down = 0
		}
		s.last[sm.InstanceID] = [2]int64{up, down}
		if parkUp > 0 || parkDown > 0 {
			p := s.pending[sm.InstanceID]
			p[0] += parkUp
			p[1] += parkDown
			s.pending[sm.InstanceID] = p
		}
	}
}

func (s *proxyStatsState) sample() []wsproto.ProxyCounterSample {
	now := time.Now()
	type cum struct{ up, down int64 }
	live := map[int64]cum{}

	// xray instances
	xdir := filepath.Join(coreStateDir(), "xray")
	for _, id := range listInstanceIDs(xdir) {
		port := readStatsPort(xdir, id)
		if port == 0 {
			continue
		}
		bin := findCoreBinary([]string{"xray"}, []string{
			filepath.Join(xdir, "xray"),
			"/var/lib/nft/cores/xray/xray",
			"/usr/local/bin/xray",
			"/usr/bin/xray",
		})
		if bin == "" {
			continue
		}
			up, down, ok := queryXrayInboundStats(bin, port, "vless-in")
			if !ok {
				up, down, ok = queryXrayInboundStats(bin, port, "")
			}
			if !ok {
				continue
			}
			live[id] = cum{up, down}
	}

	// sing-box instances (ss / socks5 / anytls / naive)
	sdir := filepath.Join(coreStateDir(), "sing-box")
	for _, id := range listInstanceIDs(sdir) {
		port := readStatsPort(sdir, id)
		if port == 0 {
			continue
		}
		up, down, ok := querySingBoxClashTraffic(port)
		if !ok {
			continue
		}
		live[id] = cum{up, down}
	}

	// mieru/mita: `mita get users` 30-day rolling totals (best-effort cumulative).
	mdir := filepath.Join(coreStateDir(), "mieru")
	userTotals := queryMitaUserTraffic() // username → {up,down} 30d totals in bytes
	for _, id := range listMieruInstanceIDs(mdir) {
		user := readStatsUser(mdir, id)
		if user == "" {
			// Fallback: read username from instance JSON written at deploy.
			user = readMieruUsernameFromConfig(mdir, id)
		}
		if user == "" {
			continue
		}
		if c, ok := userTotals[user]; ok {
			live[id] = c
		}
	}

	// Convert live cum → [2]int64 for applyLiveDeltas.
	livePairs := map[int64][2]int64{}
	for id, c := range live {
		livePairs[id] = [2]int64{c.up, c.down}
	}
	elapsedMs := int64(0)
	if !s.lastAt.IsZero() {
		elapsedMs = now.Sub(s.lastAt).Milliseconds()
	}
	if elapsedMs < 0 {
		elapsedMs = 0
	} else if elapsedMs > 30_000 {
		elapsedMs = 30_000
	}
	return s.applyLiveDeltas(livePairs, elapsedMs)
}

// applyLiveDeltas folds live cumulative counters into per-instance deltas.
// Caller may hold no lock; this method locks s.mu.
// First observation of an id only baselines (no delta) so long-running totals
// (e.g. mita 30-day window) are not dumped on agent restart.
func (s *proxyStatsState) applyLiveDeltas(live map[int64][2]int64, elapsedMs int64) []wsproto.ProxyCounterSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[int64][2]int64{}
	}
	if s.baselined == nil {
		s.baselined = map[int64]bool{}
	}
	s.lastAt = time.Now()

	// Merge pending reAdd parks.
	type accum struct{ up, down int64 }
	merged := map[int64]*accum{}
	for id, p := range s.pending {
		if p[0] != 0 || p[1] != 0 {
			merged[id] = &accum{up: p[0], down: p[1]}
		}
	}
	s.pending = map[int64][2]int64{}

	seen := map[int64]bool{}
	for id, c := range live {
		seen[id] = true
		// First observation of an instance only baselines the cursor so a
		// long-running mita 30-day total is not dumped as a single delta on
		// agent restart / first poll.
		if !s.baselined[id] {
			s.last[id] = c
			s.baselined[id] = true
			continue
		}
		last := s.last[id]
		deltaUp := c[0] - last[0]
		deltaDown := c[1] - last[1]
		// Decreases: xray/sing-box process restart → treat as reset (delta=cur).
		// mieru 30-day window slide → only take non-negative delta (no re-bill).
		// Heuristic: if new totals are large relative to the drop, prefer
		// "window slide" (zero delta); if counters go to near-zero, prefer
		// "reset" (full re-count of current).
		if c[0] < last[0] {
			if c[0] == 0 || c[0]*2 < last[0] {
				deltaUp = c[0] // hard reset
			} else {
				deltaUp = 0 // rolling window slide
			}
		}
		if c[1] < last[1] {
			if c[1] == 0 || c[1]*2 < last[1] {
				deltaDown = c[1]
			} else {
				deltaDown = 0
			}
		}
		s.last[id] = c
		if deltaUp > 0 || deltaDown > 0 {
			a := merged[id]
			if a == nil {
				a = &accum{}
				merged[id] = a
			}
			a.up += deltaUp
			a.down += deltaDown
		}
	}
	for id := range s.last {
		if !seen[id] {
			// Instance gone — drop cursor (pending already merged above).
			if _, ok := live[id]; !ok {
				// keep pending-only ids in merged; drop last for dead instances
				if merged[id] == nil {
					delete(s.last, id)
					delete(s.baselined, id)
				}
			}
		}
	}

	var out []wsproto.ProxyCounterSample
	for id, a := range merged {
		if a.up <= 0 && a.down <= 0 {
			continue
		}
		out = append(out, wsproto.ProxyCounterSample{
			InstanceID: id,
			BytesUp:    a.up,
			BytesDown:  a.down,
			ElapsedMs:  elapsedMs,
		})
	}
	return out
}

func listInstanceIDs(coreDir string) []int64 {
	ents, err := os.ReadDir(coreDir)
	if err != nil {
		return nil
	}
	var ids []int64
	for _, e := range ents {
		name := e.Name()
		// instance-123.pid
		if !strings.HasPrefix(name, "instance-") || !strings.HasSuffix(name, ".pid") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(name, "instance-"), ".pid")
		id, err := strconv.ParseInt(mid, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		// Only live pids.
		if !pidFileAlive(filepath.Join(coreDir, name)) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// listMieruInstanceIDs lists mieru instances by config/json or statsuser files.
// mita is a single shared daemon (no per-instance pid), so we key off deploy
// artifacts written by deployMieru.
func listMieruInstanceIDs(coreDir string) []int64 {
	ents, err := os.ReadDir(coreDir)
	if err != nil {
		return nil
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, e := range ents {
		name := e.Name()
		var mid string
		switch {
		case strings.HasPrefix(name, "instance-") && strings.HasSuffix(name, ".json"):
			mid = strings.TrimSuffix(strings.TrimPrefix(name, "instance-"), ".json")
		case strings.HasPrefix(name, "instance-") && strings.HasSuffix(name, ".statsuser"):
			mid = strings.TrimSuffix(strings.TrimPrefix(name, "instance-"), ".statsuser")
		default:
			continue
		}
		id, err := strconv.ParseInt(mid, 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func readMieruUsernameFromConfig(coreDir string, instanceID int64) string {
	path := filepath.Join(coreDir, fmt.Sprintf("instance-%d.json", instanceID))
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc struct {
		Users []struct {
			Name string `json:"name"`
		} `json:"users"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return ""
	}
	if len(doc.Users) == 0 {
		return ""
	}
	return strings.TrimSpace(doc.Users[0].Name)
}

// queryMitaUserTraffic runs `mita get users` and returns 30-day upload/download
// totals per username in bytes. Soft-fails to empty map when mita is absent.
func queryMitaUserTraffic() map[string]struct{ up, down int64 } {
	out := map[string]struct{ up, down int64 }{}
	mitaPath := resolveMitaBinary()
	if mitaPath == "" {
		return out
	}
	type result struct {
		raw string
		ok  bool
	}
	ch := make(chan result, 1)
	go func() {
		cmd := exec.Command(mitaPath, "get", "users")
		b, err := cmd.CombinedOutput()
		if err != nil {
			ch <- result{}
			return
		}
		ch <- result{raw: string(b), ok: true}
	}()
	select {
	case r := <-ch:
		if !r.ok {
			return out
		}
		return parseMitaUsersTable(r.raw)
	case <-time.After(2 * time.Second):
		return out
	}
}

// parseMitaUsersTable parses `mita get users` text table:
//
//	User  LastActive  1DayDownload  1DayUpload  30DaysDownload  30DaysUpload
//	abcd  2025-…      938.1MiB      12.9MiB     4.0GiB          31.8MiB
//
// Panel "up" = client upload ≈ server 30DaysUpload; "down" = 30DaysDownload.
func parseMitaUsersTable(raw string) map[string]struct{ up, down int64 } {
	out := map[string]struct{ up, down int64 }{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Skip header.
		if strings.HasPrefix(strings.ToLower(line), "user") && strings.Contains(strings.ToLower(line), "download") {
			continue
		}
		fields := strings.Fields(line)
		// Expect: User LastActive 1DayDl 1DayUl 30DayDl 30DayUl  (≥6 cols)
		if len(fields) < 6 {
			continue
		}
		user := fields[0]
		// Last field = 30DaysUpload, second-to-last = 30DaysDownload.
		dl, ok1 := parseHumanBytes(fields[len(fields)-2])
		ul, ok2 := parseHumanBytes(fields[len(fields)-1])
		if !ok1 && !ok2 {
			continue
		}
		out[user] = struct{ up, down int64 }{up: ul, down: dl}
	}
	return out
}

// parseHumanBytes accepts 12.9MiB / 4.0GiB / 938.1KiB / 1024 / 1.5MB / 2GB.
func parseHumanBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	// Split numeric prefix and unit.
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return 0, false
	}
	numStr := s[:i]
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, false
	}
	mult := float64(1)
	switch unit {
	case "", "b", "byte", "bytes":
		mult = 1
	case "k", "kb", "kib":
		mult = 1024
	case "m", "mb", "mib":
		mult = 1024 * 1024
	case "g", "gb", "gib":
		mult = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}
	v := int64(f*mult + 0.5)
	if v < 0 {
		return 0, false
	}
	return v, true
}

func pidFileAlive(pidPath string) bool {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return false
	}
	// On Linux /proc is authoritative; elsewhere try Signal(0).
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// queryXrayInboundStats runs `xray api statsquery` against the local API port.
// inboundTag selects inbound>>>TAG>>>traffic>>>; empty tag matches any inbound.
func queryXrayInboundStats(xrayBin string, apiPort int, inboundTag string) (up, down int64, ok bool) {
	server := fmt.Sprintf("127.0.0.1:%d", apiPort)
	pattern := "inbound>>>"
	if inboundTag != "" {
		pattern = fmt.Sprintf("inbound>>>%s>>>traffic>>>", inboundTag)
	}
	ctxDone := time.After(2 * time.Second)
	type result struct {
		up, down int64
		ok       bool
	}
	ch := make(chan result, 1)
		go func() {
			args := [][]string{
				{"api", "statsquery", "--server=" + server, "-pattern", pattern},
				{"api", "statsquery", "--server=" + server, "--pattern=" + pattern},
				{"api", "statsquery", "-s", server, "-pattern", pattern},
			}
			for _, a := range args {
				cmd := exec.Command(xrayBin, a...)
				out, _ := cmd.CombinedOutput()
				u, d, parsed := parseXrayStatsQuery(string(out))
				if parsed {
					ch <- result{u, d, true}
					return
				}
			}
			ch <- result{}
		}()
	select {
	case r := <-ch:
		return r.up, r.down, r.ok
	case <-ctxDone:
		return 0, 0, false
	}
}

func jsonInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	case json.RawMessage:
		s := strings.TrimSpace(string(n))
		s = strings.Trim(s, `"`)
		i, err := strconv.ParseInt(s, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func inboundTrafficFromName(name string) (kind string, ok bool) {
	// inbound>>>vless-in>>>traffic>>>uplink
	if !strings.HasPrefix(name, "inbound>>>") || !strings.Contains(name, ">>>traffic>>>") {
		return "", false
	}
	if strings.HasSuffix(name, ">>>uplink") {
		return "up", true
	}
	if strings.HasSuffix(name, ">>>downlink") {
		return "down", true
	}
	return "", false
}

// parseXrayStatsQuery extracts inbound uplink/downlink from statsquery output.
// Xray protojson prints value as a number (`"value": 123`), not a string.
func parseXrayStatsQuery(out string) (up, down int64, ok bool) {
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, 0, false
	}
	if i := strings.Index(out, "{"); i > 0 {
		out = out[i:]
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err == nil {
		for _, key := range []string{"stat", "Stat"} {
			arr, _ := raw[key].([]any)
			for _, item := range arr {
				m, okm := item.(map[string]any)
				if !okm {
					continue
				}
				name, _ := m["name"].(string)
				if name == "" {
					name, _ = m["Name"].(string)
				}
				kind, okk := inboundTrafficFromName(name)
				if !okk {
					continue
				}
				v, okv := jsonInt64(m["value"])
				if !okv {
					v, okv = jsonInt64(m["Value"])
				}
				if !okv {
					continue
				}
				if kind == "up" {
					up = v
					ok = true
				} else {
					down = v
					ok = true
				}
			}
		}
		if ok {
			return up, down, true
		}
	}
	// Pretty-printed JSON / text: name and value may sit on adjacent lines.
	if m := xrayStatPairRe.FindAllStringSubmatch(out, -1); len(m) > 0 {
		for _, g := range m {
			n, err := strconv.ParseInt(g[3], 10, 64)
			if err != nil {
				continue
			}
			if g[2] == "uplink" {
				up = n
				ok = true
			} else {
				down = n
				ok = true
			}
		}
		if ok {
			return up, down, true
		}
	}
	if m := xrayStatLineRe.FindAllStringSubmatch(out, -1); len(m) > 0 {
		for _, g := range m {
			n, err := strconv.ParseInt(g[3], 10, 64)
			if err != nil {
				continue
			}
			if g[2] == "uplink" {
				up = n
				ok = true
			} else {
				down = n
				ok = true
			}
		}
	}
	return up, down, ok
}

// querySingBoxClashTraffic sums upload+download across clash_api /connections.
// Cumulative counters are not always available; we use total from connections
// snapshot and treat drops as reset (same wrap logic as caller).
//
// Note: clash API reports per-connection totals that reset when the connection
// closes. Summing open connections undercounts finished flows. For a better
// long-term total we also try /traffic once (non-streaming JSON if present).
// When only live connections are available, the agent still reports deltas
// between ticks for active flows — coarse but better than zero.
func querySingBoxClashTraffic(apiPort int) (up, down int64, ok bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/connections", apiPort)
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, false
	}
	var doc struct {
		Connections []struct {
			Upload   int64 `json:"upload"`
			Download int64 `json:"download"`
		} `json:"connections"`
		// Some builds expose cumulative downloadTotal/uploadTotal.
		DownloadTotal int64 `json:"downloadTotal"`
		UploadTotal   int64 `json:"uploadTotal"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, 0, false
	}
	if doc.UploadTotal > 0 || doc.DownloadTotal > 0 {
		return doc.UploadTotal, doc.DownloadTotal, true
	}
	for _, c := range doc.Connections {
		up += c.Upload
		down += c.Download
	}
	// ok even if both zero (idle) so the sampler can advance cursor.
	return up, down, true
}

// log once helper for stats inject failures (avoid spam).
var statsInjectOnce sync.Once

func logStatsInjectOnce(msg string) {
	statsInjectOnce.Do(func() {
		log.Printf("proxy stats: %s", msg)
	})
}
