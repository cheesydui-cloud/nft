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
	now := time.Now()
	sc.nodes[1] = &nodeSpeedState{
		lastSeen: now,
		hops: map[string]*hopState{
			"tcp/1000": {upBps: 100, downBps: 200, ownerID: 100, lastTime: now},
			"tcp/1001": {upBps: 30, downBps: 40, ownerID: 200, lastTime: now},
			"tcp/1002": {upBps: 5, downBps: 6, ownerID: 100, lastTime: now},
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
	now := time.Now()
	sc.nodes[10] = &nodeSpeedState{
		lastSeen: now,
		hops: map[string]*hopState{
			// Entry hop for rule 61 (tcp + udp)
			"tcp/2000": {upBps: 1000, downBps: 2000, ownerID: 7, ruleID: 61, hopPos: 0, lastTime: now},
			"udp/2000": {upBps: 100, downBps: 50, ownerID: 7, ruleID: 61, hopPos: 0, lastTime: now},
			// Middle hop of the same rule must not be added on top of entry
			"tcp/2001": {upBps: 9999, downBps: 9999, ownerID: 7, ruleID: 61, hopPos: 1, lastTime: now},
			// Another user's rule
			"tcp/3000": {upBps: 10, downBps: 20, ownerID: 8, ruleID: 99, hopPos: 0, lastTime: now},
		},
	}
	// Only middle hop reporting for rule 70 (composite entry offline)
	sc.nodes[11] = &nodeSpeedState{
		lastSeen: now,
		hops: map[string]*hopState{
			"tcp/4000": {upBps: 50, downBps: 60, ownerID: 7, ruleID: 70, hopPos: 1, lastTime: now},
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

// Idle hops must not keep their last bps just because another port on the same
// node is still reporting (node lastSeen stays fresh). This is the "one live
// stream makes every rule on the relay show ~300KB/s" bug.
func TestSpeedCacheIdleHopZeroed(t *testing.T) {
	sc := newSpeedCache()
	now := time.Now()
	stale := now.Add(-hopIdleTTL - time.Second)
	sc.nodes[1] = &nodeSpeedState{
		lastSeen: now, // node still "alive" via the busy hop
		hops: map[string]*hopState{
			// Busy rule 10 — recent sample
			"tcp/1000": {upBps: 300_000, downBps: 300_000, ownerID: 1, ruleID: 10, hopPos: 0, lastTime: now},
			// Idle rule 11 — last rate stuck, but no sample within hopIdleTTL
			"tcp/1001": {upBps: 300_000, downBps: 300_000, ownerID: 1, ruleID: 11, hopPos: 0, lastTime: stale},
		},
	}

	rules := entryByRule(sc.snapshotRules())
	if _, ok := rules[11]; ok {
		t.Fatalf("idle rule 11 must not appear in rule snapshot, got %+v", rules[11])
	}
	if got := rules[10]; got.Up != 300_000 || got.Down != 300_000 {
		t.Fatalf("busy rule 10: got up=%d down=%d", got.Up, got.Down)
	}

	nodes := entryByNode(sc.snapshot())
	if got := nodes[1]; got.Up != 300_000 || got.Down != 300_000 {
		t.Fatalf("node total must exclude idle hop: got up=%d down=%d, want 300000", got.Up, got.Down)
	}
}

// Agent ElapsedMs must drive bps so rates match the sample window (router-like),
// not a stretched panel wall-clock gap.
func TestSpeedCacheUsesAgentElapsed(t *testing.T) {
	sc := newSpeedCache()
	// 1s window, 100_000 bytes up → 100_000 B/s
	sc.update(1, []counterDelta{{
		proto: "tcp", listenPortStr: "1000",
		bytesUp: 100_000, bytesDown: 200_000, elapsedSec: 1.0,
		ownerID: 1, ruleID: 10, hopPos: 0,
	}})
	got := entryByNode(sc.snapshot())[1]
	if got.Up != 100_000 || got.Down != 200_000 {
		t.Fatalf("agent elapsed rate: up=%d down=%d want 100000/200000", got.Up, got.Down)
	}
}

// Empty counter batch (agent heartbeat with no traffic) must zero sticky rates.
func TestSpeedCacheEmptyBatchZeros(t *testing.T) {
	sc := newSpeedCache()
	sc.update(1, []counterDelta{{
		proto: "tcp", listenPortStr: "1000",
		bytesUp: 500_000, bytesDown: 500_000, elapsedSec: 1.0,
		ownerID: 1, ruleID: 10, hopPos: 0,
	}})
	if got := entryByNode(sc.snapshot())[1]; got.Up == 0 {
		t.Fatal("expected non-zero before empty batch")
	}
	sc.update(1, nil) // empty heartbeat
	nodes := entryByNode(sc.snapshot())
	if got, ok := nodes[1]; ok && (got.Up != 0 || got.Down != 0) {
		t.Fatalf("empty batch must zero rates, got up=%d down=%d", got.Up, got.Down)
	}
	if _, ok := entryByRule(sc.snapshotRules())[10]; ok {
		t.Fatal("zeroed rule must not appear in rule snapshot")
	}
}

// A busy port in the batch must not leave other ports on the same node sticky.
func TestSpeedCacheMissingPortZeroed(t *testing.T) {
	sc := newSpeedCache()
	sc.update(1, []counterDelta{
		{proto: "tcp", listenPortStr: "1000", bytesUp: 100_000, bytesDown: 100_000, elapsedSec: 1, ownerID: 1, ruleID: 10, hopPos: 0},
		{proto: "tcp", listenPortStr: "1001", bytesUp: 50_000, bytesDown: 50_000, elapsedSec: 1, ownerID: 1, ruleID: 11, hopPos: 0},
	})
	// Only port 1000 still has traffic.
	sc.update(1, []counterDelta{
		{proto: "tcp", listenPortStr: "1000", bytesUp: 80_000, bytesDown: 80_000, elapsedSec: 1, ownerID: 1, ruleID: 10, hopPos: 0},
	})
	rules := entryByRule(sc.snapshotRules())
	if _, ok := rules[11]; ok {
		t.Fatalf("port 1001 not in batch must zero rule 11, got %+v", rules[11])
	}
	if got := rules[10]; got.Up != 80_000 {
		t.Fatalf("rule 10: up=%d want 80000", got.Up)
	}
}
