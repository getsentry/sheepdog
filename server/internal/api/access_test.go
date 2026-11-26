package api

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sheepdog/internal/store"
	"sheepdog/internal/tasks"
)

const (
	readTimeout    = 300 * time.Millisecond
	tooManySignIns = "Too many failed sign-in attempts. Try again in a minute"
	tooManyStreams = "Too many open event streams. Close another client and try again"
	invalidSession = "The session is not valid. Sign in again"
)

func (h *hub) count(userID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streams[userID])
}

func rawDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	query := url.Values{"_pragma": {"busy_timeout(5000)"}}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?"+query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ageSessions makes every session of the login look unused for longer than the idle timeout.
func ageSessions(t *testing.T, dbPath, login string) {
	t.Helper()
	old := time.Now().Add(-store.SessionIdleTimeout - time.Hour).UnixMilli()
	_, err := rawDB(t, dbPath).Exec(`UPDATE sessions SET last_used_at = ? WHERE user_id = (SELECT id FROM users WHERE login = ?)`, old, login)
	if err != nil {
		t.Fatal(err)
	}
}

func sessionCount(t *testing.T, dbPath, login string) int {
	t.Helper()
	var n int
	err := rawDB(t, dbPath).QueryRow(`SELECT count(*) FROM sessions WHERE user_id = (SELECT id FROM users WHERE login = ?)`, login).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *harness) userID(login string) int64 {
	h.t.Helper()
	var id int64
	if err := rawDB(h.t, h.dbPath).QueryRow(`SELECT id FROM users WHERE login = ?`, login).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitKeepAlive reads the stream until the next keep-alive comment.
func (s *stream) waitKeepAlive() {
	s.t.Helper()
	for {
		if name, _ := s.next(); name == "comment" {
			return
		}
	}
}

func (h *harness) wantLogin(session, login string) {
	h.t.Helper()
	var me struct{ Login string }
	h.json("GET", "/api/me", session, nil, http.StatusOK, &me)
	if me.Login != login {
		h.t.Errorf("me = %q, want %q", me.Login, login)
	}
}

func TestRemovedLoginLosesAccess(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	h.settings(alice, map[string]string{"repo": aliceRepo})
	h.settings(bob, map[string]string{"repo": bobRepo})
	h.close()

	restarted := newHarnessAt(t, h.dbPath, "bob", nil)
	for _, path := range []string{"/api/me", "/api/settings", "/api/review-requests", "/api/events"} {
		restarted.wantError("GET", path, alice, nil, http.StatusForbidden, notAllowed)
	}
	restarted.wantLogin(bob, "bob")
	waitFor(t, "bob is polled", func() bool { return restarted.github.pollCount(bobToken) >= 2 })
	if n := restarted.github.pollCount(aliceToken); n != 0 {
		t.Errorf("the token of a removed login was used for %d polls", n)
	}
}

func TestIdleSessionExpires(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	ageSessions(t, h.dbPath, "alice")

	h.wantError("GET", "/api/me", alice, nil, http.StatusUnauthorized, invalidSession)
	if n := sessionCount(t, h.dbPath, "alice"); n != 0 {
		t.Errorf("the expired session was not deleted: %d rows", n)
	}
	h.wantLogin(bob, "bob")
}

func TestExpiredSessionsAreDeleted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	h := newHarnessAt(t, dbPath, defaultAllowed, func(s *Server) {
		ctx := context.Background()
		alice, _ := s.store.UpsertUser(ctx, 1, "alice", aliceToken)
		bob, _ := s.store.UpsertUser(ctx, 2, "bob", bobToken)
		s.store.CreateSession(ctx, alice)
		s.store.CreateSession(ctx, bob)
		ageSessions(t, dbPath, "alice")
		s.sessionCleanup = 50 * time.Millisecond
	})
	if n := sessionCount(t, dbPath, "alice"); n != 0 {
		t.Errorf("an expired session is left after start: %d rows", n)
	}
	if n := sessionCount(t, dbPath, "bob"); n != 1 {
		t.Errorf("bob has %d sessions after start, want 1", n)
	}

	ageSessions(t, h.dbPath, "bob")
	waitFor(t, "the periodic cleanup", func() bool { return sessionCount(t, dbPath, "bob") == 0 })
}

func TestSignOutEndsOnlyItsSession(t *testing.T) {
	h := newHarness(t, nil)
	phone, laptop, bob := h.signIn(aliceToken), h.signIn(aliceToken), h.signIn(bobToken)
	h.wantLogin(phone, "alice")
	h.wantLogin(laptop, "alice")
	h.wantLogin(bob, "bob")
	phoneEvents, laptopEvents, bobEvents := h.events(phone), h.events(laptop), h.events(bob)

	h.json("DELETE", "/api/session", phone, nil, http.StatusNoContent, nil)
	phoneEvents.waitClosed()
	h.wantError("GET", "/api/me", phone, nil, http.StatusUnauthorized, invalidSession)
	h.wantLogin(laptop, "alice")
	h.wantLogin(bob, "bob")
	laptopEvents.waitKeepAlive()
	bobEvents.waitKeepAlive()
}

func TestSignOutEverywhere(t *testing.T) {
	h := newHarness(t, nil)
	phone, laptop, bob := h.signIn(aliceToken), h.signIn(aliceToken), h.signIn(bobToken)
	phoneEvents, laptopEvents, bobEvents := h.events(phone), h.events(laptop), h.events(bob)

	h.json("DELETE", "/api/sessions", laptop, nil, http.StatusNoContent, nil)
	phoneEvents.waitClosed()
	laptopEvents.waitClosed()
	h.wantError("GET", "/api/me", phone, nil, http.StatusUnauthorized, invalidSession)
	h.wantError("GET", "/api/me", laptop, nil, http.StatusUnauthorized, invalidSession)
	h.wantLogin(bob, "bob")
	bobEvents.waitKeepAlive()

	h.wantLogin(h.signIn(aliceToken), "alice")
}

func (h *harness) signInFrom(forwardedFor, token string) int {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.url+"/api/session", strings.NewReader(fmt.Sprintf(`{"githubToken":%q}`, token)))
	req.Header.Set("X-Forwarded-For", forwardedFor)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestSignInLimitCountsFailuresForEachForwardedClient(t *testing.T) {
	h := newHarness(t, nil)
	for range 12 {
		h.signIn(aliceToken)
	}
	for range 10 {
		if status := h.signInFrom("198.51.100.1", "ghp_unknown"); status != http.StatusUnauthorized {
			t.Fatalf("failed sign-in = %d", status)
		}
	}
	for forwarded, want := range map[string]int{
		"198.51.100.1":               http.StatusTooManyRequests,
		"10.0.0.1, 198.51.100.1":     http.StatusTooManyRequests,
		"198.51.100.1, 198.51.100.2": http.StatusOK,
	} {
		if status := h.signInFrom(forwarded, aliceToken); status != want {
			t.Errorf("sign-in with X-Forwarded-For %q = %d, want %d", forwarded, status, want)
		}
	}
	h.signIn(aliceToken)
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		remote    string
		forwarded []string
		want      string
	}{
		{"127.0.0.1:5000", nil, "127.0.0.1"},
		{"127.0.0.1:5000", []string{"1.1.1.1, 2.2.2.2"}, "2.2.2.2"},
		{"127.0.0.1:5000", []string{"1.1.1.1", "3.3.3.3"}, "3.3.3.3"},
		{"[::1]:5000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"203.0.113.5:5000", []string{"1.1.1.1"}, "203.0.113.5"},
		{"100.64.0.7:5000", []string{"1.1.1.1"}, "100.64.0.7"},
	} {
		r := &http.Request{RemoteAddr: c.remote, Header: http.Header{}}
		for _, v := range c.forwarded {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%s, %v) = %s, want %s", c.remote, c.forwarded, got, c.want)
		}
	}
}

func TestEventsOutliveReadTimeout(t *testing.T) {
	h := newHarness(t, nil)
	events := h.events(h.signIn(aliceToken))
	end := time.Now().Add(3 * readTimeout)
	for time.Now().Before(end) {
		events.waitKeepAlive()
	}
}

func TestEventsEndForReaderThatStops(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	id := h.userID("alice")
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "GET /api/events HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\n\r\n", alice)
	waitFor(t, "the stream opens", func() bool { return h.server.hub.count(id) == 1 })

	big := strings.Repeat("x", 1<<20)
	waitFor(t, "the write deadline ends the stream", func() bool {
		h.server.hub.publish(id, "requests", big)
		return h.server.hub.count(id) == 0
	})
}

func TestEventStreamLimit(t *testing.T) {
	h := newHarness(t, nil)
	bob := h.signIn(bobToken)
	var sessions []string
	for range maxStreams {
		session := h.signIn(aliceToken)
		h.events(session)
		sessions = append(sessions, session)
	}
	ninth := h.signIn(aliceToken)
	if status, _ := h.openEvents(ninth); status != http.StatusTooManyRequests {
		t.Fatalf("stream %d = %d, want 429", maxStreams+1, status)
	}
	h.wantError("GET", "/api/events", ninth, nil, http.StatusTooManyRequests, tooManyStreams)
	h.events(sessions[0])
	h.events(bob)

	h.json("DELETE", "/api/sessions", ninth, nil, http.StatusNoContent, nil)
	h.events(h.signIn(aliceToken))
}

func TestNewStreamEndsOlderStreamsOfSession(t *testing.T) {
	h := newHarness(t, nil)
	phone, laptop := h.signIn(aliceToken), h.signIn(aliceToken)
	old, other := h.events(phone), h.events(laptop)
	current := h.events(phone)
	old.waitClosed()
	other.waitKeepAlive()
	current.waitKeepAlive()
	if n := h.server.hub.count(h.userID("alice")); n != 2 {
		t.Errorf("%d open streams, want 2", n)
	}
}

// The handler runs as if authed accepted the session just before a sign-out, so the sign-out found no stream to end.
func TestEventsRejectSessionEndedBeforeSubscribe(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	user, err := h.server.store.User(context.Background(), h.userID("alice"))
	if err != nil {
		t.Fatal(err)
	}
	h.json("DELETE", "/api/session", alice, nil, http.StatusNoContent, nil)

	req := httptest.NewRequest("GET", "/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+alice)
	rec := httptest.NewRecorder()
	h.server.events(rec, req, user)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), invalidSession) {
		t.Errorf("events after sign-out = %d %s, want 401", rec.Code, rec.Body)
	}
	if n := h.server.hub.count(user.ID); n != 0 {
		t.Errorf("%d streams left open", n)
	}
}

func TestReusedLoginGetsNewUser(t *testing.T) {
	h := newHarness(t, nil)
	first := h.signIn(aliceToken)
	h.settings(first, map[string]string{"repo": aliceRepo, "model": "alice/model", "openRouterKey": aliceKey})
	h.json("POST", reviewPath, first, nil, http.StatusAccepted, nil)
	waitFor(t, "the review is stored", func() bool {
		status, _ := h.do("GET", reviewPath, first, nil)
		return status == http.StatusOK
	})

	second := h.signIn(newAliceToken)
	h.wantLogin(second, "alice")
	var settings settingsResponse
	h.json("GET", "/api/settings", second, nil, http.StatusOK, &settings)
	if !reflect.DeepEqual(settings, settingsResponse{Starred: []string{}}) {
		t.Errorf("settings of the new user %+v, want none", settings)
	}
	h.wantError("GET", "/api/models", second, nil, http.StatusBadRequest, "Set an OpenRouter API key first")
	h.wantError("GET", reviewPath, second, nil, http.StatusNotFound, "This pull request has no review")
	var requests Requests
	h.json("GET", "/api/review-requests", second, nil, http.StatusOK, &requests)
	if requests.Repo != "" || len(requests.Rows) != 0 {
		t.Errorf("requests of the new user %+v", requests)
	}

	h.json("DELETE", "/api/sessions", second, nil, http.StatusNoContent, nil)
	h.wantLogin(first, "alice")
	h.json("GET", "/api/settings", first, nil, http.StatusOK, &settings)
	if !reflect.DeepEqual(settings, settingsResponse{Repo: aliceRepo, Model: "alice/model", HasOpenRouterKey: true, Starred: []string{}}) {
		t.Errorf("settings of the first user %+v", settings)
	}
	h.json("GET", reviewPath, first, nil, http.StatusOK, nil)
	polled := h.github.pollCount(aliceToken)
	waitFor(t, "the first user is polled with its own token", func() bool { return h.github.pollCount(aliceToken) > polled })
	if n := h.github.pollCount(newAliceToken); n != 0 {
		t.Errorf("the token of the new user was used for %d polls", n)
	}

	var users int
	if err := rawDB(t, h.dbPath).QueryRow(`SELECT count(*) FROM users WHERE login = 'alice'`).Scan(&users); err != nil || users != 2 {
		t.Errorf("%d user rows with the login alice, %v; want 2", users, err)
	}
}

func TestSignInKey(t *testing.T) {
	for ip, want := range map[string]string{
		"198.51.100.1":         "198.51.100.1",
		"::ffff:198.51.100.1":  "198.51.100.1",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2::9":      "2001:db8:1:2::/64",
		"fe80::1%eth0":         "fe80::/64",
		"not an address":       "not an address",
	} {
		if got := signInKey(ip); got != want {
			t.Errorf("signInKey(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestSignInLimitCoversIPv6Prefix(t *testing.T) {
	h := newHarness(t, nil)
	for range 10 {
		h.signInFrom("2001:db8:1:2::1", "ghp_unknown")
	}
	if status := h.signInFrom("2001:db8:1:2:ffff::7", aliceToken); status != http.StatusTooManyRequests {
		t.Errorf("sign-in from the same /64 = %d, want 429", status)
	}
	if status := h.signInFrom("2001:db8:1:3::1", aliceToken); status != http.StatusOK {
		t.Errorf("sign-in from another /64 = %d, want 200", status)
	}
}

func TestLimiterDeletesEndedWindowsOncePerPeriod(t *testing.T) {
	l := newLimiter(1, time.Minute)
	ended := time.Now().Add(-2 * time.Minute)
	l.swept = time.Now()
	l.failures["ended"] = &window{start: ended, count: 5}
	if l.blocked("other") {
		t.Fatal("a key without failures is blocked")
	}
	if _, ok := l.failures["ended"]; !ok {
		t.Fatal("the limiter deleted windows again within one period")
	}
	if l.blocked("ended") {
		t.Error("an ended window still blocks")
	}

	l.failures["ended"] = &window{start: ended, count: 5}
	l.swept = ended
	l.blocked("other")
	if len(l.failures) != 0 {
		t.Errorf("%d windows left after the periodic delete", len(l.failures))
	}
}

// record must publish under the server lock, so that a restart cannot publish between its store and its event.
func TestRecordPublishesUnderServerLock(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.settings(alice, map[string]string{"repo": aliceRepo})
	id := h.userID("alice")
	h.server.mu.Lock()
	run := h.server.polls[id]
	h.server.mu.Unlock()

	serverLocked := func() bool {
		if h.server.mu.TryLock() {
			h.server.mu.Unlock()
			return false
		}
		return true
	}
	h.server.hub.mu.Lock()
	recorded := make(chan bool)
	go func() { recorded <- h.server.record(id, run, Requests{}, nil) }()
	held := false
	for deadline := time.Now().Add(time.Second); !held && time.Now().Before(deadline); {
		held = serverLocked()
	}
	time.Sleep(20 * time.Millisecond)
	held = held && serverLocked()
	h.server.hub.mu.Unlock()
	<-recorded
	if !held {
		t.Error("record released the server lock before it published")
	}
}

func TestSignInClearsRepoCache(t *testing.T) {
	h := newHarness(t, nil)
	alice := h.signIn(aliceToken)
	h.json("GET", "/api/repos", alice, nil, http.StatusOK, nil)
	h.json("GET", "/api/repos", alice, nil, http.StatusOK, nil)
	if n := h.github.repoListCount(aliceToken); n != 1 {
		t.Fatalf("%d repository loads, want 1 with the cache", n)
	}

	h.signIn(aliceToken2)
	h.json("GET", "/api/repos", alice, nil, http.StatusOK, nil)
	if n := h.github.repoListCount(aliceToken2); n != 1 {
		t.Errorf("%d repository loads with the new token, want 1", n)
	}
}

func TestSignInKeepsListOnOtherDevices(t *testing.T) {
	h := newHarness(t, nil)
	phone := h.signIn(aliceToken)
	h.settings(phone, map[string]string{"repo": aliceRepo})
	h.waitRequests(phone, func(r Requests) bool { return r.FetchedAt != nil })
	events := h.events(phone)
	events.nextRequests()

	h.signIn(aliceToken)
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		if r := events.nextRequests(); len(r.Rows) == 0 {
			t.Fatalf("a sign-in with the same token emptied the list: %+v", r)
		}
	}

	h.signIn(aliceToken2)
	waitFor(t, "a poll with the new token", func() bool { return h.github.pollCount(aliceToken2) >= 1 })
	stopped := h.github.pollCount(aliceToken)
	time.Sleep(200 * time.Millisecond)
	if n := h.github.pollCount(aliceToken); n != stopped {
		t.Errorf("the old token was used for %d more polls", n-stopped)
	}
}

func TestSignInAfterRenameRestartsPoller(t *testing.T) {
	h := newHarnessAt(t, filepath.Join(t.TempDir(), "test.db"), defaultAllowed+",alice2", nil)
	session := h.signIn(aliceToken)
	h.settings(session, map[string]string{"repo": aliceRepo})
	h.waitRequests(session, func(r Requests) bool { return r.FetchedAt != nil })

	h.github.rename(aliceToken, "alice2")
	h.signIn(aliceToken)
	h.wantLogin(session, "alice2")
	waitFor(t, "the poller uses the new login", func() bool {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		for _, run := range h.server.polls {
			if run.login == "alice2" {
				return true
			}
		}
		return false
	})
}

func TestReviewConflictIsPerUser(t *testing.T) {
	h := newHarness(t, nil)
	alice, bob := h.signIn(aliceToken), h.signIn(bobToken)
	for _, session := range []string{alice, bob} {
		h.settings(session, map[string]string{"openRouterKey": aliceKey, "model": "slow/model"})
	}
	var aliceTask, bobTask tasks.Task
	h.json("POST", reviewPath, alice, nil, http.StatusAccepted, &aliceTask)
	h.openrouter.waitStarted(t)
	h.json("POST", reviewPath, bob, nil, http.StatusAccepted, &bobTask)
	h.openrouter.waitStarted(t)
	for _, session := range []string{alice, bob} {
		h.wantError("POST", reviewPath, session, nil, http.StatusConflict, "A review of this pull request is already running")
	}

	for session, want := range map[string]tasks.Task{alice: aliceTask, bob: bobTask} {
		var list []tasks.Task
		h.json("GET", "/api/tasks", session, nil, http.StatusOK, &list)
		if len(list) != 1 || list[0] != want {
			t.Errorf("tasks %+v, want only %+v", list, want)
		}
	}
	h.close()
	h.openrouter.waitCancelled(t)
	h.openrouter.waitCancelled(t)
}
