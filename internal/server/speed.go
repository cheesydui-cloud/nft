package server

import (
	"sort"
	"sync"
	"time"
)

// SpeedEntry holds the instantaneous throughput for a single node,
// computed from the most recent counter batch the agent reported.
type SpeedEntry struct {
	NodeID int64 `json:"node_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
	TS     int64 `json:"ts"`
}

// RuleSpeedEntry is the same shape keyed by rule, so a rules table can show
// that rule's own live rate even when rules.node_id is a composite (which
// never appears as a physical agent node in the node-level speed map).
type RuleSpeedEntry struct {
	RuleID int64 `json:"rule_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
	TS     int64 `json:"ts"`
}

// hopIdleTTL is how long a per-port hop may keep its last measured bps after
// the agent stops reporting that port (or reports a zero-window for it).
// Agents now push counters every ~1s including empty batches; keep this short
// so idle rates collapse quickly instead of sticking near the last peak.
const hopIdleTTL = 3 * time.Second

// nodeStaleTTL drops a node from snapshots when no counter batch arrived.
const nodeStaleTTL = 30 * time.Second

// speedCache aggregates per-node throughput derived from agent counter
// batches. Entries older than nodeStaleTTL are excluded from snapshots so
// disconnected nodes fade out automatically. Individual hops also zero out
// after hopIdleTTL without a sample so idle ports don't keep a stale rate.
type speedCache struct {
	mu sync.RWMutex
	// per node: aggregated speed
	nodes map[int64]*nodeSpeedState
}

type nodeSpeedState struct {
	lastSeen time.Time
	hops     map[string]*hopState
}

type hopState struct {
	lastUp   int64
	lastDown int64
	lastTime time.Time
	upBps    float64
	downBps  float64
	// ownerID attributes this hop's throughput to the user who owns its rule,
	// so a per-user snapshot can show a user only their own share of the node.
	// 0 means no owner (an admin-created rule with no owner).
	ownerID int64
	// ruleID tags this hop to its logical rule so a rules table can look up
	// live rate by rule id (composite rules.node_id never reports itself).
	ruleID int64
	// hopPos is the hop's position in the rule chain (-1 unknown). The per-rule
	// snapshot prefers position 0 (entry) and otherwise picks the lowest pos
	// that is reporting, so multi-hop chains are never summed hop-by-hop.
	hopPos int
}

func newSpeedCache() *speedCache {
	return &speedCache{nodes: map[int64]*nodeSpeedState{}}
}

type counterDelta struct {
	proto         string
	listenPortStr string
	bytesUp       int64
	bytesDown     int64
	// elapsedSec is the agent-measured sample window when provided; 0 means
	// fall back to panel wall-clock gap between updates for this hop.
	elapsedSec float64
	ownerID    int64
	ruleID     int64
	hopPos     int
}

// update folds a counter batch into the cache. The bytes/sec rate prefers the
// agent-supplied sample window (ElapsedMs); otherwise it uses wall-clock time
// since the previous batch for this hop. An empty samples slice is a heartbeat:
// all hops on this node are zeroed so idle rates do not stick.
// The first non-empty batch for a new hop is skipped (no rate without a prior
// reference when only wall-clock is available and elapsedSec is 0).
func (sc *speedCache) update(nodeID int64, samples []counterDelta) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	ns, ok := sc.nodes[nodeID]
	if !ok {
		ns = &nodeSpeedState{hops: map[string]*hopState{}}
		sc.nodes[nodeID] = ns
	}
	now := time.Now()
	ns.lastSeen = now

	// Empty batch = agent is alive but no port had a non-zero delta this tick.
	// Zero every hop immediately so UI matches "idle" instead of hopIdleTTL stickiness.
	if len(samples) == 0 {
		for _, hs := range ns.hops {
			hs.upBps = 0
			hs.downBps = 0
			hs.lastTime = now
		}
		return
	}

	seen := make(map[string]bool, len(samples))
	for _, s := range samples {
		key := s.proto + "/" + s.listenPortStr
		seen[key] = true
		hs, ok := ns.hops[key]
		if !ok {
			hs = &hopState{lastTime: now, hopPos: -1}
			ns.hops[key] = hs
		}
		// A listen port can be reassigned to a different user's rule between
		// batches, so the owner/rule/pos are refreshed every sample rather
		// than only on creation.
		hs.ownerID = s.ownerID
		hs.ruleID = s.ruleID
		hs.hopPos = s.hopPos

		elapsed := s.elapsedSec
		if elapsed <= 0 {
			elapsed = now.Sub(hs.lastTime).Seconds()
		}
		// Need a real window. Skip the first wall-clock sample for a brand-new hop
		// (elapsed from lastTime==now is ~0) unless agent sent ElapsedMs.
		if elapsed > 0.05 {
			// Cap pathological gaps (reconnect backlog) so one late frame cannot
			// report a multi-second average that looks "stuck low" vs router.
			if elapsed > 10 {
				elapsed = 10
			}
			hs.upBps = float64(s.bytesUp) / elapsed
			hs.downBps = float64(s.bytesDown) / elapsed
		} else if s.bytesUp == 0 && s.bytesDown == 0 {
			hs.upBps = 0
			hs.downBps = 0
		}
		hs.lastTime = now
	}
	// Ports not in this non-empty batch had zero delta this tick — zero them.
	for key, hs := range ns.hops {
		if !seen[key] {
			hs.upBps = 0
			hs.downBps = 0
			hs.lastTime = now
		}
	}
}

// snapshot returns the node-total throughput for every node updated within the
// last 30 s (all owners summed), sorted by node ID for deterministic output.
// This is the admin/aggregate view.
func (sc *speedCache) snapshot() []SpeedEntry {
	return sc.snapshotFiltered(func(*hopState) bool { return true })
}

// snapshotForUser returns per-node throughput counting only hops owned by the
// given user, so a user sees their own share of each node rather than its
// total. A node where the user has no active hop is omitted entirely (no zero
// row), matching how the dashboard treats a missing entry as "idle".
func (sc *speedCache) snapshotForUser(userID int64) []SpeedEntry {
	return sc.snapshotFiltered(func(hs *hopState) bool { return hs.ownerID == userID })
}

// snapshotRules returns per-rule throughput from entry hops only (ruleID set),
// for every rule with a recent sample. Admin view: all owners.
func (sc *speedCache) snapshotRules() []RuleSpeedEntry {
	return sc.snapshotRulesFiltered(func(*hopState) bool { return true })
}

// snapshotRulesForUser is the per-rule view restricted to one owner's entry hops.
func (sc *speedCache) snapshotRulesForUser(userID int64) []RuleSpeedEntry {
	return sc.snapshotRulesFiltered(func(hs *hopState) bool { return hs.ownerID == userID })
}

// hopRate returns the hop's live bps, or zeros when the hop has gone idle
// (no sample within hopIdleTTL). Agents omit zero-delta ports, so lastTime
// is the only signal that traffic on this listen port has stopped.
func hopRate(hs *hopState, now time.Time) (up, down float64) {
	if hs == nil {
		return 0, 0
	}
	if hs.lastTime.IsZero() || now.Sub(hs.lastTime) > hopIdleTTL {
		return 0, 0
	}
	return hs.upBps, hs.downBps
}

// snapshotFiltered aggregates each node's hops that pass keep into one entry,
// dropping nodes not seen in the last nodeStaleTTL and nodes with no matching hop.
// Idle hops (no sample within hopIdleTTL) contribute 0 so one busy port cannot
// keep a stale rate on every other port of the same node.
func (sc *speedCache) snapshotFiltered(keep func(*hopState) bool) []SpeedEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	now := time.Now()
	cutoff := now.Add(-nodeStaleTTL)
	out := make([]SpeedEntry, 0, len(sc.nodes))
	for nid, ns := range sc.nodes {
		if ns.lastSeen.Before(cutoff) {
			continue
		}
		var totalUp, totalDown float64
		matched := false
		for _, hs := range ns.hops {
			if !keep(hs) {
				continue
			}
			matched = true
			up, down := hopRate(hs, now)
			totalUp += up
			totalDown += down
		}
		if !matched {
			continue
		}
		out = append(out, SpeedEntry{
			NodeID: nid,
			Up:     int64(totalUp),
			Down:   int64(totalDown),
			TS:     ns.lastSeen.UnixMilli(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// snapshotRulesFiltered builds one rate per rule. For each rule it picks the
// lowest hop position that is currently reporting (entry preferred) and sums
// multi-proto samples at that same position only — never every hop of a chain.
// Stale nodes drop out after nodeStaleTTL; idle hops (no sample within
// hopIdleTTL) contribute 0 so a busy port on the same relay cannot inflate
// every other rule's live rate.
func (sc *speedCache) snapshotRulesFiltered(keep func(*hopState) bool) []RuleSpeedEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	now := time.Now()
	cutoff := now.Add(-nodeStaleTTL)
	type agg struct {
		up, down float64
		ts       int64
		pos      int
		hasPos   bool
	}
	byRule := map[int64]*agg{}
	for _, ns := range sc.nodes {
		if ns.lastSeen.Before(cutoff) {
			continue
		}
		for _, hs := range ns.hops {
			if hs.ruleID <= 0 || !keep(hs) {
				continue
			}
			up, down := hopRate(hs, now)
			// Skip fully-idle hops entirely so a rule with only stale ports
			// does not appear in the map with a fake 0 row forever — the UI
			// treats a missing rule_id as idle.
			if up == 0 && down == 0 {
				continue
			}
			pos := hs.hopPos
			if pos < 0 {
				pos = 1 << 20 // unknown pos sorts last; still usable as fallback
			}
			ts := hs.lastTime.UnixMilli()
			if ts <= 0 {
				ts = ns.lastSeen.UnixMilli()
			}
			a := byRule[hs.ruleID]
			if a == nil {
				a = &agg{pos: pos, hasPos: true}
				byRule[hs.ruleID] = a
			}
			if !a.hasPos || pos < a.pos {
				// Switch to a better (closer-to-entry) hop; reset totals.
				a.up, a.down = up, down
				a.pos = pos
				a.hasPos = true
				a.ts = ts
				continue
			}
			if pos == a.pos {
				// Same logical hop, multi-proto: sum tcp+udp.
				a.up += up
				a.down += down
				if ts > a.ts {
					a.ts = ts
				}
			}
		}
	}
	out := make([]RuleSpeedEntry, 0, len(byRule))
	for rid, a := range byRule {
		out = append(out, RuleSpeedEntry{
			RuleID: rid,
			Up:     int64(a.up),
			Down:   int64(a.down),
			TS:     a.ts,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleID < out[j].RuleID })
	return out
}
