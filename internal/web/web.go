// Package web embeds and serves the Stats static UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticRoot embed.FS

// Handler serves the embedded Stats UI at /stats and /stats/*.
func Handler() http.Handler {
	sub, err := fs.Sub(staticRoot, "static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "static assets unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stats" {
			// Preserve query (e.g. ?token=).
			q := r.URL.RawQuery
			loc := "/stats/"
			if q != "" {
				loc += "?" + q
			}
			http.Redirect(w, r, loc, http.StatusFound)
			return
		}
		// Map /stats/ → / and /stats/foo → /foo
		http.StripPrefix("/stats", fileServer).ServeHTTP(w, r)
	})
}
