package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// coreRunSpec is the durable recipe for a detached proxy-core process
// (xray / sing-box). Written next to the pid file so an agent restart can
// re-adopt instances and keep them alive without a panel re-publish.
type coreRunSpec struct {
	Binary  string   `json:"binary"`
	Args    []string `json:"args"`
	PidPath string   `json:"pid_path"`
	LogPath string   `json:"log_path"`
}

// coreWatch tracks live supervised instances and crash-loop backoff.
type coreWatch struct {
	mu   sync.Mutex
	spec map[string]*coreRunSpec // key = pidPath
	// failAt records consecutive failure times for exponential backoff.
	failN map[string]int
	// lastStart avoids thrashing when restart itself is slow.
	lastStart map[string]time.Time
}

var globalCoreWatch = &coreWatch{
	spec:      map[string]*coreRunSpec{},
	failN:     map[string]int{},
	lastStart: map[string]time.Time{},
}

func runspecPath(pidPath string) string {
	return strings.TrimSuffix(pidPath, filepath.Ext(pidPath)) + ".runspec.json"
}

func writeRunSpec(pidPath, logPath, binary string, args []string) {
	sp := coreRunSpec{
		Binary:  binary,
		Args:    append([]string(nil), args...),
		PidPath: pidPath,
		LogPath: logPath,
	}
	b, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(runspecPath(pidPath), b, 0o600)
	globalCoreWatch.register(&sp)
}

func removeRunSpec(pidPath string) {
	_ = os.Remove(runspecPath(pidPath))
	globalCoreWatch.unregister(pidPath)
}

func (w *coreWatch) register(sp *coreRunSpec) {
	if sp == nil || sp.PidPath == "" || sp.Binary == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := *sp
	cp.Args = append([]string(nil), sp.Args...)
	w.spec[sp.PidPath] = &cp
	// Successful (re)deploy resets crash-loop counter.
	delete(w.failN, sp.PidPath)
}

func (w *coreWatch) unregister(pidPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.spec, pidPath)
	delete(w.failN, pidPath)
	delete(w.lastStart, pidPath)
}

func (w *coreWatch) snapshot() []*coreRunSpec {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*coreRunSpec, 0, len(w.spec))
	for _, sp := range w.spec {
		cp := *sp
		cp.Args = append([]string(nil), sp.Args...)
		out = append(out, &cp)
	}
	return out
}

// loadRunSpecsFromDisk scans xray/sing-box instance runspecs under coreStateDir
// and registers them so the watchdog can keep orphaned cores alive after agent restart.
func loadRunSpecsFromDisk() {
	root := coreStateDir()
	for _, sub := range []string{"xray", "sing-box"} {
		dir := filepath.Join(root, sub)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".runspec.json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			var sp coreRunSpec
			if err := json.Unmarshal(b, &sp); err != nil || sp.PidPath == "" || sp.Binary == "" {
				continue
			}
			globalCoreWatch.register(&sp)
		}
	}
}

// startCoreWatchdog periodically ensures every registered core process is alive.
// Dead processes are restarted with exponential backoff (cap 2m) so a bad config
// does not spin the CPU; a successful live period resets the counter.
func startCoreWatchdog(ctx context.Context) {
	loadRunSpecsFromDisk()
	// Immediate pass so agent restart brings cores back without waiting a tick.
	globalCoreWatch.tick()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			globalCoreWatch.tick()
		}
	}
}

func (w *coreWatch) tick() {
	for _, sp := range w.snapshot() {
		w.ensure(sp)
	}
}

func (w *coreWatch) ensure(sp *coreRunSpec) {
	pid := readPIDFile(sp.PidPath)
	if pid > 1 && pidAlive(pid) {
		// Healthy: decay fail counter slowly so occasional blips don't stick.
		w.mu.Lock()
		if n := w.failN[sp.PidPath]; n > 0 {
			// Only clear after process has been up past lastStart + 30s.
			if ls, ok := w.lastStart[sp.PidPath]; !ok || time.Since(ls) > 30*time.Second {
				delete(w.failN, sp.PidPath)
			}
		}
		w.mu.Unlock()
		return
	}

	w.mu.Lock()
	n := w.failN[sp.PidPath]
	// Backoff: 2s, 4s, 8s … cap 120s.
	backoff := time.Duration(1<<min(n, 6)) * 2 * time.Second
	if backoff > 2*time.Minute {
		backoff = 2 * time.Minute
	}
	if ls, ok := w.lastStart[sp.PidPath]; ok && time.Since(ls) < backoff {
		w.mu.Unlock()
		return
	}
	w.lastStart[sp.PidPath] = time.Now()
	w.mu.Unlock()

	log.Printf("core-watch: restarting dead core pidfile=%s binary=%s fails=%d", sp.PidPath, sp.Binary, n)
	if err := restartDetachedTracked(sp.PidPath, sp.LogPath, sp.Binary, sp.Args...); err != nil {
		log.Printf("core-watch: restart failed %s: %v", sp.PidPath, err)
		w.mu.Lock()
		w.failN[sp.PidPath] = n + 1
		w.mu.Unlock()
		return
	}
	// Don't wipe failN immediately — wait for 30s healthy window above.
	// But do register so subsequent ticks see the new pid.
	writeRunSpec(sp.PidPath, sp.LogPath, sp.Binary, sp.Args)
}

func readPIDFile(pidPath string) int {
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}
