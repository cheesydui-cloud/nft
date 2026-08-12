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
