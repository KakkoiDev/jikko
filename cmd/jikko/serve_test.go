package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	jikko "github.com/KakkoiDev/jikko"
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
		return postForm(t, h, target, url.Values{"token": {token}}, nil, func(r *http.Request) {
			for k, v := range headers {
				r.Header.Set(k, v)
			}
		})
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
	id, err := s.create("alice", "cred")
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := s.lookup(id); !ok || e.identity != "alice" || e.credential != "cred" {
		t.Fatalf("fresh session = %#v %v", e, ok)
	}
	s.mu.Lock()
	s.m[id] = sessionEntry{identity: "alice", expires: time.Now().Add(-time.Second)}
	s.mu.Unlock()
	s.touch(id)
	if _, ok := s.lookup(id); ok {
		t.Fatal("expired session accepted or revived")
	}
	id2, _ := s.create("bob", "cred")
	s.drop(id2)
	if _, ok := s.lookup(id2); ok {
		t.Fatal("dropped session accepted")
	}
	if _, ok := s.lookup(""); ok {
		t.Fatal("empty session id accepted")
	}

	// lookup leaves the expiry alone; touch slides it.
	id3, _ := s.create("carol", "cred")
	s.mu.Lock()
	e := s.m[id3]
	e.expires = time.Now().Add(time.Minute)
	s.m[id3] = e
	s.mu.Unlock()
	if got, _ := s.lookup(id3); !got.expires.Equal(e.expires) {
		t.Fatal("lookup extended the session")
	}
	s.touch(id3)
	if got, _ := s.lookup(id3); !got.expires.After(e.expires) {
		t.Fatal("touch did not extend the session")
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
	rec := postForm(t, h, "/login", url.Values{"token": {token}}, nil, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rec.Code)
	}
	return rec.Result().Cookies()[0]
}

var csrfField = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// formToken fetches the anti-forgery token the home page renders for a
// caller with the given cookies. It returns the token and the cookies the
// caller holds afterwards.
func formToken(t *testing.T, h http.Handler, cookies []*http.Cookie) (string, []*http.Cookie) {
	t.Helper()
	rec := getWith(h, "/", func(r *http.Request) {
		for _, c := range cookies {
			r.AddCookie(c)
		}
	})
	m := csrfField.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no anti-forgery token on the page (status %d)", rec.Code)
	}
	return m[1], append(append([]*http.Cookie(nil), cookies...), rec.Result().Cookies()...)
}

// postForm posts a form the way the browser interface does: with the
// caller's cookies and a valid anti-forgery token.
func postForm(t *testing.T, h http.Handler, target string, form url.Values, cookies []*http.Cookie, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	token, cookies := formToken(t, h, cookies)
	form = cloneValues(form)
	form.Set("_csrf", token)
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if mod != nil {
		mod(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
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
	if e, ok := s.sessions.lookup(c.Value); !ok || e.identity != "alice" {
		t.Fatal("cross-site request ended the session")
	}

	if rec := postForm(t, h, "/logout", nil, []*http.Cookie{c}, func(r *http.Request) { r.Form = nil; r.Body = http.NoBody }); rec.Code != http.StatusForbidden {
		t.Fatalf("logout without a token = %d", rec.Code)
	}
	rec = postForm(t, h, "/logout", nil, []*http.Cookie{c}, nil)
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

// A session is bound to the credential it was created from. Revoking that
// credential ends the session on its next request, for good: creating a new
// credential for the same identity does not revive it.
func TestRevokedCredentialEndsSessions(t *testing.T) {
	s, h, _, token := teamServer(t)
	c := loggedIn(t, h, token)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.RevokeCredentials("alice"); err != nil {
		t.Fatal(err)
	}
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("session outlived its revoked credential")
	}
	if _, ok := s.sessions.lookup(c.Value); ok {
		t.Fatal("session of a revoked credential was kept")
	}
	if _, err := ws.CreateCredential("alice"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("a new credential revived an ended session")
	}
}

// Another credential of the same identity keeps its own sessions.
func TestRevocationIsPerCredentialSession(t *testing.T) {
	s, h, dir, token := teamServer(t)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ws.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := loggedIn(t, h, token), loggedIn(t, h, other)
	// Drop only the first credential, by hand, as an operator editing
	// .auth.md would.
	a, err := jikko.LoadAuth(dir)
	if err != nil {
		t.Fatal(err)
	}
	kept := a.Credentials[1:]
	var b strings.Builder
	b.WriteString("---\ncredentials:\n")
	for _, cred := range kept {
		fmt.Fprintf(&b, "  - identity: %s\n    token_hash: %s\n", cred.Identity, cred.TokenHash)
	}
	b.WriteString("---\n")
	if err := os.WriteFile(filepath.Join(dir, ".auth.md"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/pages", func(r *http.Request) { r.AddCookie(c1) }).Body.String(), "Secret") {
		t.Fatal("session of the removed credential survived")
	}
	if !strings.Contains(getWith(h, "/pages", func(r *http.Request) { r.AddCookie(c2) }).Body.String(), "Secret") {
		t.Fatal("session of the remaining credential ended")
	}
}

// Deleting the identity behind a credential ends its sessions, and they stay
// ended when an identity of the same name is created again.
func TestDeletedIdentityEndsSessions(t *testing.T) {
	s, h, dir, token := teamServer(t)
	c := loggedIn(t, h, token)
	withCookie := func(r *http.Request) { r.AddCookie(c) }
	if err := os.Remove(filepath.Join(dir, "alice.md")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/", withCookie).Body.String(), `action="/logout"`) {
		t.Fatal("session outlived its identity")
	}
	if _, ok := s.sessions.lookup(c.Value); ok {
		t.Fatal("session of a deleted identity was kept")
	}
	if err := os.WriteFile(filepath.Join(dir, "alice.md"), []byte("---\ntype: identity\n---\n# Alice again\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(getWith(h, "/pages", withCookie).Body.String(), "Secret") {
		t.Fatal("ended session revived by a same-named identity")
	}
}

// An open event stream re-checks its session: it closes once the credential is
// revoked, and while it runs it does not slide the session's expiry forward.
func TestEventStreamEndsWithRevokedCredential(t *testing.T) {
	s, h, _, token := teamServer(t)
	c := loggedIn(t, h, token)
	before, ok := s.sessions.lookup(c.Value)
	if !ok {
		t.Fatal("no session")
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(c)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	time.Sleep(2500 * time.Millisecond) // a few stream ticks
	after, ok := s.sessions.lookup(c.Value)
	if !ok {
		t.Fatal("session ended while its credential stood")
	}
	if !after.expires.Equal(before.expires) {
		t.Fatal("the event stream extended the session")
	}

	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.RevokeCredentials("alice"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream ended with %v, want a clean close", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("event stream stayed open after its credential was revoked")
	}
	if _, ok := s.sessions.lookup(c.Value); ok {
		t.Fatal("session of a revoked credential was kept")
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
	s := newServer(dir, false)
	h := s.routes()
	for _, target := range []string{"/", "/pages"} {
		rec := get(t, h, target)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), dir) || strings.Contains(rec.Body.String(), "no such file") {
			t.Fatalf("%s leaked detail: %q", target, rec.Body.String())
		}
	}
	pre := strings.Repeat("p", 43)
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"token": {"x"}, "_csrf": {s.sign("pre", pre)}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: preSessionCookie, Value: pre})
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

// Every response carries the security headers, errors and assets included.
func TestSecurityHeaders(t *testing.T) {
	_, h, _, _ := teamServer(t)
	for _, target := range []string{"/", "/pages", "/assets/htmx.min.js", "/missing"} {
		rec := get(t, h, target)
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Fatalf("%s: CSP %q lacks %q", target, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-") {
			t.Fatalf("%s: CSP relaxes itself: %q", target, csp)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: headers = %v", target, rec.Header())
		}
	}
}

// The CSP forbids inline script and style, so the page must not need them.
// This guards the templates against a change the policy would silently break.
func TestPageNeedsNoInlineCode(t *testing.T) {
	_, h, _, token := teamServer(t)
	for _, mod := range []func(*http.Request){nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }} {
		for _, target := range []string{"/", "/pages"} {
			body := getWith(h, target, mod).Body.String()
			lower := strings.ToLower(body)
			for _, banned := range []string{"<style", " style=", "javascript:", "hx-on", "hx-live"} {
				if strings.Contains(lower, banned) {
					t.Fatalf("%s contains %q, which the CSP blocks", target, banned)
				}
			}
			if regexp.MustCompile(`\son[a-z]+=`).MatchString(lower) {
				t.Fatalf("%s has an inline event handler", target)
			}
			for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(lower, -1) {
				if !strings.Contains(tag, `src="/assets/`) {
					t.Fatalf("%s has an inline or foreign script: %s", target, tag)
				}
			}
		}
	}
}
