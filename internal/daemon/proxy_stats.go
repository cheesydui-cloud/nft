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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nft/internal/wsproto"
)

// proxyStats tracks cumulative up/down per instance for delta samples.
type proxyStatsState struct {
	mu       sync.Mutex
	last     map[int64][2]int64 // instanceID → {up, down} cumulative
	lastAt   time.Time
	pending  map[int64][2]int64 // reAdd park
}

var globalProxyStats = &proxyStatsState{
	last:    map[int64][2]int64{},
	pending: map[int64][2]int64{},
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

	// mieru/mita: no stable per-instance stats API yet — skip (0).

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[int64][2]int64{}
	}
	elapsedMs := int64(0)
	if !s.lastAt.IsZero() {
		elapsedMs = now.Sub(s.lastAt).Milliseconds()
	}
	s.lastAt = now
	if elapsedMs < 0 {
		elapsedMs = 0
	} else if elapsedMs > 30_000 {
		elapsedMs = 30_000
	}

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
		last := s.last[id]
		deltaUp := c.up - last[0]
		if c.up < last[0] {
			deltaUp = c.up // process restart / counter reset
		}
		deltaDown := c.down - last[1]
		if c.down < last[1] {
			deltaDown = c.down
		}
		s.last[id] = [2]int64{c.up, c.down}
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
// Returns cumulative uplink/downlink for the named inbound tag.
func queryXrayInboundStats(xrayBin string, apiPort int, inboundTag string) (up, down int64, ok bool) {
	// xray api statsquery --server=127.0.0.1:PORT -pattern "inbound>>>TAG>>>traffic>>>"
	server := fmt.Sprintf("127.0.0.1:%d", apiPort)
	pattern := fmt.Sprintf("inbound>>>%s>>>traffic>>>", inboundTag)
	ctxDone := time.After(2 * time.Second)
	type result struct {
		up, down int64
		ok       bool
	}
	ch := make(chan result, 1)
	go func() {
		cmd := exec.Command(xrayBin, "api", "statsquery", "--server="+server, "-pattern", pattern)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// Soft-fail: binary may lack api subcommand or instance still starting.
			ch <- result{}
			return
		}
		u, d, parsed := parseXrayStatsQuery(string(out))
		ch <- result{u, d, parsed}
	}()
	select {
	case r := <-ch:
		return r.up, r.down, r.ok
	case <-ctxDone:
		return 0, 0, false
	}
}

// parseXrayStatsQuery extracts uplink/downlink from statsquery text/JSON output.
// Accepts both JSON (`"name":"...uplink","value":"123"`) and plain text forms.
func parseXrayStatsQuery(out string) (up, down int64, ok bool) {
	// Prefer JSON array form.
	var doc struct {
		Stat []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err == nil && len(doc.Stat) > 0 {
		for _, s := range doc.Stat {
			v, _ := strconv.ParseInt(s.Value, 10, 64)
			if strings.HasSuffix(s.Name, ">>>uplink") {
				up = v
				ok = true
			}
			if strings.HasSuffix(s.Name, ">>>downlink") {
				down = v
				ok = true
			}
		}
		return up, down, ok
	}
	// Text fallback: lines containing uplink/downlink and a number.
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		low := strings.ToLower(line)
		// pull last integer on the line
		fields := strings.Fields(line)
		var num int64
		for i := len(fields) - 1; i >= 0; i-- {
			if n, err := strconv.ParseInt(strings.Trim(fields[i], `",`), 10, 64); err == nil {
				num = n
				break
			}
		}
		if strings.Contains(low, "uplink") {
			up = num
			ok = true
		}
		if strings.Contains(low, "downlink") {
			down = num
			ok = true
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
