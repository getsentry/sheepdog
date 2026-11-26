package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const index = `<!doctype html><script type="module" src="/assets/index-abc123.js"></script>`

var built = fstest.MapFS{
	"index.html":             {Data: []byte(index)},
	"favicon.svg":            {Data: []byte("<svg></svg>")},
	"assets/index-abc123.js": {Data: []byte("console.log(1)")},
}

func get(t *testing.T, h http.Handler, method, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Result()
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wantSecurityHeaders(t *testing.T, path string, resp *http.Response) {
	t.Helper()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, part := range []string{"default-src 'self'", "connect-src 'self'", "img-src 'self' data: https://avatars.githubusercontent.com", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("%s: CSP %q has no %q", path, csp, part)
		}
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("%s: CSP %q allows eval", path, csp)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options %q", path, got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("%s: Referrer-Policy %q", path, got)
	}
}

func TestServesFilesAndIndexForAppRoutes(t *testing.T) {
	h := newHandler(built)
	for _, c := range []struct {
		path, body, contentType, cache string
	}{
		{"/", index, "text/html; charset=utf-8", "no-cache"},
		{"/index.html", index, "text/html; charset=utf-8", "no-cache"},
		{"/settings", index, "text/html; charset=utf-8", "no-cache"},
		{"/a/b/../c", index, "text/html; charset=utf-8", "no-cache"},
		{"/assets", index, "text/html; charset=utf-8", "no-cache"},
		{"/favicon.svg", "<svg></svg>", "image/svg+xml", "no-cache"},
		{"/assets/index-abc123.js", "console.log(1)", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
	} {
		resp := get(t, h, "GET", c.path)
		if resp.StatusCode != http.StatusOK || body(t, resp) != c.body {
			t.Errorf("GET %s = %d, want 200 with %q", c.path, resp.StatusCode, c.body)
		}
		if got := resp.Header.Get("Content-Type"); got != c.contentType {
			t.Errorf("GET %s Content-Type %q, want %q", c.path, got, c.contentType)
		}
		if got := resp.Header.Get("Cache-Control"); got != c.cache {
			t.Errorf("GET %s Cache-Control %q, want %q", c.path, got, c.cache)
		}
		wantSecurityHeaders(t, c.path, resp)
	}
}

func TestMissingAssetIsNotFound(t *testing.T) {
	resp := get(t, newHandler(built), "GET", "/assets/old-123.js")
	if resp.StatusCode != http.StatusNotFound || strings.Contains(body(t, resp), "<script") {
		t.Errorf("missing asset = %d, want 404 without index.html", resp.StatusCode)
	}
	wantSecurityHeaders(t, "/assets/old-123.js", resp)
}

func TestOnlyGetAndHead(t *testing.T) {
	h := newHandler(built)
	if resp := get(t, h, "HEAD", "/settings"); resp.StatusCode != http.StatusOK || body(t, resp) != "" {
		t.Errorf("HEAD = %d", resp.StatusCode)
	}
	resp := get(t, h, "POST", "/")
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST = %d, Allow %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestPageWithoutBuild(t *testing.T) {
	h := newHandler(fstest.MapFS{".gitkeep": {}})
	for _, path := range []string{"/", "/settings", "/.gitkeep"} {
		resp := get(t, h, "GET", path)
		if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body(t, resp), "npm run build") {
			t.Errorf("GET %s without a build = %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("Content-Type %q", got)
		}
		wantSecurityHeaders(t, path, resp)
	}
}

func TestEmbeddedFilesServe(t *testing.T) {
	if resp := get(t, Handler(), "GET", "/settings"); resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET /settings = %d", resp.StatusCode)
	}
}
