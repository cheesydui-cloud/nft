package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginSecFetchSite(t *testing.T) {
	req := httptest.NewRequest("POST", "http://panel.example/api/x", nil)
	req.Host = "panel.example"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOrigin(req) {
		t.Fatal("same-origin Sec-Fetch-Site must pass")
	}
	req.Header.Set("Sec-Fetch-Site", "same-site")
	if !sameOrigin(req) {
		t.Fatal("same-site Sec-Fetch-Site must pass")
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "http://panel.example")
	if sameOrigin(req) {
		t.Fatal("cross-site Sec-Fetch-Site must fail even with matching Origin")
	}
}

func TestSameOriginProxyForwardedHost(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/x", nil)
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Forwarded-Host", "panel.example")
	req.Header.Set("Origin", "https://panel.example")
	if !sameOrigin(req) {
		t.Fatal("trusted proxy X-Forwarded-Host must match browser Origin")
	}
}

func TestSameOriginPortTolerance(t *testing.T) {
	req := httptest.NewRequest("POST", "http://panel.example/api/x", nil)
	req.Host = "panel.example:443"
	req.Header.Set("Origin", "https://panel.example")
	if !sameOrigin(req) {
		t.Fatal("default https port on Host should match bare Origin host")
	}
}

func TestSameOriginRejectsEmpty(t *testing.T) {
	req := httptest.NewRequest("POST", "http://panel.example/api/x", nil)
	req.Host = "panel.example"
	if sameOrigin(req) {
		t.Fatal("missing Origin/Referer must fail closed")
	}
}

func TestCSRFProtectAllowsSecFetchSameOrigin(t *testing.T) {
	d := openDB(t)
	s := newServer(t, d)
	admin := loginAsAdmin(t, d)

	// Prove Sec-Fetch-Site alone is enough: no Origin header.
	req := httptest.NewRequest("POST", "/api/node-roles", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(admin)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("CSRF rejected same-origin Sec-Fetch-Site request: %s", rec.Body.String())
	}
}
