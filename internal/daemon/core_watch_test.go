package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteRunSpecAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NFT_CORE_STATE_DIR", dir)
	// Reset global map for isolation.
	globalCoreWatch = &coreWatch{
		spec:      map[string]*coreRunSpec{},
		failN:     map[string]int{},
		lastStart: map[string]time.Time{},
	}

	xdir := filepath.Join(dir, "xray")
	if err := os.MkdirAll(xdir, 0o755); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(xdir, "instance-1.pid")
	logPath := filepath.Join(xdir, "instance-1.log")
	writeRunSpec(pidPath, logPath, "/bin/true", []string{"run", "-c", "cfg.json"})

	// Disk file exists.
	if _, err := os.Stat(runspecPath(pidPath)); err != nil {
		t.Fatal(err)
	}
	// Registered in memory.
	if len(globalCoreWatch.snapshot()) != 1 {
		t.Fatalf("want 1 registered, got %d", len(globalCoreWatch.snapshot()))
	}

	// Simulate agent restart: clear memory, reload from disk.
	globalCoreWatch = &coreWatch{
		spec:      map[string]*coreRunSpec{},
		failN:     map[string]int{},
		lastStart: map[string]time.Time{},
	}
	loadRunSpecsFromDisk()
	snaps := globalCoreWatch.snapshot()
	if len(snaps) != 1 {
		t.Fatalf("reload: want 1, got %d", len(snaps))
	}
	if snaps[0].Binary != "/bin/true" || snaps[0].PidPath != pidPath {
		t.Fatalf("unexpected spec: %+v", snaps[0])
	}

	removeRunSpec(pidPath)
	if len(globalCoreWatch.snapshot()) != 0 {
		t.Fatal("unregister failed")
	}
	if _, err := os.Stat(runspecPath(pidPath)); !os.IsNotExist(err) {
		t.Fatalf("runspec file should be gone: %v", err)
	}
}

func TestStopPIDFileDropsRunSpec(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NFT_CORE_STATE_DIR", dir)
	globalCoreWatch = &coreWatch{
		spec:      map[string]*coreRunSpec{},
		failN:     map[string]int{},
		lastStart: map[string]time.Time{},
	}
	xdir := filepath.Join(dir, "xray")
	_ = os.MkdirAll(xdir, 0o755)
	pidPath := filepath.Join(xdir, "instance-2.pid")
	logPath := filepath.Join(xdir, "instance-2.log")
	writeRunSpec(pidPath, logPath, "/bin/true", []string{})
	// No live process — stop should still drop runspec.
	stopPIDFile(pidPath)
	if len(globalCoreWatch.snapshot()) != 0 {
		t.Fatal("stopPIDFile should unregister")
	}
}
