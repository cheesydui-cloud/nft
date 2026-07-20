package db

import "testing"

func TestApplyStaleOnlinePolicyDemotesStale(t *testing.T) {
	d := openTestDB(t)
	nid := createTestNode(t, d, "stale-node")
	// Force online + ancient last_seen.
	if _, err := d.Exec(`UPDATE nodes SET online=1, last_seen=? WHERE id=?`, now()-nodeStaleOnlineTTL-10, nid); err != nil {
		t.Fatal(err)
	}
	nodes, err := ListNodes(d)
	if err != nil {
		t.Fatal(err)
	}
	ApplyStaleOnlinePolicy(d, nodes)
	var found bool
	for _, n := range nodes {
		if n.ID == nid {
			found = true
			if n.Online != 0 {
				t.Fatalf("want online=0 after stale demotion, got %d", n.Online)
			}
		}
	}
	if !found {
		t.Fatal("node missing from list")
	}
	// Persisted.
	got, err := GetNode(d, nid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Online != 0 {
		t.Fatalf("persisted online want 0, got %d", got.Online)
	}
}

func TestApplyStaleOnlinePolicyKeepsFresh(t *testing.T) {
	d := openTestDB(t)
	nid := createTestNode(t, d, "fresh-node")
	if _, err := d.Exec(`UPDATE nodes SET online=1, last_seen=? WHERE id=?`, now(), nid); err != nil {
		t.Fatal(err)
	}
	nodes, err := ListNodes(d)
	if err != nil {
		t.Fatal(err)
	}
	ApplyStaleOnlinePolicy(d, nodes)
	for _, n := range nodes {
		if n.ID == nid && n.Online != 1 {
			t.Fatalf("fresh node demoted unexpectedly, online=%d", n.Online)
		}
	}
}

func TestMarkAllAgentNodesOffline(t *testing.T) {
	d := openTestDB(t)
	nid := createTestNode(t, d, "boot-clear")
	if _, err := d.Exec(`UPDATE nodes SET online=1, last_seen=? WHERE id=?`, now(), nid); err != nil {
		t.Fatal(err)
	}
	if err := MarkAllAgentNodesOffline(d); err != nil {
		t.Fatal(err)
	}
	got, err := GetNode(d, nid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Online != 0 {
		t.Fatalf("want online=0 after boot clear, got %d", got.Online)
	}
}

func TestTouchNodeLastSeen(t *testing.T) {
	d := openTestDB(t)
	nid := createTestNode(t, d, "ping-touch")
	old := now() - 30
	if _, err := d.Exec(`UPDATE nodes SET online=1, last_seen=? WHERE id=?`, old, nid); err != nil {
		t.Fatal(err)
	}
	if err := TouchNodeLastSeen(d, nid); err != nil {
		t.Fatal(err)
	}
	got, err := GetNode(d, nid)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeen == nil || *got.LastSeen <= old {
		t.Fatalf("last_seen not refreshed: old=%d got=%v", old, got.LastSeen)
	}
}
