package daemon

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"nft/internal/forward"
	"nft/internal/wsproto"
)

// handleCounters returns per-rule counters merged across the kernel and
// userspace backends. The poller uses these for tenant traffic accounting;
// exposing them on the daemon (not on every client) keeps the data plane as
// the single source of truth.
func (d *Daemon) handleCounters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	counters, err := d.countersFn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if counters == nil {
		counters = []forward.Counter{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"counters": counters})
}

// counterSamples computes per-rule byte deltas since the last call, for the
// dialer to push to the panel. nft re-applies (flush+recreate) the table on
// every reconcile, zeroing kernel counters, so a current value below the last
// observed one is treated as a reset (delta = current).
//
// Pre-flush fold (foldCountersBeforeFlush) parks deltas from the moment just
// before a table recreate into pendingFlushDeltas; this call merges those in
// so a reconcile between ticks no longer silently drops traffic.
func (d *Daemon) counterSamples() []wsproto.CounterSample {
	cur, err := d.dp.Counters()
	if err != nil {
		log.Printf("counters: %v", err)
		return nil
	}
	now := time.Now()
	d.countersMu.Lock()
	defer d.countersMu.Unlock()
	if d.lastCounters == nil {
		d.lastCounters = map[string][2]int64{}
	}
	// Elapsed window for rate = agent sample interval (not panel wall clock).
	elapsedMs := int64(0)
	if !d.lastCounterAt.IsZero() {
		elapsedMs = now.Sub(d.lastCounterAt).Milliseconds()
	}
	d.lastCounterAt = now
	// Clamp to a sane range so a long sleep / GC pause doesn't create a spike.
	if elapsedMs < 0 {
		elapsedMs = 0
	} else if elapsedMs > 30_000 {
		elapsedMs = 30_000
	}

	// Merge any deltas folded before a kernel flush. Key format is "proto/port".
	type accum struct{ up, down int64 }
	merged := map[string]*accum{}
	for key, p := range d.pendingFlushDeltas {
		if p[0] == 0 && p[1] == 0 {
			continue
		}
		merged[key] = &accum{up: p[0], down: p[1]}
	}
	// Clear park after taking ownership under the same lock.
	d.pendingFlushDeltas = nil

	seen := make(map[string]bool, len(cur))
	for _, c := range cur {
		key := c.Proto + "/" + strconv.Itoa(c.ListenPort)
		seen[key] = true
		last := d.lastCounters[key]
		deltaUp := c.BytesUp - last[0]
		if c.BytesUp < last[0] {
			deltaUp = c.BytesUp
		}
		deltaDown := c.BytesDown - last[1]
		if c.BytesDown < last[1] {
			deltaDown = c.BytesDown
		}
		d.lastCounters[key] = [2]int64{c.BytesUp, c.BytesDown}
		if deltaUp > 0 || deltaDown > 0 {
			a := merged[key]
			if a == nil {
				a = &accum{}
				merged[key] = a
			}
			a.up += deltaUp
			a.down += deltaDown
		}
	}
	for key := range d.lastCounters {
		if !seen[key] {
			delete(d.lastCounters, key)
		}
	}

	var out []wsproto.CounterSample
	for key, a := range merged {
		if a.up <= 0 && a.down <= 0 {
			continue
		}
		// key is "proto/port"
		slash := -1
		for i := 0; i < len(key); i++ {
			if key[i] == '/' {
				slash = i
				break
			}
		}
		if slash < 0 {
			continue
		}
		port, err := strconv.Atoi(key[slash+1:])
		if err != nil {
			continue
		}
		out = append(out, wsproto.CounterSample{
			ListenPort: port,
			Proto:      key[:slash],
			BytesUp:    a.up,
			BytesDown:  a.down,
			ElapsedMs:  elapsedMs,
		})
	}
	return out
}

// foldCountersBeforeFlush samples the live dataplane and advances lastCounters
// so the deltas are preserved across a kernel table delete+recreate. Call
// under reconcileMu (and not while holding countersMu from outside).
//
// Implementation: run the same delta math as counterSamples, but instead of
// returning samples we park the deltas in pendingFlushDeltas. The next
// counterSamples() call merges them into the outbound batch, then clears the
// park. This keeps the dialer's 1s tick as the single push path while closing
// the "reconcile between ticks loses bytes" hole.
func (d *Daemon) foldCountersBeforeFlush() {
	if d.dp == nil {
		return
	}
	cur, err := d.dp.Counters()
	if err != nil {
		log.Printf("counters: pre-flush sample: %v", err)
		return
	}
	d.countersMu.Lock()
	defer d.countersMu.Unlock()
	if d.lastCounters == nil {
		d.lastCounters = map[string][2]int64{}
	}
	if d.pendingFlushDeltas == nil {
		d.pendingFlushDeltas = map[string][2]int64{}
	}
	for _, c := range cur {
		key := c.Proto + "/" + strconv.Itoa(c.ListenPort)
		last := d.lastCounters[key]
		deltaUp := c.BytesUp - last[0]
		if c.BytesUp < last[0] {
			deltaUp = c.BytesUp
		}
		deltaDown := c.BytesDown - last[1]
		if c.BytesDown < last[1] {
			deltaDown = c.BytesDown
		}
		// Cursor now matches live counters so post-flush zero reads as a reset
		// with delta=0 (not a full re-count of pre-flush totals).
		d.lastCounters[key] = [2]int64{c.BytesUp, c.BytesDown}
		if deltaUp > 0 || deltaDown > 0 {
			p := d.pendingFlushDeltas[key]
			p[0] += deltaUp
			p[1] += deltaDown
			d.pendingFlushDeltas[key] = p
		}
	}
}

// reAddCounters rewinds the sampler cursor by the given deltas after a failed
// send or counters_ack NACK so the next counterSamples() call re-reports them.
// Without this, a dropped counters frame silently discards traffic and
// undercounts quota.
//
// After foldCountersBeforeFlush the cursor may already be at the pre-flush
// total (or zero post-sample), so a plain subtract would clamp and lose the
// batch. Any remainder that cannot be rewound onto lastCounters is parked in
// pendingFlushDeltas and merged on the next sample — same path as pre-flush
// folds. Keys already pruned from lastCounters (rule removed) are parked the
// same way so a transient NACK still retransmits.
func (d *Daemon) reAddCounters(samples []wsproto.CounterSample) {
	d.countersMu.Lock()
	defer d.countersMu.Unlock()
	if d.lastCounters == nil {
		d.lastCounters = map[string][2]int64{}
	}
	if d.pendingFlushDeltas == nil {
		d.pendingFlushDeltas = map[string][2]int64{}
	}
	for _, s := range samples {
		key := s.Proto + "/" + strconv.Itoa(s.ListenPort)
		last, ok := d.lastCounters[key]
		if !ok {
			p := d.pendingFlushDeltas[key]
			p[0] += s.BytesUp
			p[1] += s.BytesDown
			d.pendingFlushDeltas[key] = p
			continue
		}
		up := last[0] - s.BytesUp
		down := last[1] - s.BytesDown
		var parkUp, parkDown int64
		// Clamp cursor at 0; park the overflow so it is not silently dropped.
		if up < 0 {
			parkUp = -up
			up = 0
		}
		if down < 0 {
			parkDown = -down
			down = 0
		}
		d.lastCounters[key] = [2]int64{up, down}
		if parkUp > 0 || parkDown > 0 {
			p := d.pendingFlushDeltas[key]
			p[0] += parkUp
			p[1] += parkDown
			d.pendingFlushDeltas[key] = p
		}
	}
}
