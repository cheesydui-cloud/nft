package server

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"nft/internal/db"
)

const (
	docUploadMaxBytes = 5 << 20 // 5 MiB
	docTitleMaxRunes  = 200
	docContentMax     = 2 << 20 // 2 MiB of Markdown text
)

var docAssetNameRe = regexp.MustCompile(`^[a-f0-9]{16,64}\.(jpg|jpeg|png|gif|webp)$`)

// ensureDocsDir creates the on-disk asset directory when configured.
func (s *Server) ensureDocsDir() (string, error) {
	dir := strings.TrimSpace(s.DocsDir)
	if dir == "" {
		return "", fmt.Errorf("docs asset directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// --- Admin ---

func (s *Server) apiListDocs(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListDocs(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Doc{}
	}
	jsonOK(w, map[string]any{"docs": list})
}

func (s *Server) apiGetDoc(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	doc, err := db.GetDoc(s.DB, id)
	if err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, doc)
}

func (s *Server) apiCreateDoc(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		Content   string `json:"content"`
		Published bool   `json:"published"`
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		jsonErr(w, http.StatusBadRequest, "title is required")
		return
	}
	if utf8.RuneCountInString(title) > docTitleMaxRunes {
		jsonErr(w, http.StatusBadRequest, "title is too long")
		return
	}
	if len(body.Content) > docContentMax {
		jsonErr(w, http.StatusBadRequest, "content is too long")
		return
	}
	doc, err := db.CreateDoc(s.DB, title, body.Content, body.Published)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, doc)
}

func (s *Server) apiUpdateDoc(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Title     string `json:"title"`
		Content   string `json:"content"`
		Published bool   `json:"published"`
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		jsonErr(w, http.StatusBadRequest, "title is required")
		return
	}
	if utf8.RuneCountInString(title) > docTitleMaxRunes {
		jsonErr(w, http.StatusBadRequest, "title is too long")
		return
	}
	if len(body.Content) > docContentMax {
		jsonErr(w, http.StatusBadRequest, "content is too long")
		return
	}
	doc, err := db.UpdateDoc(s.DB, id, title, body.Content, body.Published)
	if err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, doc)
}

func (s *Server) apiSetDocPublished(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Published bool `json:"published"`
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	doc, err := db.SetDocPublished(s.DB, id, body.Published)
	if err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, doc)
}

func (s *Server) apiDeleteDoc(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := db.DeleteDoc(s.DB, id); err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	} else if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

func (s *Server) apiReorderDocs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		jsonErr(w, http.StatusBadRequest, "ids required")
		return
	}
	if err := db.ReorderDocs(s.DB, body.IDs); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err := db.ListDocs(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Doc{}
	}
	jsonOK(w, map[string]any{"docs": list})
}

func (s *Server) apiMoveDoc(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Direction string `json:"direction"` // "up" or "down"
	}
	if err := decodeJSON(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dir := strings.ToLower(strings.TrimSpace(body.Direction))
	if dir != "up" && dir != "down" {
		jsonErr(w, http.StatusBadRequest, "direction must be up or down")
		return
	}
	list, err := db.ListDocs(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	idx := -1
	for i, d := range list {
		if d.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	}
	swap := idx
	if dir == "up" {
		if idx == 0 {
			jsonOK(w, map[string]any{"docs": list})
			return
		}
		swap = idx - 1
	} else {
		if idx >= len(list)-1 {
			jsonOK(w, map[string]any{"docs": list})
			return
		}
		swap = idx + 1
	}
	if err := db.SwapDocOrder(s.DB, list[idx].ID, list[swap].ID); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err = db.ListDocs(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Doc{}
	}
	jsonOK(w, map[string]any{"docs": list})
}

// apiUploadDocAsset stores an image under DocsDir and returns a panel URL.
func (s *Server) apiUploadDocAsset(w http.ResponseWriter, r *http.Request) {
	dir, err := s.ensureDocsDir()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, docUploadMaxBytes+512*1024)
	if err := r.ParseMultipartForm(docUploadMaxBytes + 512*1024); err != nil {
		jsonErr(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	if n == 0 {
		jsonErr(w, http.StatusBadRequest, "empty file")
		return
	}
	kind := http.DetectContentType(head)
	ext, ok := docImageExt(kind, hdr.Filename)
	if !ok {
		jsonErr(w, http.StatusBadRequest, "only jpg/png/gif/webp images are allowed")
		return
	}

	name := db.RandToken(16) + ext
	path := filepath.Join(dir, name)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "failed to create asset file")
		return
	}
	defer out.Close()

	written, err := out.Write(head)
	if err != nil {
		_ = os.Remove(path)
		jsonErr(w, http.StatusInternalServerError, "failed to write asset")
		return
	}
	rest, err := io.Copy(out, io.LimitReader(file, docUploadMaxBytes+1-int64(written)))
	if err != nil {
		_ = os.Remove(path)
		jsonErr(w, http.StatusInternalServerError, "failed to write asset")
		return
	}
	if int64(written)+rest > docUploadMaxBytes {
		_ = os.Remove(path)
		jsonErr(w, http.StatusBadRequest, "file too large (max 5MB)")
		return
	}

	url := "/api/docs/assets/" + name
	jsonOK(w, map[string]any{"url": url, "name": name})
}

// apiServeDocAsset serves an uploaded image to any authenticated user.
func (s *Server) apiServeDocAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !docAssetNameRe.MatchString(name) {
		jsonErr(w, http.StatusBadRequest, "invalid asset name")
		return
	}
	dir := strings.TrimSpace(s.DocsDir)
	if dir == "" {
		jsonErr(w, http.StatusNotFound, "asset not found")
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		jsonErr(w, http.StatusNotFound, "asset not found")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

// --- User (published only) ---

func (s *Server) apiMyDocs(w http.ResponseWriter, r *http.Request) {
	list, err := db.ListPublishedDocs(s.DB)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []db.Doc{}
	}
	jsonOK(w, map[string]any{"docs": list})
}

func (s *Server) apiMyGetDoc(w http.ResponseWriter, r *http.Request) {
	id, err := urlParamInt64(r, "id")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	doc, err := db.GetPublishedDoc(s.DB, id)
	if err == sql.ErrNoRows {
		jsonErr(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, doc)
}

func docImageExt(contentType, filename string) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch ct {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return ".jpg", true
	case ".png":
		return ".png", true
	case ".gif":
		return ".gif", true
	case ".webp":
		return ".webp", true
	}
	return "", false
}
