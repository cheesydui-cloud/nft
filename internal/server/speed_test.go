package server

import (
	"testing"
	"time"
)

// A node carries hops from several users. The node-total snapshot sums every
// hop, while a per-user snapshot keeps only the requesting user's hops so a
// user sees their own throughput on the node, not everyone's.
func TestSpeedCacheSnapshotPerUser(t *testing.T) {
	sc := newSpeedCache()
	sc.nodes[1] = &nodeSpeedState{
		lastSeen: time.Now(),
		hops: map[string]*hopState{
			"tcp/1000": {upBps: 100, downBps: 200, ownerID: 100},
			"tcp/1001": {upBps: 30, downBps: 40, ownerID: 200},
			"tcp/1002": {upBps: 5, downBps: 6, ownerID: 100},
		},
	}

	total := entryByNode(sc.snapshot())
	if got := total[1]; got.Up != 135 || got.Down != 246 {
		t.Fatalf("node total: got up=%d down=%d, want up=135 down=246", got.Up, got.Down)
	}

	u100 := entryByNode(sc.snapshotForUser(100))
	if got := u100[1]; got.Up != 105 || got.Down != 206 {
		t.Fatalf("user 100 on node: got up=%d down=%d, want up=105 down=206", got.Up, got.Down)
	}

	u200 := entryByNode(sc.snapshotForUser(200))
	if got := u200[1]; got.Up != 30 || got.Down != 40 {
		t.Fatalf("user 200 on node: got up=%d down=%d, want up=30 down=40", got.Up, got.Down)
	}

	// A user with no hop on the node gets no entry rather than a zero row.
	if _, ok := entryByNode(sc.snapshotForUser(999))[1]; ok {
		t.Fatalf("user 999 should have no entry on node 1")
	}
}

// Per-rule snapshot prefers the entry hop (pos 0). Middle hops of the same
// rule must not double-count. Multi-proto samples at the same pos are summed.
// When only a middle hop is reporting (entry offline), that hop is used.
func TestSpeedCacheSnapshotRules(t *testing.T) {
	sc := newSpeedCache()
	sc.nodes[10] = &nodeSpeedState{
		lastSeen: time.Now(),
		hops: map[string]*hopState{
			// Entry hop for rule 61 (tcp + udp)
			"tcp/2000": {upBps: 1000, downBps: 2000, ownerID: 7, ruleID: 61, hopPos: 0},
			"udp/2000": {upBps: 100, downBps: 50, ownerID: 7, ruleID: 61, hopPos: 0},
			// Middle hop of the same rule must not be added on top of entry
			"tcp/2001": {upBps: 9999, downBps: 9999, ownerID: 7, ruleID: 61, hopPos: 1},
			// Another user's rule
			"tcp/3000": {upBps: 10, downBps: 20, ownerID: 8, ruleID: 99, hopPos: 0},
		},
	}
	// Only middle hop reporting for rule 70 (composite entry offline)
	sc.nodes[11] = &nodeSpeedState{
		lastSeen: time.Now(),
		hops: map[string]*hopState{
			"tcp/4000": {upBps: 50, downBps: 60, ownerID: 7, ruleID: 70, hopPos: 1},
		},
	}

	all := entryByRule(sc.snapshotRules())
	if got := all[61]; got.Up != 1100 || got.Down != 2050 {
		t.Fatalf("rule 61: got up=%d down=%d, want up=1100 down=2050", got.Up, got.Down)
	}
	if got := all[99]; got.Up != 10 || got.Down != 20 {
		t.Fatalf("rule 99: got up=%d down=%d, want up=10 down=20", got.Up, got.Down)
	}
	if got := all[70]; got.Up != 50 || got.Down != 60 {
		t.Fatalf("rule 70 fallback to middle hop: got up=%d down=%d", got.Up, got.Down)
	}

	u7 := entryByRule(sc.snapshotRulesForUser(7))
	if _, ok := u7[99]; ok {
		t.Fatalf("user 7 must not see rule 99")
	}
	if got := u7[61]; got.Up != 1100 || got.Down != 2050 {
		t.Fatalf("user 7 rule 61: got up=%d down=%d", got.Up, got.Down)
	}
}

func entryByNode(entries []SpeedEntry) map[int64]SpeedEntry {
	m := map[int64]SpeedEntry{}
	for _, e := range entries {
		m[e.NodeID] = e
	}
	return m
}

func entryByRule(entries []RuleSpeedEntry) map[int64]RuleSpeedEntry {
	m := map[int64]RuleSpeedEntry{}
	for _, e := range entries {
		m[e.RuleID] = e
	}
	return m
}
