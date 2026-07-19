package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nft/internal/db"
)

func TestDocsAPIAdminAndUser(t *testing.T) {
	d := openDB(t)
	tmp := t.TempDir()
	docsDir := filepath.Join(tmp, "docs-assets")
	s, err := NewWithDocsDir(d, docsDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })

	admin := loginAsAdmin(t, d)
	_, userCookie := loginAsUser(t, d, 10)

	// Create draft
	rec := adminJSON(t, s, admin, "POST", "/api/docs", map[string]any{
		"title": "入门教程", "content": "# hi\n\nhello", "published": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create draft: %d %s", rec.Code, rec.Body.String())
	}
	var created db.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("create parse: %v %s", err, rec.Body.String())
	}

	// User must not see drafts
	req := newTestRequest("GET", "/api/my/docs", nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("my docs: %d %s", rec.Code, rec.Body.String())
	}
	var my struct {
		Docs []db.Doc `json:"docs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &my)
	if len(my.Docs) != 0 {
		t.Fatalf("user saw drafts: %+v", my.Docs)
	}

	// Publish
	rec = adminJSON(t, s, admin, "POST", fmt.Sprintf("/api/docs/%d/published", created.ID), map[string]any{
		"published": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}

	req = newTestRequest("GET", "/api/my/docs", nil)
	req.AddCookie(userCookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &my)
	if len(my.Docs) != 1 || my.Docs[0].Title != "入门教程" {
		t.Fatalf("user published list: %+v", my.Docs)
	}

	// Upload PNG
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0, 0, 0, 0xd, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0,
	}
	png = append(png, bytes.Repeat([]byte{0}, 64)...)
	if _, err := part.Write(png); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	req = newTestRequest("POST", "/api/docs/upload", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(admin)
	up := httptest.NewRecorder()
	s.Router().ServeHTTP(up, req)
	if up.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", up.Code, up.Body.String())
	}
	var upResp struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(up.Body.Bytes(), &upResp); err != nil || upResp.URL == "" {
		t.Fatalf("upload parse: %v %s", err, up.Body.String())
	}
	if _, err := os.Stat(filepath.Join(docsDir, upResp.Name)); err != nil {
		t.Fatalf("asset missing on disk: %v", err)
	}

	// User can fetch asset
	req = newTestRequest("GET", upResp.URL, nil)
	req.AddCookie(userCookie)
	get := httptest.NewRecorder()
	s.Router().ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("serve asset: %d %s", get.Code, get.Body.String())
	}
	data, _ := io.ReadAll(get.Body)
	if len(data) < 8 || data[0] != 0x89 {
		t.Fatalf("bad asset body len=%d", len(data))
	}
}
