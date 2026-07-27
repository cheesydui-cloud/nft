package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nft/internal/db"
)

func TestNodeCFRequiresDomainAndIP(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	n, err := db.CreateNode(d, "line-ip", "https://p", "tok-ip")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, n.ID, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}

	// CF on + bare IP relay → 400
	body := map[string]any{
		"cf_sync": true, "backend_ip": "1.2.3.4",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/nodes/"+itoa(n.ID)+"/cf", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for IP relay+cf_sync, got %d %s", rec.Code, rec.Body.String())
	}

	// Domain relay but missing backend_ip → 400
	n2, err := db.CreateNode(d, "line-dom", "https://p", "tok-dom")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, n2.ID, "entry.example.com"); err != nil {
		t.Fatal(err)
	}
	body = map[string]any{
		"cf_sync": true, "backend_ip": "",
	}
	buf, _ = json.Marshal(body)
	req = newTestRequest("POST", "/api/nodes/"+itoa(n2.ID)+"/cf", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing backend_ip, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeCFWithoutTokenRecordsError(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	n, err := db.CreateNode(d, "line-cf", "https://p", "tok-cf")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, n.ID, "line.example.com"); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"cf_sync": true, "backend_ip": "203.0.113.10",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/nodes/"+itoa(n.ID)+"/cf", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save should succeed even if CF fails: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node   db.Node      `json:"node"`
		CFSync cfSyncResult `json:"cf_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.CFSync.Attempted || resp.CFSync.OK {
		t.Fatalf("cf_sync=%+v", resp.CFSync)
	}
	if !strings.Contains(resp.CFSync.Message, "Token") {
		t.Fatalf("message=%q", resp.CFSync.Message)
	}
	got, err := db.GetNode(d, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CFSync || got.BackendIP != "203.0.113.10" {
		t.Fatalf("got %+v", got)
	}
	if got.CFLastError == "" {
		t.Fatal("expected cf_last_error set")
	}
	// relay_host unchanged
	if got.RelayHost != "line.example.com" {
		t.Fatalf("relay_host=%q", got.RelayHost)
	}
}

func TestNodeCFSyncSuccessAndChangeIP(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-xxxx" {
			t.Errorf("auth=%q", auth)
		}
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/dns_records"):
			posts++
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"r1","type":"A","name":"line.example.com","content":"203.0.113.50","ttl":1,"proxied":false}}`))
		case r.Method == "PUT":
			posts++
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"r1","type":"A","name":"line.example.com","content":"203.0.113.99","ttl":1,"proxied":false}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		}
	}))
	t.Cleanup(srv.Close)

	_ = db.SetSetting(d, "cf_api_token", "test-token-xxxx")
	_ = db.SetSetting(d, "cf_api_base", srv.URL)
	_ = db.SetSetting(d, "cf_ttl", "1")

	n, err := db.CreateNode(d, "line-ok", "https://p", "tok-ok")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateNodeRelayHost(d, n.ID, "line.example.com"); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"cf_sync": true, "backend_ip": "203.0.113.50",
		"cf_zone_id": "zone99",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/nodes/"+itoa(n.ID)+"/cf", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cf: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node   db.Node      `json:"node"`
		CFSync cfSyncResult `json:"cf_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.CFSync.Attempted || !resp.CFSync.OK {
		t.Fatalf("cf_sync=%+v", resp.CFSync)
	}
	if posts < 1 {
		t.Fatal("expected CF create POST")
	}
	got, err := db.GetNode(d, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CFLastError != "" || got.CFLastIP != "203.0.113.50" || got.CFLastSyncAt == 0 {
		t.Fatalf("got %+v", got)
	}
	if got.RelayHost != "line.example.com" {
		t.Fatalf("relay_host must stay domain: %q", got.RelayHost)
	}

	// Change IP only — domain unchanged
	buf, _ = json.Marshal(map[string]any{"backend_ip": "203.0.113.99"})
	req = newTestRequest("POST", "/api/nodes/"+itoa(n.ID)+"/backend-ip", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("backend-ip: %d %s", rec.Code, rec.Body.String())
	}
	var ch struct {
		Node   db.Node      `json:"node"`
		CFSync cfSyncResult `json:"cf_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	if ch.Node.BackendIP != "203.0.113.99" || ch.Node.RelayHost != "line.example.com" {
		t.Fatalf("node backend=%q relay=%q", ch.Node.BackendIP, ch.Node.RelayHost)
	}
	if !ch.CFSync.OK {
		t.Fatalf("cf change: %+v", ch.CFSync)
	}

	// Resync
	req = newTestRequest("POST", "/api/nodes/"+itoa(n.ID)+"/cf-resync", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resync: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeCFRejectedOnComposite(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	// Create a composite via API-ish: insert node with composite type.
	res, err := d.Exec(`INSERT INTO nodes(name, node_type, address, secret, secret_hashed, roles, sort_order, created_at)
		VALUES ('combo','composite','','',0,1,1,strftime('%s','now'))`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()

	buf, _ := json.Marshal(map[string]any{"cf_sync": true, "backend_ip": "1.1.1.1"})
	req := newTestRequest("POST", "/api/nodes/"+itoa(id)+"/cf", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on composite, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestListNodesIncludesCFDefaults(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	if _, err := db.CreateNode(d, "plain", "https://p", "tok"); err != nil {
		t.Fatal(err)
	}
	req := newTestRequest("GET", "/api/nodes", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	// Must parse without scan errors after migration columns added.
	var list struct {
		Nodes []db.Node `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Nodes) < 1 {
		t.Fatal("expected at least one node")
	}
	for _, n := range list.Nodes {
		if n.CFSync {
			t.Fatalf("default cf_sync should be false for %s", n.Name)
		}
	}
}
