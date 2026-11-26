// Package webui serves the web app from the build output in dist.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

const contentSecurityPolicy = "default-src 'self'; connect-src 'self'; img-src 'self' data: https://avatars.githubusercontent.com; " +
	"style-src 'self' 'unsafe-inline'; frame-ancestors 'none'"

const notBuilt = `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Sheepdog</title>
<h1>The web app is not built</h1>
<p>Run <code>npm ci &amp;&amp; npm run build</code> in the folder <code>web</code>, then build the server again.</p>
</html>
`

// Handler serves the embedded web app.
func Handler() http.Handler {
	files, _ := fs.Sub(dist, "dist")
	return newHandler(files)
}

// newHandler serves the files, and index.html for every path that is not a file, so that the routes of the app work.
// Vite puts the hashed assets in assets/, so only they get a long cache time.
func newHandler(files fs.FS) http.Handler {
	_, err := fs.Stat(files, "index.html")
	built := err == nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !built {
			h.Set("Cache-Control", "no-cache")
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, notBuilt)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		asset := strings.HasPrefix(name, "assets/")
		if info, err := fs.Stat(files, name); err != nil || info.IsDir() {
			if asset {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if asset {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		serve(w, r, files, name)
	})
}

func serve(w http.ResponseWriter, r *http.Request, files fs.FS, name string) {
	f, err := files.Open(name)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	content, ok := f.(io.ReadSeeker)
	info, statErr := f.Stat()
	if !ok || statErr != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
}
