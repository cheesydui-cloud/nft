package db

import "testing"

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

	// Grant only VLESS (and its node for caps).
	if err := GrantNode(d, uid, nid, 10, 0); err != nil {
		t.Fatal(err)
	}
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
	// Node grant remains.
	if _, err := GetNodeGrant(d, uid, nid); err != nil {
		t.Fatal("node grant must survive service revoke")
	}
}
