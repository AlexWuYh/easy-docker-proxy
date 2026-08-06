// Package web embeds and serves the Stats static UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static/*
var staticRoot embed.FS

// Handler serves the embedded Stats UI at /stats and /stats/*.
//
// Important: do not let http.FileServer redirect /index.html → ./  (that pairs
// with our /stats/ → index.html redirect and creates an infinite loop).
func Handler() http.Handler {
	sub, err := fs.Sub(staticRoot, "static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "static assets unavailable", http.StatusInternalServerError)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip /stats prefix → filesystem path under static/
		rel := strings.TrimPrefix(r.URL.Path, "/stats")
		rel = path.Clean("/" + rel)
		if rel == "/" {
			rel = "/index.html"
		}
		// drop leading slash for fs.FS
		name := strings.TrimPrefix(rel, "/")
		if name == "" || name == "." {
			name = "index.html"
		}

		// Directory request → index.html without 301 dance
		if strings.HasSuffix(name, "/") {
			name = path.Join(name, "index.html")
		}

		data, err := fs.ReadFile(sub, name)
		if err != nil {
			// try as directory index
			if data2, err2 := fs.ReadFile(sub, path.Join(name, "index.html")); err2 == nil {
				data = data2
				name = path.Join(name, "index.html")
			} else {
				http.NotFound(w, r)
				return
			}
		}

		ctype := contentType(name)
		w.Header().Set("Content-Type", ctype)
		// Always no-store for console assets so login/theme fixes are not stuck on old JS/CSS.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}
