package daemon

import (
	"os"
	"testing"
	"time"
)

func TestSystemdWatchdogIntervalEmpty(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	if got := systemdWatchdogInterval(); got != 0 {
		t.Fatalf("want 0, got %v", got)
	}
}

func TestSystemdWatchdogIntervalHalf(t *testing.T) {
	// 60s watchdog → 30s ping interval.
	t.Setenv("WATCHDOG_USEC", "60000000")
	got := systemdWatchdogInterval()
	want := 30 * time.Second
	if got != want {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestSdNotifyNoSocketIsNoop(t *testing.T) {
	os.Unsetenv("NOTIFY_SOCKET")
	// Must not panic when unsupervised.
	sdNotify("READY=1")
	sdNotify("WATCHDOG=1")
}
