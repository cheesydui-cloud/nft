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

func TestNodeRepoPureIPStillWorks(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	body := map[string]any{
		"name": "pro-664", "protocol": "ss",
		"host": "68.252.208.113", "port": 4865,
		"uri": "", "remark": "", "expires_at": 0, "group_id": 0,
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create pure IP: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	node, _ := resp["node"].(map[string]any)
	if node == nil {
		t.Fatalf("want node in response, got %v", resp)
	}
	if node["host"] != "68.252.208.113" {
		t.Fatalf("host=%v", node["host"])
	}
	if node["cf_sync"] == true {
		t.Fatal("cf_sync should be false by default")
	}

	// list still returns the row
	req = newTestRequest("GET", "/api/node-repo", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Nodes []db.NodeRepoEntry `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Nodes) != 1 || list.Nodes[0].Host != "68.252.208.113" {
		t.Fatalf("list=%+v", list.Nodes)
	}
}

func TestNodeRepoCFSyncRequiresDomainAndIP(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	// CF on + bare IP host → 400
	body := map[string]any{
		"name": "x", "protocol": "ss",
		"host": "1.2.3.4", "port": 443,
		"cf_sync": true, "backend_ip": "1.2.3.4",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for IP+cf_sync, got %d %s", rec.Code, rec.Body.String())
	}

	// CF on + domain but missing backend_ip → 400
	body = map[string]any{
		"name": "x", "protocol": "ss",
		"host": "home.example.com", "port": 443,
		"cf_sync": true, "backend_ip": "",
	}
	buf, _ = json.Marshal(body)
	req = newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing backend_ip, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeRepoCFSyncWithoutTokenRecordsError(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	body := map[string]any{
		"name": "ddns-1", "protocol": "ss",
		"host": "home.example.com", "port": 443,
		"cf_sync": true, "backend_ip": "203.0.113.10",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create should succeed even if CF fails: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node   db.NodeRepoEntry `json:"node"`
		CFSync cfSyncResult     `json:"cf_sync"`
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
	// Row persisted with error
	got, err := db.GetNodeRepoEntry(d, resp.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CFSync || got.BackendIP != "203.0.113.10" {
		t.Fatalf("got %+v", got)
	}
	if got.CFLastError == "" {
		t.Fatal("expected cf_last_error set")
	}
}

func TestSettingsCFTokenMaskAndClear(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	// save token
	body := map[string]any{
		"panel_url": "http://127.0.0.1:7788",
		"cf_api_token": "abcd1234efgh5678",
		"cf_zone_name": "example.com",
		"cf_ttl": 1,
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/settings", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	// get must not leak full token
	req = newTestRequest("GET", "/api/settings", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var settings map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &settings)
	if settings["cf_token_configured"] != true {
		t.Fatalf("settings=%v", settings)
	}
	raw, _ := json.Marshal(settings)
	if strings.Contains(string(raw), "abcd1234efgh5678") {
		t.Fatal("full token leaked in GET /settings")
	}
	stored, _ := db.GetSetting(d, "cf_api_token")
	if stored != "abcd1234efgh5678" {
		t.Fatalf("stored=%q", stored)
	}

	// clear
	body = map[string]any{"panel_url": "http://127.0.0.1:7788", "cf_clear_token": true}
	buf, _ = json.Marshal(body)
	req = newTestRequest("POST", "/api/settings", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = db.GetSetting(d, "cf_api_token")
	if stored != "" {
		t.Fatalf("token not cleared: %q", stored)
	}
}

func TestNodeRepoDomainWithoutCF(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	body := map[string]any{
		"name": "dom", "protocol": "ss",
		"host": "landing.example.net", "port": 8443,
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create domain: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node   db.NodeRepoEntry `json:"node"`
		CFSync cfSyncResult     `json:"cf_sync"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Node.Host != "landing.example.net" || resp.Node.CFSync {
		t.Fatalf("node=%+v", resp.Node)
	}
	if resp.CFSync.Attempted {
		t.Fatalf("should skip CF: %+v", resp.CFSync)
	}
}


func TestNodeRepoCFSyncSuccessWithMockAPI(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"r1","type":"A","name":"home.example.com","content":"203.0.113.50","ttl":1,"proxied":false}}`))
		case r.Method == "PUT":
			posts++
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"r1","type":"A","name":"home.example.com","content":"203.0.113.99","ttl":1,"proxied":false}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
		}
	}))
	t.Cleanup(srv.Close)

	_ = db.SetSetting(d, "cf_api_token", "test-token-xxxx")
	_ = db.SetSetting(d, "cf_api_base", srv.URL)
	_ = db.SetSetting(d, "cf_ttl", "1")

	body := map[string]any{
		"name": "cf-ok", "protocol": "ss",
		"host": "home.example.com", "port": 443,
		"cf_sync": true, "backend_ip": "203.0.113.50",
		"cf_zone_id": "zone99",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Node   db.NodeRepoEntry `json:"node"`
		CFSync cfSyncResult     `json:"cf_sync"`
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
	got, err := db.GetNodeRepoEntry(d, resp.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CFLastError != "" || got.CFLastIP != "203.0.113.50" || got.CFLastSyncAt == 0 {
		t.Fatalf("got %+v", got)
	}

	// IP-only update: domain host unchanged → endpoint_changed false, CF updates
	body["backend_ip"] = "203.0.113.99"
	buf, _ = json.Marshal(body)
	req = newTestRequest("PATCH", "/api/node-repo/"+itoa(resp.Node.ID), bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var up struct {
		EndpointChanged bool             `json:"endpoint_changed"`
		Node            db.NodeRepoEntry `json:"node"`
		CFSync          cfSyncResult     `json:"cf_sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if up.EndpointChanged {
		t.Fatal("backend_ip-only must not cascade endpoint")
	}
	if !up.CFSync.OK {
		t.Fatalf("cf patch: %+v", up.CFSync)
	}
	if up.Node.BackendIP != "203.0.113.99" {
		t.Fatalf("backend_ip=%q", up.Node.BackendIP)
	}
}

func TestNodeRepoChangeIPAndProbe(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/dns_records") {
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"r1","type":"A","name":"p1.example.com","content":"1.1.1.1","ttl":1,"proxied":false}]}`))
			return
		}
		if r.Method == "PUT" || r.Method == "POST" {
			_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"r1","type":"A","name":"p1.example.com","content":"2.2.2.2","ttl":1,"proxied":false}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
	}))
	t.Cleanup(srv.Close)
	_ = db.SetSetting(d, "cf_api_token", "tok")
	_ = db.SetSetting(d, "cf_api_base", srv.URL)

	// create domain entry
	body := map[string]any{
		"name": "p1", "protocol": "ss", "host": "p1.example.com", "port": 443,
		"cf_sync": true, "backend_ip": "1.1.1.1", "cf_zone_id": "z1",
	}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/node-repo", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Node db.NodeRepoEntry `json:"node"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created.Node.ID

	// change IP only
	buf, _ = json.Marshal(map[string]any{"backend_ip": "2.2.2.2"})
	req = newTestRequest("POST", "/api/node-repo/"+itoa(id)+"/backend-ip", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("backend-ip %d %s", rec.Code, rec.Body.String())
	}
	var ch struct {
		Node   db.NodeRepoEntry `json:"node"`
		CFSync cfSyncResult     `json:"cf_sync"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Node.BackendIP != "2.2.2.2" || ch.Node.Host != "p1.example.com" {
		t.Fatalf("node=%+v", ch.Node)
	}
	if !ch.CFSync.OK {
		t.Fatalf("cf=%+v", ch.CFSync)
	}

	// resync
	req = newTestRequest("POST", "/api/node-repo/"+itoa(id)+"/cf-resync", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("resync %d %s", rec.Code, rec.Body.String())
	}

	// probe literal path for pure IP entry
	n2, err := db.CreateNodeRepoEntry(d, "iponly", "ss", "9.9.9.9", 80, "", "", 0, "", db.NodeRepoCFFields{})
	if err != nil {
		t.Fatal(err)
	}
	req = newTestRequest("GET", "/api/node-repo/"+itoa(n2.ID)+"/probe-dns", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("probe %d %s", rec.Code, rec.Body.String())
	}
	var probe map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &probe)
	if probe["status"] != "literal_ip" {
		t.Fatalf("probe=%v", probe)
	}
}
