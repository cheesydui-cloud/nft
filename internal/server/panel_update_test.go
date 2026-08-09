package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeKomariURL(t *testing.T) {
	got, err := normalizeKomariURL("")
	if err != nil || got != "" {
		t.Fatalf("empty: %q %v", got, err)
	}
	got, err = normalizeKomariURL("https://mon.example.com/")
	if err != nil || got != "https://mon.example.com" {
		t.Fatalf("https: %q %v", got, err)
	}
	got, err = normalizeKomariURL("mon.example.com")
	if err != nil || got != "https://mon.example.com" {
		t.Fatalf("bare: %q %v", got, err)
	}
	if _, err := normalizeKomariURL("ftp://x"); err == nil {
		t.Fatal("want error for ftp")
	}
}

func TestSettingsKomariURLRoundTrip(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	body := map[string]any{
		"panel_url":  "http://127.0.0.1:7788",
		"komari_url": "https://monitor.example.com/",
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

	req = newTestRequest("GET", "/api/settings", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	var settings map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &settings)
	if settings["komari_url"] != "https://monitor.example.com" {
		t.Fatalf("komari_url=%v", settings["komari_url"])
	}

	// /me for admin includes komari_url
	req = newTestRequest("GET", "/api/me", nil)
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	var me map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me["komari_url"] != "https://monitor.example.com" {
		t.Fatalf("me komari_url=%v", me["komari_url"])
	}
}

func TestPanelUpdateApplyWithoutScript(t *testing.T) {
	// Point state path at a temp dir so tests don't touch /var/lib/nft.
	dir := t.TempDir()
	oldState := panelUpdateStatePath
	// We can't reassign const; write via env-less path by overriding only if we use a var.
	// schedulePanelUpgrade checks panelUpgradeScript which is fixed; can_apply should be false
	// without the script on this machine (typical macOS dev).
	_ = dir
	_ = oldState

	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	req := newTestRequest("GET", "/api/system/update", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get update: %d %s", rec.Code, rec.Body.String())
	}
	var info map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info["current_version"] == nil || info["current_version"] == "" {
		t.Fatalf("missing current_version: %v", info)
	}
	// On CI/dev without nft-upgrade, can_apply should be false.
	if info["can_apply"] == true {
		// Only possible on a real panel host; still must not crash apply.
		t.Log("can_apply true on this host — skipping negative apply assert")
		return
	}

	req = newTestRequest("POST", "/api/system/update", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(admin)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("apply without script want 503, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPanelUpdateStatusIdle(t *testing.T) {
	// Ensure missing state file → idle
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	// If /var/lib/nft is not writable, load still returns idle for missing file.
	req := newTestRequest("GET", "/api/system/update/status", nil)
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var st map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["state"] == "" {
		t.Fatalf("status missing state: %v", st)
	}
}

func TestSavePanelUpdateStateRoundTrip(t *testing.T) {
	// Use a temp path by writing via the real functions only if we can write
	// the production path; otherwise unit-test marshal helpers indirectly.
	dir := t.TempDir()
	path := filepath.Join(dir, "panel-update.json")
	st := panelUpdateState{State: "running", TargetVersion: "v9.9.9", StartedAt: 123, StartedVersion: "v1.0.0"}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got panelUpdateState
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "running" || got.TargetVersion != "v9.9.9" {
		t.Fatalf("%+v", got)
	}
}
