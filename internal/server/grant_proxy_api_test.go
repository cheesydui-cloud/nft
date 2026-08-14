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
}
