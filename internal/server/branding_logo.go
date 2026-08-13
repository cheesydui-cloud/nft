package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nft/internal/db"
	"nft/web"
)

const (
	// panelLogoMaxBytes caps uploaded logo size (decoded). Plenty for favicon + sidebar.
	panelLogoMaxBytes = 512 << 10 // 512 KiB
	settingPanelLogo  = "panel_logo"      // raw base64 payload (no data: prefix)
	settingPanelLogoM = "panel_logo_mime" // e.g. image/png
	settingPanelLogoR = "panel_logo_rev"  // cache-bust token (unix seconds)
)

var panelLogoAllowedMIME = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/jpg":                true,
	"image/webp":               true,
	"image/gif":                true,
	"image/svg+xml":            true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
}

func normalizeLogoMIME(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if m == "image/jpg" {
		return "image/jpeg"
	}
	return m
}

// panelLogoRev returns the cache-bust token when a custom logo is configured.
func (s *Server) panelLogoRev() string {
	rev, _ := db.GetSetting(s.DB, settingPanelLogoR)
	return strings.TrimSpace(rev)
}

// panelLogoConfigured is true when base64 payload is present.
func (s *Server) panelLogoConfigured() bool {
	raw, _ := db.GetSetting(s.DB, settingPanelLogo)
	return strings.TrimSpace(raw) != ""
}

// brandingLogoURL is the public path clients use for <img> and favicon.
// Empty when no custom logo (FE falls back to BrandMark / default favicon.svg).
func (s *Server) brandingLogoURL() string {
	if !s.panelLogoConfigured() {
		return ""
	}
	rev := s.panelLogoRev()
	if rev == "" {
		return "/api/branding/logo"
	}
	return "/api/branding/logo?v=" + rev
}

// apiServeBrandingLogo is public (no session). Serves custom logo bytes, or
// the built-in favicon.svg when none is configured so /api/branding/logo is
// always a valid icon URL for <link rel="icon">.
func (s *Server) apiServeBrandingLogo(w http.ResponseWriter, r *http.Request) {
	raw, _ := db.GetSetting(s.DB, settingPanelLogo)
	raw = strings.TrimSpace(raw)
	mime, _ := db.GetSetting(s.DB, settingPanelLogoM)
	mime = normalizeLogoMIME(mime)
	rev, _ := db.GetSetting(s.DB, settingPanelLogoR)

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if rev != "" {
		w.Header().Set("ETag", `"`+rev+`"`)
	}

	if raw == "" {
		// Fall back to embedded default favicon so tabs never break.
		b, err := web.Assets.ReadFile("dist/favicon.svg")
		if err != nil {
			// Dev / incomplete embed: minimal SVG mark.
			w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 42 42"><rect width="42" height="42" rx="11" fill="#c4785a"/></svg>`))
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(b)
		return
	}

	// Accept accidental data-URL storage from older clients.
	if i := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && i > 0 {
		meta := raw[:i]
		raw = raw[i+1:]
		if j := strings.Index(meta, ";"); j > 5 {
			// data:image/png;base64
			mime = normalizeLogoMIME(meta[5:j])
		} else if strings.HasPrefix(meta, "data:") {
			mime = normalizeLogoMIME(meta[5:])
		}
	}
	bin, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// try raw URL-encoding-free base64 without padding issues
		bin, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(bin) == 0 {
		http.Error(w, "logo corrupt", http.StatusInternalServerError)
		return
	}
	if mime == "" || !panelLogoAllowedMIME[mime] {
		mime = http.DetectContentType(bin)
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(bin)))
	_, _ = w.Write(bin)
}

// apiUploadPanelLogo accepts multipart field "file" (admin).
func (s *Server) apiUploadPanelLogo(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, panelLogoMaxBytes+512*1024)
	if err := r.ParseMultipartForm(panelLogoMaxBytes + 512*1024); err != nil {
		jsonErr(w, http.StatusBadRequest, "文件过大或表单无效（最大 512KB）")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "请选择图片文件")
		return
	}
	defer file.Close()

	mime := normalizeLogoMIME(hdr.Header.Get("Content-Type"))
	// Prefer extension when browser sends octet-stream.
	name := strings.ToLower(hdr.Filename)
	if mime == "" || mime == "application/octet-stream" {
		switch {
		case strings.HasSuffix(name, ".png"):
			mime = "image/png"
		case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
			mime = "image/jpeg"
		case strings.HasSuffix(name, ".webp"):
			mime = "image/webp"
		case strings.HasSuffix(name, ".gif"):
			mime = "image/gif"
		case strings.HasSuffix(name, ".svg"):
			mime = "image/svg+xml"
		case strings.HasSuffix(name, ".ico"):
			mime = "image/x-icon"
		}
	}
	if !panelLogoAllowedMIME[mime] {
		jsonErr(w, http.StatusBadRequest, "仅支持 PNG / JPEG / WebP / GIF / SVG / ICO")
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, panelLogoMaxBytes+1))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "读取文件失败")
		return
	}
	if len(data) == 0 {
		jsonErr(w, http.StatusBadRequest, "空文件")
		return
	}
	if len(data) > panelLogoMaxBytes {
		jsonErr(w, http.StatusBadRequest, "图片不能超过 512KB")
		return
	}
	// Sniff when MIME was guessed from extension only.
	if detected := http.DetectContentType(data); strings.HasPrefix(detected, "image/") {
		// DetectContentType may return image/svg+xml incorrectly for small files;
		// keep declared mime for svg; otherwise prefer sniff for raster.
		if mime != "image/svg+xml" && !strings.HasPrefix(detected, "text/") {
			if panelLogoAllowedMIME[normalizeLogoMIME(detected)] {
				mime = normalizeLogoMIME(detected)
			}
		}
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	rev := strconv.FormatInt(time.Now().Unix(), 10)
	if err := db.SetSetting(s.DB, settingPanelLogo, b64); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := db.SetSetting(s.DB, settingPanelLogoM, mime); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := db.SetSetting(s.DB, settingPanelLogoR, rev); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	db.WriteAudit(s.DB, u.ID, "settings.panel_logo", rev, mime)
	jsonOK(w, map[string]any{
		"ok":              true,
		"panel_logo":      true,
		"panel_logo_url":  s.brandingLogoURL(),
		"panel_logo_rev":  rev,
		"panel_logo_mime": mime,
	})
}

// apiClearPanelLogo removes the custom logo (admin).
func (s *Server) apiClearPanelLogo(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	_ = db.SetSetting(s.DB, settingPanelLogo, "")
	_ = db.SetSetting(s.DB, settingPanelLogoM, "")
	_ = db.SetSetting(s.DB, settingPanelLogoR, "")
	db.WriteAudit(s.DB, u.ID, "settings.panel_logo_clear", "", "")
	jsonOK(w, map[string]any{"ok": true, "panel_logo": false, "panel_logo_url": ""})
}
