// Package web serves the embedded, pre-built frontend with an SPA fallback.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is populated by `npm run build` in server/web (vite outDir points here).
// In a fresh checkout it only holds .gitkeep; the placeholder below is served
// instead so the binary still builds and runs without Node.
//
//go:embed all:dist
var dist embed.FS

//go:embed placeholder.html
var placeholder []byte

// Handler serves static assets; unknown paths fall back to index.html so the
// client-side router can handle them.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FS(sub)
	server := http.FileServer(files)
	_, indexErr := sub.Open("index.html")
	built := indexErr == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p != "/" {
			if f, err := files.Open(p); err == nil {
				f.Close()
				if p == "/manifest.webmanifest" {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				if strings.HasPrefix(p, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					// Service workers and the manifest must be revalidated so a
					// deploy cannot strand an installed app on stale behavior.
					w.Header().Set("Cache-Control", "no-cache")
				}
				server.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(placeholder)
			return
		}
		r.URL.Path = "/"
		server.ServeHTTP(w, r)
	})
}
