package server

import (
	"io/fs"
	"net/http"
	"strings"

	"nft/web"
)

func spaHandler() http.Handler {
	dist, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		panic("embedded web/dist not found: " + err.Error())
	}
	files := http.FileServerFS(dist)
	index, _ := fs.ReadFile(dist, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err == nil {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if p == "index.html" || p == "favicon.svg" {
				// index.html references hashed JS/CSS chunks that change every
				// release. Never let browsers or CDNs cache it, otherwise an old
				// HTML shell will try to load chunks that no longer exist and the
				// SPA will crash on startup.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(index)
	})
}
