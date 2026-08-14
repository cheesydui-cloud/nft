package server

import (
	"testing"

	"nft/internal/db"
	"nft/internal/wsproto"
)

func TestApplyProxyCounters(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	node, err := db.CreateNode(d, "proxy-traffic-n", "https://p", "tok-pt")
	if err != nil {
		t.Fatal(err)
	}
	cfg := []byte(`{"listen_port":443}`)
	svc, err := db.CreateProxyService(d, "PT", "vless", "xray", cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := db.UpsertProxyInstance(d, svc.ID, node.ID, 443, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	ok, msg := s.Hub.applyProxyCounters(node.ID, []wsproto.ProxyCounterSample{
		{InstanceID: inst.ID, BytesUp: 1000, BytesDown: 2000},
	})
	if !ok {
		t.Fatalf("apply failed: %s", msg)
	}
	got, err := db.GetProxyInstance(d, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrafficUpBytes != 1000 || got.TrafficDownBytes != 2000 {
		t.Fatalf("traffic = %d/%d, want 1000/2000", got.TrafficUpBytes, got.TrafficDownBytes)
	}
	// Wrong node must not bill.
	ok2, _ := s.Hub.applyProxyCounters(node.ID+999, []wsproto.ProxyCounterSample{
		{InstanceID: inst.ID, BytesUp: 50, BytesDown: 0},
	})
	// skip-not-on-node is still overall OK (batch continues); ledger unchanged
	if !ok2 {
		// If begin fails only — accept either
	}
	got2, _ := db.GetProxyInstance(d, inst.ID)
	if got2.TrafficUpBytes != 1000 {
		t.Fatalf("wrong-node sample must not add, got up=%d", got2.TrafficUpBytes)
	}
}

func TestApplyProxyCountersRuleCoreBillsUser(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	node, err := db.CreateNode(d, "proxy-core-n", "https://p", "tok-pc")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, node.ID, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	uid, _ := loginAsUser(t, d, 10)
	if err := db.GrantNode(d, uid, node.ID, 10, 0); err != nil {
		t.Fatal(err)
	}
	svc, err := db.CreateProxyService(d, "PC", "vless", "xray", []byte(`{"uuid":"11111111-1111-4111-8111-111111111111"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	ruleID := createTestRuleDirectNode(t, d, uid, node.ID)
	rl, err := db.GetRule(d, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	rl.ProxyServiceID = svc.ID
	if err := db.UpdateRuleHeader(d, rl); err != nil {
		t.Fatal(err)
	}
	seedLandingExit(t, d, uid, "8.8.8.8", 443, 0, 0)

	ok, msg := s.Hub.applyProxyCounters(node.ID, []wsproto.ProxyCounterSample{
		{InstanceID: ruleCoreInstanceID(ruleID), BytesUp: 400, BytesDown: 600, ElapsedMs: 1000},
	})
	if !ok {
		t.Fatalf("apply failed: %s", msg)
	}
	u, err := db.GetUserByID(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if u.TrafficUsedBytes != 1000 || u.TotalTrafficUsedBytes != 1000 {
		t.Fatalf("user used=%d/%d want 1000/1000", u.TrafficUsedBytes, u.TotalTrafficUsedBytes)
	}
	got, err := db.GetRule(d, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitBytes != 1000 {
		t.Fatalf("rule exit_bytes=%d want 1000", got.ExitBytes)
	}
	g, err := db.GetNodeGrant(d, uid, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.TrafficUsedBytes != 1000 {
		t.Fatalf("grant used=%d want 1000", g.TrafficUsedBytes)
	}
	if used := exitUsed(t, d, uid); used != 1000 {
		t.Fatalf("landing used=%d want 1000", used)
	}
	// L4 heartbeat must not wipe proxy speed.
	s.Hub.applyCounters(node.ID, nil)
	rules := s.Hub.speedCache.snapshotRulesForUser(uid)
	found := false
	for _, e := range rules {
		if e.RuleID == ruleID {
			found = true
			if e.Up == 0 && e.Down == 0 {
				t.Fatalf("rule speed wiped by L4 heartbeat: %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("expected per-rule speed from protocol core")
	}
	nodes := s.Hub.speedCache.snapshotForUser(uid)
	if len(nodes) == 0 || (nodes[0].Up == 0 && nodes[0].Down == 0) {
		t.Fatalf("expected node speed, got %+v", nodes)
	}
}

func TestApplyProxyCountersPublishedUniqueRuleBillsUser(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	node, err := db.CreateNode(d, "proxy-pub-n", "https://p", "tok-pp")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, node.ID, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	uid, _ := loginAsUser(t, d, 10)
	if err := db.GrantNode(d, uid, node.ID, 10, 0); err != nil {
		t.Fatal(err)
	}
	svc, err := db.CreateProxyService(d, "PP", "vless", "xray", []byte(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := db.UpsertProxyInstance(d, svc.ID, node.ID, 443, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	ruleID := createTestRuleDirectNode(t, d, uid, node.ID)
	rl, err := db.GetRule(d, ruleID)
	if err != nil {
		t.Fatal(err)
	}
	rl.ProxyServiceID = svc.ID
	if err := db.UpdateRuleHeader(d, rl); err != nil {
		t.Fatal(err)
	}

	ok, msg := s.Hub.applyProxyCounters(node.ID, []wsproto.ProxyCounterSample{
		{InstanceID: inst.ID, BytesUp: 100, BytesDown: 200, ElapsedMs: 1000},
	})
	if !ok {
		t.Fatalf("apply failed: %s", msg)
	}
	u, _ := db.GetUserByID(d, uid)
	if u.TrafficUsedBytes != 300 {
		t.Fatalf("unique published rule should bill user, got %d", u.TrafficUsedBytes)
	}
	got, _ := db.GetProxyInstance(d, inst.ID)
	if got.TrafficUpBytes != 100 || got.TrafficDownBytes != 200 {
		t.Fatalf("instance ledger = %d/%d", got.TrafficUpBytes, got.TrafficDownBytes)
	}

	// Second owner on the same service+node: no longer uniquely attributable.
	uid2, _ := loginAsUser(t, d, 10)
	if err := db.GrantNode(d, uid2, node.ID, 10, 0); err != nil {
		t.Fatal(err)
	}
	rule2 := createTestRuleDirectNode(t, d, uid2, node.ID)
	rl2, _ := db.GetRule(d, rule2)
	rl2.ProxyServiceID = svc.ID
	if err := db.UpdateRuleHeader(d, rl2); err != nil {
		t.Fatal(err)
	}
	ok, msg = s.Hub.applyProxyCounters(node.ID, []wsproto.ProxyCounterSample{
		{InstanceID: inst.ID, BytesUp: 50, BytesDown: 50, ElapsedMs: 1000},
	})
	if !ok {
		t.Fatalf("apply2 failed: %s", msg)
	}
	u, _ = db.GetUserByID(d, uid)
	if u.TrafficUsedBytes != 300 {
		t.Fatalf("shared inbound must not keep billing first user, got %d", u.TrafficUsedBytes)
	}
	u2, _ := db.GetUserByID(d, uid2)
	if u2.TrafficUsedBytes != 0 {
		t.Fatalf("shared inbound must not bill second user, got %d", u2.TrafficUsedBytes)
	}
	got, _ = db.GetProxyInstance(d, inst.ID)
	if got.TrafficUpBytes != 150 || got.TrafficDownBytes != 250 {
		t.Fatalf("instance should still accumulate, got %d/%d", got.TrafficUpBytes, got.TrafficDownBytes)
	}
}
