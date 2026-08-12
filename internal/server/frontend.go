package server

import (
	"bytes"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"nft/web"
)

// spaHandler serves the embedded Vite build. index.html is rewritten on each
// request so <title> matches settings.panel_name (avoids a flash of "nft" on
// hard refresh before React boots).
func spaHandler() http.Handler {
	return spaHandlerWithTitle(nil)
}

// spaHandlerWithTitle is like spaHandler but uses titleFn for the document
// title when non-nil and non-empty. Pass Server.panelBrandName for live panel name.
func spaHandlerWithTitle(titleFn func() string) http.Handler {
	dist, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	files := http.FileServerFS(dist)
	indexRaw, _ := fs.ReadFile(dist, "index.html")

	var mu sync.Mutex
	var cachedTitle string
	var cachedIndex []byte

	indexFor := func() []byte {
		title := "nft"
		if titleFn != nil {
			if t := strings.TrimSpace(titleFn()); t != "" {
				title = t
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if cachedIndex != nil && cachedTitle == title {
			return cachedIndex
		}
		// Replace default <title>…</title> only (first occurrence).
		out := indexRaw
		if i := bytes.Index(out, []byte("<title>")); i >= 0 {
			if j := bytes.Index(out[i:], []byte("</title>")); j >= 0 {
				out = append([]byte(nil), out[:i]...)
				out = append(out, []byte("<title>"+htmlEscapeTitle(title)+"</title>")...)
				out = append(out, indexRaw[i+j+len("</title>"):]...)
			}
		}
		cachedTitle = title
		cachedIndex = out
		return out
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		// Always rewrite index.html (even when the file exists in dist).
		if p == "index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			w.Write(indexFor())
			return
		}
		if _, err := fs.Stat(dist, p); err == nil {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if p == "favicon.svg" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			files.ServeHTTP(w, r)
			return
		}
		// SPA fallback: any unknown path gets index.html with live title.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(indexFor())
	})
}

func htmlEscapeTitle(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
