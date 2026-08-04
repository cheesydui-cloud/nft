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

func TestNormalizeMigratePanelURL(t *testing.T) {
	got, err := normalizeMigratePanelURL("https://panel.example.com/")
	if err != nil || got != "https://panel.example.com" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = normalizeMigratePanelURL("wss://panel.example.com/v1/agents")
	if err != nil || got != "https://panel.example.com" {
		t.Fatalf("wss path strip: %q err=%v", got, err)
	}
	got, err = normalizeMigratePanelURL("1.2.3.4:7788")
	if err != nil || got != "https://1.2.3.4:7788" {
		t.Fatalf("bare: %q err=%v", got, err)
	}
	if _, err := normalizeMigratePanelURL(""); err == nil {
		t.Fatal("empty should fail")
	}
}

func TestMigrateExportDownload(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)
	_, _ = db.CreateUser(d, "u1", "hash", "user")

	req := newTestRequest("GET", "/api/migrate/export", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "octet-stream") {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Body.Len() < 100 {
		t.Fatalf("body too small: %d", rec.Body.Len())
	}
	// Snapshot must open as sqlite with our user
	// (VACUUM INTO produces a full db file)
}

func TestMigrateRedirectAndClear(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	body := map[string]any{"panel_url": "https://new.example.com:8443"}
	buf, _ := json.Marshal(body)
	req := newTestRequest("POST", "/api/migrate/redirect", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("redirect %d %s", rec.Code, rec.Body.String())
	}
	pending, _ := db.GetSetting(d, "pending_panel_redirect_url")
	if pending != "https://new.example.com:8443" {
		t.Fatalf("pending=%q", pending)
	}
	panel, _ := db.GetSetting(d, "panel_url")
	if panel != "https://new.example.com:8443" {
		t.Fatalf("panel_url=%q", panel)
	}

	req = newTestRequest("GET", "/api/migrate/status", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["pending_panel_redirect_url"] != "https://new.example.com:8443" {
		t.Fatalf("status pending=%v", st["pending_panel_redirect_url"])
	}

	req = newTestRequest("POST", "/api/migrate/clear-pending", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear %d %s", rec.Code, rec.Body.String())
	}
	pending, _ = db.GetSetting(d, "pending_panel_redirect_url")
	if pending != "" {
		t.Fatalf("pending after clear=%q", pending)
	}
}

func TestMigrateRedirectRejectsEmpty(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)
	buf, _ := json.Marshal(map[string]any{"panel_url": ""})
	req := newTestRequest("POST", "/api/migrate/redirect", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}
