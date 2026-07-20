package daemon

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// systemdWatchdogInterval returns half of WATCHDOG_USEC when the process is
// supervised by systemd with WatchdogSec= set. A zero duration means either
// we are not under systemd, or watchdog is disabled — callers should skip
// the notify loop in that case.
func systemdWatchdogInterval() time.Duration {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	// systemd expects periodic WATCHDOG=1 well before the full period.
	// Half the configured interval is the conventional recommendation.
	return time.Duration(n) * time.Microsecond / 2
}

// sdNotify sends a single NOTIFY_SOCKET datagram (READY / WATCHDOG / STOPPING).
// No-op and silent when NOTIFY_SOCKET is unset (non-systemd runs, tests).
func sdNotify(state string) {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" || state == "" {
		return
	}
	// Abstract namespace sockets are "@name" in the env; dial wants "\x00name".
	if addr[0] == '@' {
		addr = "\x00" + addr[1:]
	}
	c, err := net.Dial("unixgram", addr)
	if err != nil {
		// One-shot failure is fine to log once per call site; do not spam.
		log.Printf("sd_notify: dial: %v", err)
		return
	}
	defer c.Close()
	if _, err := fmt.Fprint(c, state); err != nil {
		log.Printf("sd_notify: write: %v", err)
	}
}

// runSystemdWatchdog pings systemd's watchdog until ctx/stop fires. Call from
// Daemon.Run so a hung agent (deadlock, stuck syscall) is restarted by
// systemd instead of sitting "active" forever with Restart=always alone
// (which only restarts on exit).
func runSystemdWatchdog(stop <-chan struct{}) {
	interval := systemdWatchdogInterval()
	if interval <= 0 {
		return
	}
	// READY=1 once so Type=notify units (and Type=simple with NotifyAccess)
	// mark the service as started. Harmless under Type=simple.
	sdNotify("READY=1")

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			sdNotify("STOPPING=1")
			return
		case <-t.C:
			sdNotify("WATCHDOG=1")
		}
	}
}
