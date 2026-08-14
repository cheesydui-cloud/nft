package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"nft/internal/db"
)

func TestGrantProxyServiceDoesNotGrantNode(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)
	uid, err := db.CreateUser(d, "proxy-only", "x", "user")
	if err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateNode(d, "线路7", "https://p", "tok")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := db.CreateProxyService(d, "线路7-vless", db.ProxyProtoVLESS, db.ProxyCoreXray, []byte(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProxyInstance(d, svc.ID, node.ID, 443, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"proxy_service_ids": []int64{svc.ID}})
	req := newTestRequest(http.MethodPost, "/api/users/"+strconv.FormatInt(uid, 10)+"/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant status %d %s", rec.Code, rec.Body.String())
	}

	if !db.HasProxyServiceGrant(d, uid, svc.ID) {
		t.Fatal("expected protocol grant")
	}
	if _, err := db.GetNodeGrant(d, uid, node.ID); err == nil {
		t.Fatal("proxy grant must not write user_nodes / 单点")
	}

	req = newTestRequest(http.MethodGet, "/api/users/"+strconv.FormatInt(uid, 10), nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get user %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Nodes           []struct{ ID int64 } `json:"nodes"`
		ProxyServiceIDs []int64              `json:"proxy_service_ids"`
		GrantedProxyIDs []int64              `json:"granted_proxy_node_ids"`
		GrantedSvcs     []struct {
			ID              int64   `json:"id"`
			Name            string  `json:"name"`
			Protocol        string  `json:"protocol"`
			DeployedNodeIDs []int64 `json:"deployed_node_ids"`
		} `json:"granted_proxy_services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 0 {
		t.Fatalf("单点 must stay empty, got %+v", got.Nodes)
	}
	if len(got.ProxyServiceIDs) != 1 || got.ProxyServiceIDs[0] != svc.ID {
		t.Fatalf("proxy_service_ids = %+v", got.ProxyServiceIDs)
	}
	if len(got.GrantedProxyIDs) != 1 || got.GrantedProxyIDs[0] != node.ID {
		t.Fatalf("granted_proxy_node_ids = %+v", got.GrantedProxyIDs)
	}
	if len(got.GrantedSvcs) != 1 || got.GrantedSvcs[0].ID != svc.ID {
		t.Fatalf("granted_proxy_services = %+v", got.GrantedSvcs)
	}
	if len(got.GrantedSvcs[0].DeployedNodeIDs) != 1 || got.GrantedSvcs[0].DeployedNodeIDs[0] != node.ID {
		t.Fatalf("deployed_node_ids = %+v", got.GrantedSvcs[0].DeployedNodeIDs)
	}
}

func TestProtocolOnlyGrantCanCreateRule(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)
	g, err := db.CreateNode(d, "线路7", "https://p", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, g.ID, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	svc, err := db.CreateProxyService(d, "线路7-vless", db.ProxyProtoVLESS, db.ProxyCoreXray, []byte(`{}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProxyInstance(d, svc.ID, g.ID, 443, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	uid, cookie := loginAsUser(t, d, 10)
	if err := db.GrantProxyService(d, uid, svc.ID); err != nil {
		t.Fatal(err)
	}

	// Plain L4 without 单点 grant is still rejected.
	plain, _ := json.Marshal(map[string]any{
		"node_id": g.ID, "name": "plain", "proto": "tcp", "exit": "9.9.9.9:8443",
	})
	req := newTestRequest(http.MethodPost, "/api/my/rules", bytes.NewReader(plain))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain node without grant: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Protocol entry on the deployed node is allowed.
	body, _ := json.Marshal(map[string]any{
		"node_id": g.ID, "name": "vless-rule", "proto": "tcp", "exit": "9.9.9.9:8443",
		"proxy_service_id": svc.ID,
	})
	req = newTestRequest(http.MethodPost, "/api/my/rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("protocol rule: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rules, _ := db.ListRulesByUser(d, uid)
	if len(rules) != 1 || rules[0].ProxyServiceID != svc.ID {
		t.Fatalf("want 1 protocol rule, got %+v", rules)
	}

	// Admin acting for the same user.
	adminBody, _ := json.Marshal(map[string]any{
		"node_id": g.ID, "owner_id": uid, "name": "admin-vless", "proto": "tcp",
		"exit": "8.8.8.8:443", "proxy_service_id": svc.ID,
	})
	req = newTestRequest(http.MethodPost, "/api/rules", bytes.NewReader(adminBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin-for-user: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
