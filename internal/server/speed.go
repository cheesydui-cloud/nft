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

// speedCache aggregates per-node throughput derived from agent counter
// batches. Entries older than 30 seconds are excluded from snapshots so
// disconnected nodes fade out automatically.
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
	ownerID       int64
	ruleID        int64
	hopPos        int
}

// update folds a counter batch into the cache. The bytes/sec rate is
// derived from the elapsed time since the previous batch for this hop;
// the first batch is skipped (no rate without a prior reference point).
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
	for _, s := range samples {
		key := s.proto + "/" + s.listenPortStr
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
		elapsed := now.Sub(hs.lastTime).Seconds()
		if elapsed > 0.5 {
			hs.upBps = float64(s.bytesUp) / elapsed
			hs.downBps = float64(s.bytesDown) / elapsed
		}
		hs.lastTime = now
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

// snapshotFiltered aggregates each node's hops that pass keep into one entry,
// dropping nodes not seen in the last 30 s and nodes with no matching hop.
func (sc *speedCache) snapshotFiltered(keep func(*hopState) bool) []SpeedEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	cutoff := time.Now().Add(-30 * time.Second)
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
			totalUp += hs.upBps
			totalDown += hs.downBps
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
// Stale nodes drop out after 30 s with no sample.
func (sc *speedCache) snapshotRulesFiltered(keep func(*hopState) bool) []RuleSpeedEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	cutoff := time.Now().Add(-30 * time.Second)
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
		ts := ns.lastSeen.UnixMilli()
		for _, hs := range ns.hops {
			if hs.ruleID <= 0 || !keep(hs) {
				continue
			}
			pos := hs.hopPos
			if pos < 0 {
				pos = 1 << 20 // unknown pos sorts last; still usable as fallback
			}
			a := byRule[hs.ruleID]
			if a == nil {
				a = &agg{pos: pos, hasPos: true}
				byRule[hs.ruleID] = a
			}
			if !a.hasPos || pos < a.pos {
				// Switch to a better (closer-to-entry) hop; reset totals.
				a.up, a.down = hs.upBps, hs.downBps
				a.pos = pos
				a.hasPos = true
				a.ts = ts
				continue
			}
			if pos == a.pos {
				// Same logical hop, multi-proto: sum tcp+udp.
				a.up += hs.upBps
				a.down += hs.downBps
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
