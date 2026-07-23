package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpsertARecordCreate(t *testing.T) {
	var gotPost map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			writeCF(w, true, []DNSRecord{})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
			_ = json.NewDecoder(r.Body).Decode(&gotPost)
			writeCF(w, true, DNSRecord{ID: "rec1", Type: "A", Name: "a.example.com", Content: "1.2.3.4", TTL: 1, Proxied: false})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeCF(w, false, nil)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	rec, err := c.UpsertARecord(context.Background(), "zone1", "a.example.com", "1.2.3.4", 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "rec1" || rec.Content != "1.2.3.4" {
		t.Fatalf("got %+v", rec)
	}
	if gotPost["proxied"] != false {
		t.Fatalf("must force DNS-only, got %+v", gotPost)
	}
	if gotPost["type"] != "A" || gotPost["content"] != "1.2.3.4" {
		t.Fatalf("payload %+v", gotPost)
	}
}

func TestUpsertARecordUpdate(t *testing.T) {
	var putPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeCF(w, true, []DNSRecord{{
				ID: "old", Type: "A", Name: "a.example.com", Content: "9.9.9.9", TTL: 1, Proxied: false,
			}})
		case r.Method == http.MethodPut:
			putPath = r.URL.Path
			writeCF(w, true, DNSRecord{ID: "old", Type: "A", Name: "a.example.com", Content: "1.2.3.4", TTL: 1})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeCF(w, false, nil)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	rec, err := c.UpsertARecord(context.Background(), "zone1", "a.example.com", "1.2.3.4", 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Content != "1.2.3.4" {
		t.Fatalf("got %+v", rec)
	}
	if !strings.Contains(putPath, "/dns_records/old") {
		t.Fatalf("expected PUT existing record, path=%s", putPath)
	}
}

func TestUpsertARecordNoopWhenSame(t *testing.T) {
	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeCF(w, true, []DNSRecord{{
				ID: "x", Type: "A", Name: "a.example.com", Content: "1.2.3.4", TTL: 1, Proxied: false,
			}})
		case http.MethodPut, http.MethodPost:
			puts++
			writeCF(w, true, DNSRecord{})
		}
	}))
	defer srv.Close()

	c := &Client{Token: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	rec, err := c.UpsertARecord(context.Background(), "zone1", "a.example.com", "1.2.3.4", 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Content != "1.2.3.4" {
		t.Fatalf("got %+v", rec)
	}
	if puts != 0 {
		t.Fatalf("should not rewrite identical record, puts=%d", puts)
	}
}

func TestUpsertRejectsNonIPv4(t *testing.T) {
	c := &Client{Token: "tok"}
	_, err := c.UpsertARecord(context.Background(), "z", "a.example.com", "not-an-ip", 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveZoneID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCF(w, true, []Zone{{ID: "zid", Name: "example.com"}})
	}))
	defer srv.Close()
	c := &Client{Token: "tok", BaseURL: srv.URL, HTTPClient: srv.Client()}
	id, err := c.ResolveZoneID(context.Background(), "example.com")
	if err != nil || id != "zid" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestAPIErrorSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Invalid API Token"}],"result":null}`))
	}))
	defer srv.Close()
	c := &Client{Token: "bad", BaseURL: srv.URL, HTTPClient: srv.Client()}
	_, err := c.ListZones(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid API Token") {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordNameForHost(t *testing.T) {
	if got := RecordNameForHost("Home.Example.com", ""); got != "home.example.com" {
		t.Fatalf("got %q", got)
	}
	if got := RecordNameForHost("x.example.com", "custom.example.com."); got != "custom.example.com" {
		t.Fatalf("got %q", got)
	}
}

func writeCF(w http.ResponseWriter, ok bool, result any) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"success": ok, "errors": []any{}, "result": result}
	if !ok {
		payload["errors"] = []map[string]any{{"code": 1, "message": "fail"}}
	}
	_ = json.NewEncoder(w).Encode(payload)
}
