package server

import (
	"database/sql"
	"testing"
	"time"

	"nft/internal/db"
)

// seedLandingExit materializes one present exit row with the given ledger.
func seedLandingExit(t *testing.T, d *sql.DB, uid int64, host string, port int, quota, used int64) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO user_landing_exits(user_id, host, port, present, quota_bytes, used_bytes) VALUES (?,?,?,1,?,?)`,
		uid, host, port, quota, used); err != nil {
		t.Fatal(err)
	}
}

func TestExitQuotaExclusion(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "ex1", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	db.GrantNode(d, uid, n1.ID, 10, 0)

	// both rules exit to 8.8.8.8:443 (the test helper's fixed exit)
	createTestRuleDirectNode(t, d, uid, n1.ID)
	createTestRuleDirectNode(t, d, uid, n1.ID)

	// exhausted ledger on the exit both rules point at
	seedLandingExit(t, d, uid, "8.8.8.8", 443, 1000, 1000)
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 0 {
		t.Fatalf("rules to an exhausted exit must be excluded, got %d hops", len(hops))
	}

	// quota=0 (unlimited) never excludes, whatever the ledger reads
	d.Exec(`UPDATE user_landing_exits SET quota_bytes=0 WHERE user_id=?`, uid)
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 2 {
		t.Fatalf("unlimited exit must not exclude, got %d hops", len(hops))
	}

	// present=0 lifts the exclusion (rule reverts to ordinary billing)
	d.Exec(`UPDATE user_landing_exits SET quota_bytes=1000, present=0 WHERE user_id=?`, uid)
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 2 {
		t.Fatalf("absent exit must not exclude, got %d hops", len(hops))
	}
}

func TestExitQuotaExclusionScopedToOwnerAndExit(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	other, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "ex2", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	db.GrantNode(d, uid, n1.ID, 10, 0)
	db.GrantNode(d, other, n1.ID, 10, 0)

	mine := createTestRuleDirectNode(t, d, uid, n1.ID)
	theirs := createTestRuleDirectNode(t, d, other, n1.ID)
	// a rule of mine to a different destination
	otherExit := createTestRuleDirectNode(t, d, uid, n1.ID)
	d.Exec(`UPDATE rules SET exit_host='9.9.9.9' WHERE id=?`, otherExit)
	d.Exec(`UPDATE rule_hops SET target_host='9.9.9.9' WHERE rule_id=?`, otherExit)

	seedLandingExit(t, d, uid, "8.8.8.8", 443, 1000, 1000)

	hops, _ := db.ActiveRuleHopsForPush(d, n1.ID)
	ruleIDs := map[int64]bool{}
	for _, h := range hops {
		ruleIDs[h.RuleID] = true
	}
	if ruleIDs[mine] {
		t.Fatal("my rule to the exhausted exit must be excluded")
	}
	if !ruleIDs[theirs] {
		t.Fatal("another user's rule to the same host:port must stay active")
	}
	if !ruleIDs[otherExit] {
		t.Fatal("my rule to a different exit must stay active")
	}
}

// A chain rule whose middle hop targets the exit's host:port must still be
// excluded exactly once at the rule level (exclusion keys on rules.exit_*).
func TestExitQuotaExclusionChainRule(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "ex3", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	n2, _ := db.CreateNode(d, "ex4", "", "")
	db.UpdateNodeRelayHost(d, n2.ID, "2.2.2.2")
	db.GrantNode(d, uid, n1.ID, 10, 0)
	db.GrantNode(d, uid, n2.ID, 10, 0)
	ruleID := createTestRuleWithHops(t, d, uid, n1.ID, n2.ID)

	seedLandingExit(t, d, uid, "8.8.8.8", 443, 500, 500)
	for _, nid := range []int64{n1.ID, n2.ID} {
		hops, _ := db.ActiveRuleHopsForPush(d, nid)
		for _, h := range hops {
			if h.RuleID == ruleID {
				t.Fatalf("chain hop on node %d should be excluded", nid)
			}
		}
	}
}

// enforceExitQuota is a smoke path: the exclusion itself is covered above;
// here we assert the callback wiring finds the exceeded exit and survives
// dispatching to unconnected nodes.
func TestEnforceExitQuota(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "eq1", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	db.GrantNode(d, uid, n1.ID, 10, 0)
	createTestRuleDirectNode(t, d, uid, n1.ID)
	seedLandingExit(t, d, uid, "8.8.8.8", 443, 100, 100)

	s := newServer(t, d)
	s.enforceExitQuota(uid)

	keys, _ := db.ExitsExceedingQuota(d, uid)
	if len(keys) != 1 {
		t.Fatalf("exceeded exit expected, got %+v", keys)
	}
}


func TestExitExpiryExclusion(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "exp1", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	db.GrantNode(d, uid, n1.ID, 10, 0)
	createTestRuleDirectNode(t, d, uid, n1.ID)

	// Present exit with future expiry: rules stay.
	seedLandingExit(t, d, uid, "8.8.8.8", 443, 0, 0)
	future := time.Now().Unix() + 86400
	if _, _, err := db.SetUserLandingExitExpires(d, uid, "8.8.8.8", 443, future); err != nil {
		t.Fatal(err)
	}
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 1 {
		t.Fatalf("future expiry must keep rule, got %d hops", len(hops))
	}

	// Past expiry: excluded even while still present=1 (push-time gate).
	past := time.Now().Unix() - 60
	d.Exec(`UPDATE user_landing_exits SET expires_at=?, present=1 WHERE user_id=? AND host=? AND port=?`,
		past, uid, "8.8.8.8", 443)
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 0 {
		t.Fatalf("expired exit must exclude rules, got %d hops", len(hops))
	}

	// Setting past via API helper also flips present=0 and still excludes.
	if updated, redisp, err := db.SetUserLandingExitExpires(d, uid, "8.8.8.8", 443, past); err != nil || !updated || !redisp {
		t.Fatalf("set past: updated=%v redisp=%v err=%v", updated, redisp, err)
	}
	var present int
	_ = d.QueryRow(`SELECT present FROM user_landing_exits WHERE user_id=? AND host=? AND port=?`,
		uid, "8.8.8.8", 443).Scan(&present)
	if present != 0 {
		t.Fatalf("past expiry should set present=0, got %d", present)
	}
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 0 {
		t.Fatalf("still excluded after present=0, got %d", len(hops))
	}

	// Extend into the future restores the rule.
	if _, _, err := db.SetUserLandingExitExpires(d, uid, "8.8.8.8", 443, future); err != nil {
		t.Fatal(err)
	}
	if hops, _ := db.ActiveRuleHopsForPush(d, n1.ID); len(hops) != 1 {
		t.Fatalf("extended expiry must restore rule, got %d hops", len(hops))
	}
}

func TestExitExpiryExclusionScoped(t *testing.T) {
	d := openDB(t)
	uid, _ := loginAsUser(t, d, 100)
	other, _ := loginAsUser(t, d, 100)
	n1, _ := db.CreateNode(d, "exp2", "", "")
	db.UpdateNodeRelayHost(d, n1.ID, "1.1.1.1")
	db.GrantNode(d, uid, n1.ID, 10, 0)
	db.GrantNode(d, other, n1.ID, 10, 0)
	mine := createTestRuleDirectNode(t, d, uid, n1.ID)
	theirs := createTestRuleDirectNode(t, d, other, n1.ID)
	otherExit := createTestRuleDirectNode(t, d, uid, n1.ID)
	d.Exec(`UPDATE rules SET exit_host='9.9.9.9' WHERE id=?`, otherExit)
	d.Exec(`UPDATE rule_hops SET target_host='9.9.9.9' WHERE rule_id=?`, otherExit)

	seedLandingExit(t, d, uid, "8.8.8.8", 443, 0, 0)
	past := time.Now().Unix() - 10
	d.Exec(`UPDATE user_landing_exits SET expires_at=? WHERE user_id=? AND host=? AND port=?`,
		past, uid, "8.8.8.8", 443)

	hops, _ := db.ActiveRuleHopsForPush(d, n1.ID)
	ruleIDs := map[int64]bool{}
	for _, h := range hops {
		ruleIDs[h.RuleID] = true
	}
	if ruleIDs[mine] {
		t.Fatal("my rule to expired exit must be excluded")
	}
	if !ruleIDs[theirs] {
		t.Fatal("other user's rule must stay")
	}
	if !ruleIDs[otherExit] {
		t.Fatal("my rule to another exit must stay")
	}
}
