package db

import (
	"database/sql"
	"testing"
)

func TestProxyServiceGrantIndependentOfSiblings(t *testing.T) {
	d := openTestDB(t)
	uid := createTestUser(t, d)
	nid := createTestNode(t, d, "NB.JP")

	vless, err := CreateProxyService(d, "测试1", ProxyProtoVLESS, ProxyCoreXray, []byte(`{}`), false)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := CreateProxyService(d, "测试2", ProxyProtoShadowsocks, ProxyCoreSingBox, []byte(`{}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertProxyInstance(d, vless.ID, nid, 443, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertProxyInstance(d, ss.ID, nid, 8388, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}

	// Grant only VLESS. Node grant is independent and must not be implied.
	if err := GrantProxyService(d, uid, vless.ID); err != nil {
		t.Fatal(err)
	}

	if !HasProxyServiceGrant(d, uid, vless.ID) {
		t.Fatal("expected VLESS grant")
	}
	if HasProxyServiceGrant(d, uid, ss.ID) {
		t.Fatal("SS must NOT be granted when only VLESS was selected")
	}

	ids, err := ListProxyServiceIDsForUser(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != vless.ID {
		t.Fatalf("want only vless id, got %v", ids)
	}

	// Node still hosts both, but ListProxyServiceNodeIDs for granted set is VLESS only.
	nids, err := ListProxyServiceNodeIDs(d, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(nids) != 1 || nids[0] != nid {
		t.Fatalf("want node %d, got %v", nid, nids)
	}

	if err := RevokeProxyService(d, uid, vless.ID); err != nil {
		t.Fatal(err)
	}
	if HasProxyServiceGrant(d, uid, vless.ID) {
		t.Fatal("revoke should clear grant")
	}
	if _, err := GetNodeGrant(d, uid, nid); err == nil {
		t.Fatal("protocol grant must not create a node grant")
	}
}

func TestListSubVisibleReadyInstancesDoesNotNeedNodeGrant(t *testing.T) {
	d := openTestDB(t)
	uid := createTestUser(t, d)
	nid := createTestNode(t, d, "线路7")
	svc, err := CreateProxyService(d, "线路7-vless", ProxyProtoVLESS, ProxyCoreXray, []byte(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := UpsertProxyInstance(d, svc.ID, nid, 443, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateProxyInstanceDeploy(d, inst.ID, ProxyDeployReady, "vless://x@1.2.3.4:443", "", "1"); err != nil {
		t.Fatal(err)
	}
	if err := GrantProxyService(d, uid, svc.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := ListSubVisibleReadyInstancesForUser(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("grant without an enabled rule must not export, got %+v", rows)
	}
	if _, err := CreateRule(d, &Rule{
		NodeID: nid, OwnerID: sql.NullInt64{Int64: uid, Valid: true}, Name: "r1", Proto: "tcp",
		ExitHost: "9.9.9.9", ExitPort: 443, ProxyServiceID: svc.ID,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = ListSubVisibleReadyInstancesForUser(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ServiceID != svc.ID {
		t.Fatalf("want one instance from grant+rule, got %+v", rows)
	}
	if _, err := GetNodeGrant(d, uid, nid); err == nil {
		t.Fatal("export must not require or create a node grant")
	}
}

func TestUserMayUseRuleEntryProtocolOnly(t *testing.T) {
	d := openTestDB(t)
	uid := createTestUser(t, d)
	nid := createTestNode(t, d, "线路7")
	svc, err := CreateProxyService(d, "线路7-vless", ProxyProtoVLESS, ProxyCoreXray, []byte(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertProxyInstance(d, svc.ID, nid, 443, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := GrantProxyService(d, uid, svc.ID); err != nil {
		t.Fatal(err)
	}
	if UserMayUseRuleEntry(d, uid, nid, 0) {
		t.Fatal("plain L4 must still require user_nodes")
	}
	if !UserMayUseRuleEntry(d, uid, nid, svc.ID) {
		t.Fatal("protocol grant on deployed node must authorize 代理 entry")
	}
	if UserMayUseRuleEntry(d, uid, nid, svc.ID+99) {
		t.Fatal("ungranted service must be rejected")
	}
}

func TestActiveProxyClientsFollowEnabledRules(t *testing.T) {
	d := openTestDB(t)
	uid := createTestUser(t, d)
	nid := createTestNode(t, d, "线路7")
	svc, err := CreateProxyService(d, "线路7-vless", ProxyProtoVLESS, ProxyCoreXray, []byte(`{"uuid":"11111111-1111-4111-8111-111111111111"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertProxyInstance(d, svc.ID, nid, 443, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := GrantProxyService(d, uid, svc.ID); err != nil {
		t.Fatal(err)
	}
	if err := InsertUserProxyCred(d, uid, svc.ID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "", ""); err != nil {
		t.Fatal(err)
	}

	clients, err := ListActiveProxyClients(d, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("no rule → no inbound client, got %+v", clients)
	}

	rid, err := CreateRule(d, &Rule{
		NodeID: nid, OwnerID: sql.NullInt64{Int64: uid, Valid: true}, Name: "r1", Proto: "tcp",
		ExitHost: "9.9.9.9", ExitPort: 443, ProxyServiceID: svc.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	clients, err = ListActiveProxyClients(d, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 1 || clients[0].UserID != uid || clients[0].UUID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("want user cred after rule create, got %+v", clients)
	}

	if _, err := DeleteRule(d, rid); err != nil {
		t.Fatal(err)
	}
	clients, err = ListActiveProxyClients(d, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("delete last rule must drop inbound client, got %+v", clients)
	}

	rid, err = CreateRule(d, &Rule{
		NodeID: nid, OwnerID: sql.NullInt64{Int64: uid, Valid: true}, Name: "r2", Proto: "tcp",
		ExitHost: "9.9.9.9", ExitPort: 443, ProxyServiceID: svc.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetUserDisabled(d, uid, true, "管理员手动禁用"); err != nil {
		t.Fatal(err)
	}
	clients, err = ListActiveProxyClients(d, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("disabled user must drop inbound client, got %+v", clients)
	}
	if UserHasActiveProxyRule(d, uid, svc.ID) {
		t.Fatal("disabled user must not have an active proxy rule")
	}
	_ = rid
}
