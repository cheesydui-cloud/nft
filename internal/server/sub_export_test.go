package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nft/internal/db"
	"nft/internal/proxysvc"
)

func TestPublicSubAndMySubscription(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)

	node, err := db.CreateNode(d, "line-sub-1", "https://p", "tok1")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.UpdateNodeRelayHost(d, node.ID, "10.0.0.1")

	uid, userCookie := loginAsUser(t, d, 10)

	priv, pub := proxysvc.GenerateRealityKeyPair()
	cfg, _ := json.Marshal(map[string]any{
		"listen_port": 443,
		"server_name": "www.cloudflare.com",
		"private_key": priv,
		"public_key":  pub,
		"short_id":    "aabbccdd",
		"uuid":        "11111111-2222-3333-4444-555555555555",
		"security":    "reality",
		"network":     "tcp",
		"flow":        "xtls-rprx-vision",
		"fingerprint": "chrome",
	})
	svc, err := db.CreateProxyService(d, "VLESS-SUB", "vless", "xray", cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	// Subscription export requires the protocol-level grant only.
	if err := db.GrantProxyService(d, uid, svc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRule(d, &db.Rule{
		NodeID: node.ID, OwnerID: sql.NullInt64{Int64: uid, Valid: true}, Name: "sub-rule", Proto: "tcp",
		ExitHost: "9.9.9.9", ExitPort: 443, ProxyServiceID: svc.ID,
	}); err != nil {
		t.Fatal(err)
	}
	inst, err := db.UpsertProxyInstance(d, svc.ID, node.ID, 443, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	uri := "vless://11111111-2222-3333-4444-555555555555@10.0.0.1:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.cloudflare.com&fp=chrome&pbk=" + pub + "&sid=aabbccdd&type=tcp#VLESS"
	if err := db.UpdateProxyInstanceDeploy(d, inst.ID, db.ProxyDeployReady, uri, "", "1.0"); err != nil {
		t.Fatal(err)
	}

	// Session: get subscription (auto-create token)
	req := newTestRequest(http.MethodGet, "/api/my/subscription", nil)
	req.AddCookie(userCookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("my/subscription status %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["node_count"].(float64) < 1 {
		t.Fatalf("expected nodes, got %+v", body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		req2 := newTestRequest(http.MethodPost, "/api/my/subscription/refresh", nil)
		req2.AddCookie(userCookie)
		rec2 := httptest.NewRecorder()
		s.Router().ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("refresh %d %s", rec2.Code, rec2.Body.String())
		}
		_ = json.Unmarshal(rec2.Body.Bytes(), &body)
		token, _ = body["token"].(string)
	}
	if token == "" {
		t.Fatal("no token")
	}

	// Public mihomo.yaml
	req3 := newTestRequest(http.MethodGet, "/sub/"+token+"/mihomo.yaml", nil)
	rec3 := httptest.NewRecorder()
	s.Router().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("public sub %d %s", rec3.Code, rec3.Body.String())
	}
	yml := rec3.Body.String()
	if !strings.Contains(yml, "mixed-port") {
		t.Fatalf("bad yaml: %s", yml)
	}
	if !strings.Contains(yml, "type: vless") {
		t.Fatalf("yaml missing vless: %s", yml)
	}

	// Bad token → 404
	req4 := newTestRequest(http.MethodGet, "/sub/deadbeefdeadbeefdeadbeefdeadbeef/mihomo.yaml", nil)
	rec4 := httptest.NewRecorder()
	s.Router().ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d", rec4.Code)
	}

	// Ungranted user should see 0 nodes
	uid2, cookie2 := loginAsUser(t, d, 10)
	_ = uid2
	req5 := newTestRequest(http.MethodGet, "/api/my/subscription", nil)
	req5.AddCookie(cookie2)
	rec5 := httptest.NewRecorder()
	s.Router().ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusOK {
		t.Fatalf("user2 status %d", rec5.Code)
	}
	var body2 map[string]any
	_ = json.Unmarshal(rec5.Body.Bytes(), &body2)
	if body2["node_count"].(float64) != 0 {
		t.Fatalf("ungranted user should see 0 nodes, got %+v", body2)
	}
}

// Subscription-Userinfo download must report billable used (raw × billing_rate),
// matching enforceUserQuota and the account UI.
func TestSubscriptionUserinfoHeaderBillable(t *testing.T) {
	u := &db.User{TrafficUsedBytes: 1000, TrafficQuotaBytes: 10_000, BillingRate: 2.5}
	info := subscriptionUserinfoHeader(u)
	if !strings.Contains(info, "download=2500") {
		t.Fatalf("want download=2500 (billable), got %q", info)
	}
	if !strings.Contains(info, "total=10000") {
		t.Fatalf("want total=10000, got %q", info)
	}
	// rate ≤ 0 falls back to 1 (same as userBillableTraffic)
	u2 := &db.User{TrafficUsedBytes: 1000, BillingRate: 0}
	info2 := subscriptionUserinfoHeader(u2)
	if !strings.Contains(info2, "download=1000") {
		t.Fatalf("rate=0 should treat as 1, got %q", info2)
	}
}
