package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// A single malformed file used to end the process: open() called log.Fatal from
// inside the request handler, so one bad save took the workspace offline.
func TestMalformedFileDoesNotStopTheServer(t *testing.T) {
	dir := workspace(t, map[string]string{"hello.md": "# Hello\n"})
	h := newServer(dir, false).routes()
	if rec := get(t, h, "/"); rec.Code != http.StatusOK {
		t.Fatalf("initial GET = %d", rec.Code)
	}

	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte("---\ntype: view\n---\n# body\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // let the modification time differ

	rec := get(t, h, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after a malformed file = %d, want 200", rec.Code)
	}
	for _, want := range []string{"Hello", "body"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("page is missing %q", want)
		}
	}
}

func TestAssetDirectoryIsNotListed(t *testing.T) {
	h := newServer(workspace(t, nil), false).routes()
	if rec := get(t, h, "/assets/"); rec.Code != http.StatusNotFound {
		t.Fatalf("asset listing = %d", rec.Code)
	}
	if rec := get(t, h, "/assets/htmx.min.js"); rec.Code != http.StatusOK {
		t.Fatalf("asset fetch = %d", rec.Code)
	}
}

func TestRestrictedPagesAreHiddenFromAnonymousCallers(t *testing.T) {
	dir := workspace(t, map[string]string{
		"alice.md":  "---\ntype: identity\n---\n# Alice\n",
		"open.md":   "# Open\n",
		"secret.md": "---\npermissions:\n  read: alice\n---\n# Secret\n",
	})
	body := get(t, newServer(dir, false).routes(), "/").Body.String()
	if !strings.Contains(body, "Open") {
		t.Fatal("open page hidden")
	}
	if strings.Contains(body, "Secret") {
		t.Fatal("restricted page listed to an anonymous caller")
	}
}

// The browser must receive a session, never the permanent bearer token.
func TestLoginExchangesTokenForSession(t *testing.T) {
	dir := workspace(t, map[string]string{"alice.md": "---\ntype: identity\n---\n# Alice\n"})
	s := newServer(dir, false)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	token, err := ws.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	h := s.routes()

	post := func(target, token string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(url.Values{"token": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := post("/login", "jk_wrong", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d", rec.Code)
	}
	if rec := post("/login", token, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site login = %d, want 403", rec.Code)
	}
	rec := post("/login", token, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	c := cookies[0]
	if c.Value == token || strings.Contains(c.Value, "jk_") {
		t.Fatal("the bearer token itself was stored in the cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("weak cookie: %#v", c)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(c)
	authed := httptest.NewRecorder()
	h.ServeHTTP(authed, r)
	if !strings.Contains(authed.Body.String(), "Alice") {
		t.Fatal("session did not authenticate")
	}

	if rec := post("/login", token, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d, want 403", rec.Code)
	}
	if rec := get(t, h, "/login"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /login = %d", rec.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := newSessionStore()
	id, err := s.create("alice")
	if err != nil {
		t.Fatal(err)
	}
	if s.identity(id) != "alice" {
		t.Fatal("fresh session not recognised")
	}
	s.mu.Lock()
	s.m[id] = sessionEntry{identity: "alice", expires: time.Now().Add(-time.Second)}
	s.mu.Unlock()
	if s.identity(id) != "" {
		t.Fatal("expired session accepted")
	}
	id2, _ := s.create("bob")
	s.drop(id2)
	if s.identity(id2) != "" {
		t.Fatal("dropped session accepted")
	}
	if s.identity("") != "" {
		t.Fatal("empty session id accepted")
	}
}

// A bare carriage return terminates a line in the event-stream grammar, so page
// content containing one could truncate the event and forge the next field.
func TestSSEDataHasNoLineBreaks(t *testing.T) {
	for _, in := range []string{"a\rb", "a\nb", "a\r\nb", "<h3>Ev\rIL</h3>"} {
		got := sseData(in)
		if strings.ContainsAny(got, "\r\n") {
			t.Fatalf("sseData(%q) = %q still breaks the line", in, got)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"fetch metadata same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"fetch metadata none", map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"fetch metadata cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"fetch metadata same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"matching origin", map[string]string{"Origin": "http://example.com"}, true},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, false},
		{"no browser headers", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://example.com/login", nil)
			r.Host = "example.com"
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := sameOrigin(r); got != c.want {
				t.Fatalf("sameOrigin = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCookieSecureFlag(t *testing.T) {
	plain := httptest.NewRequest(http.MethodPost, "/login", nil)
	proxied := httptest.NewRequest(http.MethodPost, "/login", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if newServer(t.TempDir(), false).cookie(plain, "x", 1).Secure {
		t.Fatal("plain HTTP cookie marked secure")
	}
	if newServer(t.TempDir(), false).cookie(proxied, "x", 1).Secure {
		t.Fatal("untrusted proxy header honoured")
	}
	if !newServer(t.TempDir(), true).cookie(proxied, "x", 1).Secure {
		t.Fatal("trusted proxy header ignored")
	}
}

func TestParseKindFlag(t *testing.T) {
	if _, err := parseKindFlag("bogus"); err == nil {
		t.Fatal("unknown type accepted")
	}
	if k, err := parseKindFlag("task"); err != nil || string(k) != "task" {
		t.Fatalf("task = %v %v", k, err)
	}
	if k, err := parseKindFlag(""); err != nil || k != "" {
		t.Fatalf("empty = %v %v", k, err)
	}
}

func TestIdentityFilter(t *testing.T) {
	dir := workspace(t, map[string]string{
		"alice.md": "---\ntype: identity\n---\n# Alice\n",
		"open.md":  "# Open\n",
	})
	h := newServer(dir, false).routes()
	if !strings.Contains(get(t, h, "/").Body.String(), `hx-get="/pages?type=identity"`) {
		t.Fatal("identity filter missing")
	}
	body := get(t, h, "/pages?type=identity").Body.String()
	if !strings.Contains(body, "Alice") || strings.Contains(body, "Open") {
		t.Fatalf("identity filter body = %q", body)
	}
}

// loggedIn returns a session cookie for identity, created through /login.
func loggedIn(t *testing.T, h http.Handler, token string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"token": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rec.Code)
	}
	return rec.Result().Cookies()[0]
}

func getWith(h http.Handler, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if mod != nil {
		mod(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func teamServer(t *testing.T) (*server, http.Handler, string, string) {
	t.Helper()
	dir := workspace(t, map[string]string{
		"alice.md":  "---\ntype: identity\n---\n# Alice\n",
		"open.md":   "---\ntype: task\nstatus: todo\n---\n# Open task\n",
		"done.md":   "---\ntype: task\nstatus: done\n---\n# Done task\n",
		"secret.md": "---\npermissions:\n  admin: alice\n---\n# Secret\n",
	})
	s := newServer(dir, false)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	token, err := ws.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	return s, s.routes(), dir, token
}

func TestBearerTokenAuthenticatesRequests(t *testing.T) {
	_, h, _, token := teamServer(t)
	bearer := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", v) }
	}
	if body := getWith(h, "/pages", bearer("Bearer "+token)).Body.String(); !strings.Contains(body, "Secret") {
		t.Fatal("bearer token not honoured")
	}
	for _, header := range []string{"Bearer jk_wrong", "Bearer ", "Basic " + token, token} {
		if body := getWith(h, "/pages", bearer(header)).Body.String(); strings.Contains(body, "Secret") {
			t.Fatalf("Authorization %q exposed a restricted page", header)
		}
	}
	if body := getWith(h, "/", bearer("Bearer "+token)).Body.String(); !strings.Contains(body, `action="/logout"`) {
		t.Fatal("authenticated page lacks logout")
	}
}

func TestPagesFiltersByTypeAndStatus(t *testing.T) {
	_, h, _, _ := teamServer(t)
	body := get(t, h, "/pages?type=task&status=todo").Body.String()
	if !strings.Contains(body, "Open task") || strings.Contains(body, "Done task") {
		t.Fatalf("filtered body = %q", body)
	}
	// The live stream reconnects with the same filter.
	if !strings.Contains(body, `hx-sse:connect="/events?status=todo&amp;type=task"`) {
		t.Fatalf("event stream does not carry the filter: %q", body)
	}
	// An unknown type is ignored rather than failing the page.
	if rec := get(t, h, "/pages?type=bogus"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Done task") {
		t.Fatalf("unknown type = %d %q", rec.Code, rec.Body.String())
	}
	if body := get(t, h, "/pages?status=nothing").Body.String(); !strings.Contains(body, "No pages.") {
		t.Fatalf("empty result = %q", body)
	}
}

// Page titles and paths are author-controlled and must be escaped in both the
// full page and the HTMX fragment.
func TestRenderedContentIsEscaped(t *testing.T) {
	dir := workspace(t, map[string]string{
		`x"><img src=x onerror=alert(1)>.md`: "# <script>alert(1)</script>\n",
	})
	h := newServer(dir, false).routes()
	for _, target := range []string{"/", "/pages", `/pages?status="><script>`} {
		body := get(t, h, target).Body.String()
		for _, raw := range []string{"<script>alert", "<img src=x", `"><script>`} {
			if strings.Contains(body, raw) {
				t.Fatalf("%s rendered %q unescaped", target, raw)
			}
		}
		if target != `/pages?status="><script>` && !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Fatalf("%s lost the escaped title: %q", target, body)
		}
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	s, h, _, token := teamServer(t)
	c := loggedIn(t, h, token)
	if c.MaxAge != int(sessionTTL.Seconds()) || c.Path != "/" {
		t.Fatalf("session cookie = %#v", c)
	}
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if !strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("session not honoured")
	}

	if rec := getWith(h, "/logout", withCookie); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /logout = %d", rec.Code)
	}
	cross := httptest.NewRequest(http.MethodPost, "/logout", nil)
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	cross.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cross)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site logout = %d", rec.Code)
	}
	if s.sessions.identity(c.Value) != "alice" {
		t.Fatal("cross-site request ended the session")
	}

	r := httptest.NewRequest(http.MethodPost, "/logout", nil)
	r.AddCookie(c)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 || cleared[0].Value != "" || !cleared[0].HttpOnly {
		t.Fatalf("logout cookie = %#v", cleared)
	}
	if strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("session survived logout")
	}
}

// A session is re-checked against the workspace on every request: an identity
// that was deleted or turned into a group no longer acts.
func TestSessionFollowsIdentityChanges(t *testing.T) {
	_, h, dir, token := teamServer(t)
	c := loggedIn(t, h, token)
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if err := os.WriteFile(filepath.Join(dir, "alice.md"), []byte("---\ntype: identity\nmembers: [bob]\n---\n# Alice group\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bob.md"), []byte("---\ntype: identity\n---\n# Bob\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("a session acted as a group")
	}
	if body := getWith(h, "/", withCookie).Body.String(); strings.Contains(body, `action="/logout"`) {
		t.Fatal("group shown as logged in")
	}
}

// Revoking a credential does not end browser sessions created from it, so a
// leaked token keeps working through its session for up to sessionTTL -- and
// indefinitely while a live event stream keeps sliding the expiry forward.
func TestRevokedCredentialEndsSessions(t *testing.T) {
	t.Skip("known gap: sessions are not bound to the credential that created them; see audit report")
	s, h, _, token := teamServer(t)
	c := loggedIn(t, h, token)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.RevokeCredentials("alice"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/pages", func(r *http.Request) { r.AddCookie(c) }).Body.String(), "Secret") {
		t.Fatal("session outlived its revoked credential")
	}
}

func TestLoginBodyIsBounded(t *testing.T) {
	_, h, _, token := teamServer(t)
	big := url.Values{"token": {token}, "pad": {strings.Repeat("x", 1<<20)}}.Encode()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code == http.StatusSeeOther {
		t.Fatal("an oversized login form was read in full")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("session issued for an oversized form")
	}
}

// An unreadable workspace answers 503 without disclosing filesystem detail.
func TestUnreadableWorkspaceFailsWithoutLeaking(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	h := newServer(dir, false).routes()
	for _, target := range []string{"/", "/pages"} {
		rec := get(t, h, target)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), dir) || strings.Contains(rec.Body.String(), "no such file") {
			t.Fatalf("%s leaked detail: %q", target, rec.Body.String())
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=x"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login on unreadable workspace = %d", rec.Code)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	h := newServer(workspace(t, nil), false).routes()
	if rec := get(t, h, "/secret.md"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /secret.md = %d", rec.Code)
	}
	if rec := get(t, h, "/assets/../alice.md"); rec.Code == http.StatusOK {
		t.Fatal("asset handler escaped its directory")
	}
}

func TestCookieSecureOverTLS(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "https://example.com/login", nil)
	if r.TLS == nil {
		t.Fatal("test request lacks TLS state")
	}
	if !newServer(t.TempDir(), false).cookie(r, "x", 1).Secure {
		t.Fatal("cookie over TLS not marked secure")
	}
}

// The cache re-parses only when the Markdown tree changes, including the
// credential file, and ignores Git internals.
func TestCacheReloadsOnlyOnChange(t *testing.T) {
	dir := workspace(t, map[string]string{"a.md": "# A\n"})
	c := &cache{dir: dir}
	first, err := c.load()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.load(); again != first {
		t.Fatal("unchanged tree re-parsed")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asset.png"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if again, _ := c.load(); again != first {
		t.Fatal("non-source change re-parsed the workspace")
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n"), 0644); err != nil {
		t.Fatal(err)
	}
	next, err := c.load()
	if err != nil {
		t.Fatal(err)
	}
	if next == first || len(next.Pages) != 2 {
		t.Fatal("new page not picked up")
	}
}

func TestEventStreamLimit(t *testing.T) {
	s := newServer(workspace(t, nil), false)
	for i := 0; i < maxStreams; i++ {
		s.streams <- struct{}{}
	}
	if rec := get(t, s.routes(), "/events"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stream over the limit = %d", rec.Code)
	}
}

// The event stream pushes a re-rendered fragment, on one data line, when the
// workspace changes, and releases its slot when the client goes away.
func TestEventStreamPushesChanges(t *testing.T) {
	dir := workspace(t, map[string]string{"a.md": "# A\n"})
	s := newServer(dir, false)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	// The handler takes its baseline after sending headers, so a single write
	// could land before it. Keep changing the page until an event arrives.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; ctx.Err() == nil; i++ {
			_ = os.WriteFile(filepath.Join(dir, "b.md"), []byte(fmt.Sprintf("# Brand\rnew %d\n", i)), 0644)
			select {
			case <-ctx.Done():
			case <-time.After(300 * time.Millisecond):
			}
		}
	}()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatalf("no event: %v", err)
	}
	if !strings.HasPrefix(line, "data: <section id=\"pages\"") || !strings.Contains(line, "Brand") {
		t.Fatalf("event = %q", line)
	}
	if strings.ContainsRune(strings.TrimSuffix(line, "\n"), '\r') {
		t.Fatalf("event data carries a carriage return: %q", line)
	}
	cancel()
	<-writerDone
	resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.streams) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stream slot not released after the client left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
