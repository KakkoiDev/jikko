package main

import (
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
