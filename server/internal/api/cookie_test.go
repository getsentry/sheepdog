package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const foreignOrigin = "https://evil.example"

// browser sends requests as a browser does, with the session cookie and the given Origin header.
func (h *harness) browser(method, path, session, origin string, header http.Header, body any) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, h.url+path, reader)
	for name, values := range header {
		req.Header[name] = values
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (h *harness) browserStatus(method, path, session, origin string, body any) int {
	h.t.Helper()
	return h.browser(method, path, session, origin, nil, body).StatusCode
}

func responseCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			if found != nil {
				t.Fatal("two session cookies")
			}
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no session cookie in %v", resp.Header.Values("Set-Cookie"))
	}
	return found
}

// browserSignIn signs in through the browser path and returns the session cookie.
func (h *harness) browserSignIn(origin string, header http.Header) *http.Cookie {
	h.t.Helper()
	resp := h.browser("POST", "/api/session", "", origin, header, map[string]string{"githubToken": aliceToken})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("sign-in = %d", resp.StatusCode)
	}
	return responseCookie(h.t, resp)
}

func wantCookie(t *testing.T, c *http.Cookie, secure bool) {
	t.Helper()
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure != secure {
		t.Errorf("cookie %q: HttpOnly %v, SameSite %v, Path %q, Secure %v; want Secure %v", c.Raw, c.HttpOnly, c.SameSite, c.Path, c.Secure, secure)
	}
}

func TestSignInSetsSessionCookie(t *testing.T) {
	h := newHarness(t, nil)
	resp := h.browser("POST", "/api/session", "", "", nil, map[string]string{"githubToken": aliceToken})
	cookie := responseCookie(t, resp)
	wantCookie(t, cookie, false)
	if cookie.MaxAge <= 0 || len(cookie.Value) != 43 {
		t.Errorf("cookie %q", cookie.Raw)
	}
	h.wantLogin(cookie.Value, "alice")

	failed := h.browser("POST", "/api/session", "", "", nil, map[string]string{"githubToken": "ghp_unknown"})
	if len(failed.Cookies()) != 0 {
		t.Errorf("a failed sign-in set %v", failed.Header.Values("Set-Cookie"))
	}
}

func TestSessionCookieIsSecureOnlyForHTTPS(t *testing.T) {
	h := newHarness(t, nil)
	wantCookie(t, h.browserSignIn("", http.Header{"X-Forwarded-Proto": {"https"}}), true)
	wantCookie(t, h.browserSignIn("", http.Header{"X-Forwarded-Proto": {"http"}}), false)

	tlsServer := httptest.NewTLSServer(h.server.Handler())
	defer tlsServer.Close()
	req, _ := http.NewRequest("POST", tlsServer.URL+"/api/session", strings.NewReader(`{"githubToken":"`+aliceToken+`"}`))
	resp, err := tlsServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	wantCookie(t, responseCookie(t, resp), true)

	for _, c := range []struct {
		remote, proto string
		tls, want     bool
	}{
		{"127.0.0.1:5000", "https", false, true},
		{"[::1]:5000", "https", false, true},
		{"127.0.0.1:5000", "", false, false},
		{"127.0.0.1:5000", "http", false, false},
		{"203.0.113.5:5000", "https", false, false},
		{"100.64.0.7:5000", "https", false, false},
		{"203.0.113.5:5000", "", true, true},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		} else {
			r.TLS = nil
		}
		if got := isHTTPS(r); got != c.want {
			t.Errorf("isHTTPS(%s, %q, TLS %v) = %v, want %v", c.remote, c.proto, c.tls, got, c.want)
		}
	}
}

func TestCookieSessionNeedsOriginToChangeState(t *testing.T) {
	h := newHarness(t, nil)
	session := h.browserSignIn("", nil).Value
	settings := map[string]string{"model": "m/alice"}

	if status := h.browserStatus("GET", "/api/me", session, "", nil); status != http.StatusOK {
		t.Errorf("cookie GET without Origin = %d, want 200", status)
	}
	if status := h.browserStatus("GET", "/api/settings", session, foreignOrigin, nil); status != http.StatusOK {
		t.Errorf("cookie GET with another Origin = %d, want 200", status)
	}
	for _, origin := range []string{"", foreignOrigin, "null", strings.Replace(h.url, "http://", "https://", 1)} {
		if status := h.browserStatus("PUT", "/api/settings", session, origin, settings); status != http.StatusForbidden {
			t.Errorf("cookie PUT with Origin %q = %d, want 403", origin, status)
		}
		if status := h.browserStatus("POST", reviewPath, session, origin, nil); status != http.StatusForbidden {
			t.Errorf("cookie POST with Origin %q = %d, want 403", origin, status)
		}
		if status := h.browserStatus("DELETE", "/api/session", session, origin, nil); status != http.StatusForbidden {
			t.Errorf("cookie DELETE with Origin %q = %d, want 403", origin, status)
		}
	}
	var got settingsResponse
	h.json("GET", "/api/settings", session, nil, http.StatusOK, &got)
	if got.Model != "" {
		t.Errorf("a rejected request changed the settings: %+v", got)
	}

	if status := h.browserStatus("PUT", "/api/settings", session, h.url, settings); status != http.StatusOK {
		t.Errorf("cookie PUT with this Origin = %d, want 200", status)
	}
	if status := h.browserStatus("POST", reviewPath, session, h.url, nil); status != http.StatusBadRequest {
		t.Errorf("cookie POST with this Origin = %d, want 400 for the missing key", status)
	}
	if status := h.browserStatus("DELETE", "/api/tasks/x", session, h.url, nil); status != http.StatusNotFound {
		t.Errorf("cookie DELETE with this Origin = %d, want 404 for the unknown task", status)
	}
}

func TestAuthorizationHeaderIgnoresOrigin(t *testing.T) {
	h := newHarness(t, nil)
	session := h.signIn(aliceToken)
	for _, origin := range []string{"", foreignOrigin, "null"} {
		header := http.Header{"Authorization": {"Bearer " + session}}
		if status := h.browser("PUT", "/api/settings", "", origin, header, map[string]string{"model": "m/alice"}).StatusCode; status != http.StatusOK {
			t.Errorf("Authorization PUT with Origin %q = %d, want 200", origin, status)
		}
	}

	cookie := h.browserSignIn("", nil).Value
	header := http.Header{"Authorization": {"Bearer not-a-session"}}
	if status := h.browser("GET", "/api/me", cookie, "", header, nil).StatusCode; status != http.StatusUnauthorized {
		t.Errorf("a bad Authorization header with a good cookie = %d, want 401", status)
	}
}

func TestSignInRejectsOtherOrigin(t *testing.T) {
	h := newHarness(t, nil)
	for _, origin := range []string{foreignOrigin, "null"} {
		resp := h.browser("POST", "/api/session", "", origin, nil, map[string]string{"githubToken": aliceToken})
		if resp.StatusCode != http.StatusForbidden || len(resp.Cookies()) != 0 {
			t.Errorf("sign-in with Origin %q = %d, cookies %v; want 403 without a cookie", origin, resp.StatusCode, resp.Cookies())
		}
	}
	if n := sessionCount(t, h.dbPath, "alice"); n != 0 {
		t.Errorf("%d sessions after rejected sign-ins", n)
	}
	h.browserSignIn(h.url, nil)
	h.browserSignIn("", nil)
}

func TestSignOutClearsCookie(t *testing.T) {
	for _, path := range []string{"/api/session", "/api/sessions"} {
		h := newHarness(t, nil)
		session := h.browserSignIn("", nil).Value
		other := h.signIn(aliceToken)
		events := h.cookieEvents(session)

		resp := h.browser("DELETE", path, session, h.url, nil, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE %s = %d", path, resp.StatusCode)
		}
		cleared := responseCookie(t, resp)
		wantCookie(t, cleared, false)
		if cleared.Value != "" || cleared.MaxAge >= 0 {
			t.Errorf("DELETE %s cookie %q, want a deleted cookie", path, cleared.Raw)
		}
		events.waitClosed()
		if status := h.browserStatus("GET", "/api/me", session, "", nil); status != http.StatusUnauthorized {
			t.Errorf("cookie after DELETE %s = %d, want 401", path, status)
		}
		if status, _ := h.do("GET", "/api/me", other, nil); (status == http.StatusOK) != (path == "/api/session") {
			t.Errorf("other session after DELETE %s = %d", path, status)
		}

		bearer := h.signIn(aliceToken)
		resp = h.browser("DELETE", path, "", "", http.Header{"Authorization": {"Bearer " + bearer}}, nil)
		if cleared := responseCookie(t, resp); resp.StatusCode != http.StatusNoContent || cleared.MaxAge >= 0 {
			t.Errorf("Authorization DELETE %s = %d, cookie %q", path, resp.StatusCode, cleared.Raw)
		}
	}
}

func (h *harness) cookieEvents(session string) *stream {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", h.url+"/api/events", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		cancel()
		resp.Body.Close()
	})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("cookie events = %d", resp.StatusCode)
	}
	s := &stream{t: h.t, reader: bufio.NewReader(resp.Body)}
	s.nextRequests()
	return s
}

func TestUnknownAPIPathIsJSONNotFound(t *testing.T) {
	h := newHarness(t, nil)
	session := h.signIn(aliceToken)
	for _, c := range [][2]string{{"GET", "/api/nope"}, {"POST", "/api/nope"}, {"GET", "/api/"}, {"PUT", "/api/me"}} {
		h.wantError(c[0], c[1], session, nil, http.StatusNotFound, "No API route has this method and path")
	}
	resp, err := http.Get(h.url + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("GET /settings: %d %v", resp.StatusCode, resp.Header)
	}
}
