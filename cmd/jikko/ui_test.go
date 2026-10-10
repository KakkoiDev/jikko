package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type uiTeam struct {
	s       *server
	h       http.Handler
	dir     string
	tokens  map[string]string
	cookies map[string]*http.Cookie
}

// uiWorkspace builds a team with one identity per capability on plan.md:
// alice administers, bob writes, carol comments, dave reads.
func uiWorkspace(t *testing.T) *uiTeam {
	t.Helper()
	dir := workspace(t, map[string]string{
		"alice.md":  "---\ntype: identity\n---\n# Alice\n",
		"bob.md":    "---\ntype: identity\n---\n# Bob\n",
		"carol.md":  "---\ntype: identity\n---\n# Carol\n",
		"dave.md":   "---\ntype: identity\n---\n# Dave\n",
		"plan.md":   "---\nstatus: todo\npermissions:\n  read: dave\n  comment: carol\n  write: bob\n  admin: alice\n---\n# Plan\n\nShip the first version.\n\nThen measure it.\n",
		"open.md":   "# Open <b>page</b>\n\n<script>alert(1)</script> See [[plan]] and [[secret]].\n\n![[img.png]]\n\n![[notes.txt]]\n",
		"secret.md": "---\npermissions:\n  admin: alice\n---\n# Secret\n\n![[private.png]]\n",
		"board.md":  "---\ntype: view\nfilter:\n  type: task\nview:\n  layout: board\n  group: status\n---\n",
		"t1.md":     "---\ntype: task\nstatus: todo\n---\n# First task\n",
		"t2.md":     "---\ntype: task\nstatus: done\n---\n# Second task\n",
	})
	for name, content := range map[string]string{"img.png": "\x89PNG", "private.png": "\x89PNG", "unused.png": "\x89PNG", "notes.txt": "notes"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := newServer(dir, false)
	ws, err := s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	team := &uiTeam{s: s, h: s.routes(), dir: dir, tokens: map[string]string{}, cookies: map[string]*http.Cookie{}}
	for _, name := range []string{"alice", "bob", "carol", "dave"} {
		token, err := ws.CreateCredential(name)
		if err != nil {
			t.Fatal(err)
		}
		team.tokens[name] = token
		team.cookies[name] = loggedIn(t, team.h, token)
	}
	return team
}

func (u *uiTeam) get(t *testing.T, who, target string) *httptest.ResponseRecorder {
	t.Helper()
	return getWith(u.h, target, func(r *http.Request) {
		if c := u.cookies[who]; c != nil {
			r.AddCookie(c)
		}
	})
}

// post submits a form as who ("" for nobody) with a valid token.
func (u *uiTeam) post(t *testing.T, who, target string, form url.Values, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var cookies []*http.Cookie
	if c := u.cookies[who]; c != nil {
		cookies = append(cookies, c)
	}
	return postForm(t, u.h, target, form, cookies, mod)
}

func (u *uiTeam) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(u.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func htmx(r *http.Request) { r.Header.Set("HX-Request", "true") }

var hiddenField = regexp.MustCompile(`name="(rev|base|yours|against)" value="([^"]*)"`)

// editForm returns the hidden fields of the edit form as served.
func editForm(t *testing.T, body string) url.Values {
	t.Helper()
	out := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(body, -1) {
		out.Set(m[1], html.UnescapeString(m[2]))
	}
	if out.Get("rev") == "" || out.Get("base") == "" {
		t.Fatalf("no edit form in:\n%s", body)
	}
	return out
}

func TestPageDetail(t *testing.T) {
	u := uiWorkspace(t)
	rec := u.get(t, "", "/p/open.md")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /p/open.md = %d", rec.Code)
	}
	for _, want := range []string{
		`<h1>Open &lt;b&gt;page&lt;/b&gt;</h1>`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`<span class="jk-unresolved">plan</span>`, // anonymous may not read plan.md
		`<img src="/files/img.png"`,
		"Log in to take part.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "/edit/") || strings.Contains(body, "New comment") {
		t.Fatal("mutation affordances shown to an anonymous caller")
	}
	// Bob sees the link to plan, and the edit and comment forms on it.
	if body := u.get(t, "bob", "/p/open.md").Body.String(); !strings.Contains(body, `<a class="jk-ref" href="/p/plan.md">Plan</a>`) {
		t.Fatalf("readable link not rendered for bob:\n%s", body)
	}
	plan := u.get(t, "bob", "/p/plan.md").Body.String()
	for _, want := range []string{`href="/edit/plan.md"`, "New comment", "<dt>status</dt><dd>todo</dd>", `data-rev="`} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan for bob lacks %q", want)
		}
	}
	// Dave reads only: no edit link, no comment form.
	dave := u.get(t, "dave", "/p/plan.md").Body.String()
	if strings.Contains(dave, `href="/edit/`) || strings.Contains(dave, "New comment") {
		t.Fatal("a reader was offered mutations")
	}
	// Carol comments but does not edit.
	carol := u.get(t, "carol", "/p/plan.md").Body.String()
	if strings.Contains(carol, `href="/edit/`) || !strings.Contains(carol, "New comment") {
		t.Fatal("commenter affordances wrong")
	}
	for who, target := range map[string]string{"": "/p/secret.md", "bob": "/p/secret.md", "alice": "/p/missing.md"} {
		if rec := u.get(t, who, target); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Secret") {
			t.Fatalf("%s %s = %d", who, target, rec.Code)
		}
	}
	if rec := u.get(t, "alice", "/p/secret.md"); rec.Code != http.StatusOK {
		t.Fatalf("admin read = %d", rec.Code)
	}
	if rec := u.get(t, "bob", "/p/plan.md"); rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("page detail lacks a CSP")
	}
}

func TestViewPageRendersBoard(t *testing.T) {
	u := uiWorkspace(t)
	body := u.get(t, "", "/p/board.md").Body.String()
	for _, want := range []string{`<div class="jk-board" data-layout="board">`, `<h3>todo <span class="badge" data-variant="outline">1</span></h3>`, `<a href="/p/t1.md">First task</a>`, `<a href="/p/t2.md">Second task</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("board lacks %q:\n%s", want, body)
		}
	}
}

func TestEditPageAuthorization(t *testing.T) {
	u := uiWorkspace(t)
	for who, want := range map[string]int{"alice": 200, "bob": 200, "carol": 403, "dave": 403, "": 404} {
		if rec := u.get(t, who, "/edit/plan.md"); rec.Code != want {
			t.Errorf("%q GET /edit/plan.md = %d, want %d", who, rec.Code, want)
		}
	}
	if rec := u.get(t, "", "/edit/open.md"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous edit of an open page = %d", rec.Code)
	}
	src := u.get(t, "bob", "/edit/plan.md?mode=source").Body.String()
	if !strings.Contains(src, `name="source"`) || !strings.Contains(src, "permissions:\n  read: dave") {
		t.Fatalf("source editor:\n%s", src)
	}
}

func TestSaveFields(t *testing.T) {
	u := uiWorkspace(t)
	form := editForm(t, u.get(t, "bob", "/edit/plan.md").Body.String())
	form.Set("mode", "fields")
	form.Set("f:status", "doing")
	form.Set("new_key", "due")
	form.Set("new_value", "2026-10-20")
	form.Set("body", "# Plan\r\n\r\nShip the first version, carefully.\r\n\r\nThen measure it.\r\n")
	rec := u.post(t, "bob", "/save/plan.md", form, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/p/plan.md" {
		t.Fatalf("save = %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	want := "---\nstatus: doing\npermissions:\n  read: dave\n  comment: carol\n  write: bob\n  admin: alice\ndue: 2026-10-20\n---\n# Plan\n\nShip the first version, carefully.\n\nThen measure it.\n"
	if got := u.read(t, "plan.md"); got != want {
		t.Fatalf("plan.md =\n%s", got)
	}
	// With HTMX the page section comes back in place, with a message.
	form = editForm(t, u.get(t, "bob", "/edit/plan.md").Body.String())
	form.Set("mode", "fields")
	form.Set("f:status", "done")
	form.Set("body", "# Plan\n\nShipped.\n")
	rec = u.post(t, "bob", "/save/plan.md", form, htmx)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), `<section id="page"`) || !strings.Contains(rec.Body.String(), "Saved.") {
		t.Fatalf("htmx save = %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestSaveRequiresAntiForgeryToken(t *testing.T) {
	u := uiWorkspace(t)
	before := u.read(t, "plan.md")
	form := editForm(t, u.get(t, "bob", "/edit/plan.md").Body.String())
	form.Set("mode", "source")
	form.Set("source", strings.Replace(before, "Ship", "Forged:", 1))
	send := func(form url.Values, mod func(*http.Request)) int {
		r := httptest.NewRequest(http.MethodPost, "/save/plan.md", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(u.cookies["bob"])
		if mod != nil {
			mod(r)
		}
		rec := httptest.NewRecorder()
		u.h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := send(form, nil); code != http.StatusForbidden {
		t.Fatalf("no token = %d", code)
	}
	forged := cloneValues(form)
	forged.Set("_csrf", "forged")
	if code := send(forged, nil); code != http.StatusForbidden {
		t.Fatalf("forged token = %d", code)
	}
	// A token issued to another session does not work with bob's cookie.
	other, _ := formToken(t, u.h, []*http.Cookie{u.cookies["dave"]})
	stolen := cloneValues(form)
	stolen.Set("_csrf", other)
	if code := send(stolen, nil); code != http.StatusForbidden {
		t.Fatalf("another session's token = %d", code)
	}
	// A valid token in the header works the same as in the form, but not
	// from another site.
	own, _ := formToken(t, u.h, []*http.Cookie{u.cookies["bob"]})
	if code := send(form, func(r *http.Request) {
		r.Header.Set("X-CSRF-Token", own)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); code != http.StatusForbidden {
		t.Fatalf("cross-site = %d", code)
	}
	if u.read(t, "plan.md") != before {
		t.Fatal("a refused request wrote the page")
	}
	if code := send(form, func(r *http.Request) { r.Header.Set("X-CSRF-Token", own) }); code != http.StatusSeeOther {
		t.Fatalf("header token = %d", code)
	}
	// A bearer token is not an ambient credential and needs no form token.
	form = editForm(t, u.get(t, "bob", "/edit/plan.md").Body.String())
	form.Set("mode", "source")
	form.Set("source", "# By API\n")
	r := httptest.NewRequest(http.MethodPost, "/save/plan.md", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer "+u.tokens["alice"])
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther || u.read(t, "plan.md") != "# By API\n" {
		t.Fatalf("bearer save = %d", rec.Code)
	}
}

func TestSaveAuthorization(t *testing.T) {
	u := uiWorkspace(t)
	before := u.read(t, "plan.md")
	form := editForm(t, u.get(t, "alice", "/edit/plan.md").Body.String())
	form.Set("mode", "source")
	form.Set("source", "# Changed\n")
	for who, want := range map[string]int{"carol": 403, "dave": 403, "": 401} {
		if rec := u.post(t, who, "/save/plan.md", form, nil); rec.Code != want {
			t.Errorf("%q save = %d, want %d", who, rec.Code, want)
		}
	}
	if rec := u.post(t, "bob", "/save/secret.md", form, nil); rec.Code != http.StatusNotFound {
		t.Errorf("save of an unreadable page = %d", rec.Code)
	}
	// Bob writes, but write does not include the access policy.
	widened := cloneValues(form)
	widened.Set("source", strings.Replace(before, "read: dave", "read: [dave, carol]", 1))
	rec := u.post(t, "bob", "/save/plan.md", widened, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "admin") {
		t.Fatalf("policy change by a writer = %d\n%s", rec.Code, rec.Body.String())
	}
	if u.read(t, "plan.md") != before {
		t.Fatal("a refused save wrote the page")
	}
	// The fields form cannot touch permissions either.
	fields := editForm(t, u.get(t, "bob", "/edit/plan.md").Body.String())
	fields.Set("mode", "fields")
	fields.Set("rm:permissions", "1")
	fields.Set("body", "x")
	if rec := u.post(t, "bob", "/save/plan.md", fields, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("permissions removal through fields = %d", rec.Code)
	}
	if u.read(t, "plan.md") != before {
		t.Fatal("a refused save wrote the page")
	}
	if rec := u.post(t, "alice", "/save/plan.md", widened, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("admin policy change = %d\n%s", rec.Code, rec.Body.String())
	}
	// A malformed form is refused, not guessed at.
	broken := cloneValues(form)
	broken.Set("base", "%%%")
	if rec := u.post(t, "alice", "/save/plan.md", broken, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken base = %d", rec.Code)
	}
}

func TestSaveMergesAndShowsConflicts(t *testing.T) {
	u := uiWorkspace(t)
	form := editForm(t, u.get(t, "bob", "/edit/plan.md?mode=source").Body.String())
	base, err := base64.StdEncoding.DecodeString(form.Get("base"))
	if err != nil {
		t.Fatal(err)
	}
	// Someone else edits the last paragraph meanwhile.
	theirs := strings.Replace(string(base), "Then measure it.", "Then measure it twice.", 1)
	if err := os.WriteFile(filepath.Join(u.dir, "plan.md"), []byte(theirs), 0644); err != nil {
		t.Fatal(err)
	}
	clean := cloneValues(form)
	clean.Set("mode", "source")
	clean.Set("source", strings.Replace(string(base), "Ship the first version.", "Ship version one.", 1))
	rec := u.post(t, "bob", "/save/plan.md", clean, htmx)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "both edits were merged") {
		t.Fatalf("clean merge = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := u.read(t, "plan.md"); !strings.Contains(got, "Ship version one.") || !strings.Contains(got, "measure it twice") {
		t.Fatalf("merged =\n%s", got)
	}

	// Now both change the same paragraph.
	form = editForm(t, u.get(t, "bob", "/edit/plan.md?mode=source").Body.String())
	base, _ = base64.StdEncoding.DecodeString(form.Get("base"))
	theirs = strings.Replace(string(base), "Ship version one.", "Ship version one today.", 1)
	if err := os.WriteFile(filepath.Join(u.dir, "plan.md"), []byte(theirs), 0644); err != nil {
		t.Fatal(err)
	}
	mine := cloneValues(form)
	mine.Set("mode", "source")
	mine.Set("source", strings.Replace(string(base), "Ship version one.", "Ship version one tomorrow.", 1))
	rec = u.post(t, "bob", "/save/plan.md", mine, htmx)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict || !strings.Contains(body, "This page changed while you were editing") || !strings.Contains(body, `name="resolve:body:0"`) {
		t.Fatalf("conflict = %d\n%s", rec.Code, body)
	}
	if strings.Contains(body, "&lt;&lt;&lt;&lt;&lt;&lt;&lt;") || strings.Contains(body, "<<<<<<<") {
		t.Fatal("raw conflict markers shown")
	}
	if u.read(t, "plan.md") != theirs {
		t.Fatal("a conflict wrote the page")
	}
	// The conflict form carries everything needed to settle it.
	resolve := url.Values{}
	for _, m := range hiddenField.FindAllStringSubmatch(body[:strings.Index(body, "Save with these choices")], -1) {
		resolve.Set(m[1], html.UnescapeString(m[2]))
	}
	resolve.Set("mode", "resolve")
	resolve.Set("resolve:body:0", "yours")
	rec = u.post(t, "bob", "/save/plan.md", resolve, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("resolve = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := u.read(t, "plan.md"); !strings.Contains(got, "Ship version one tomorrow.") {
		t.Fatalf("resolved =\n%s", got)
	}
}

func TestCommentForms(t *testing.T) {
	u := uiWorkspace(t)
	add := url.Values{"op": {"add"}, "anchor": {"first version"}, "text": {"Which features? @bob"}}
	if rec := u.post(t, "dave", "/comment/plan.md", add, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader commented: %d", rec.Code)
	}
	if rec := u.post(t, "", "/comment/plan.md", add, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous commented: %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/comment/plan.md", strings.NewReader(add.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(u.cookies["carol"])
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("comment without a token = %d", rec.Code)
	}
	if rec := u.post(t, "carol", "/comment/plan.md", add, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := u.read(t, "plan.md"); !strings.Contains(got, "<!--comment:c1-->first version<!--/comment:c1-->") || !strings.Contains(got, "@carol: Which features? @bob") {
		t.Fatalf("plan.md =\n%s", got)
	}
	page := u.get(t, "dave", "/p/plan.md").Body.String()
	for _, want := range []string{`<mark class="jk-anchor" id="anchor-c1">first version</mark>`, `id="comment-c1"`, "<strong>@carol</strong> Which features?", `<a class="jk-mention" href="/p/bob.md">@bob</a>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `name="op" value="reply"`) {
		t.Fatal("a reader was offered a reply form")
	}
	rec = u.post(t, "bob", "/comment/plan.md", url.Values{"op": {"reply"}, "id": {"c1"}, "text": {"The core ones."}}, htmx)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Replied to c1.") || !strings.Contains(rec.Body.String(), "The core ones.") {
		t.Fatalf("reply = %d\n%s", rec.Code, rec.Body.String())
	}
	if rec := u.post(t, "carol", "/comment/plan.md", url.Values{"op": {"add"}, "anchor": {"not in the page"}, "text": {"x"}}, htmx); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("bad anchor = %d", rec.Code)
	}
	if rec := u.post(t, "carol", "/comment/plan.md", url.Values{"op": {"explode"}}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown op = %d", rec.Code)
	}
	if rec := u.post(t, "carol", "/comment/plan.md", url.Values{"op": {"resolve"}, "id": {"c1"}}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("resolve = %d", rec.Code)
	}
	if got := u.read(t, "plan.md"); strings.Contains(got, "<!--comment") || !strings.Contains(got, "Ship the first version.") {
		t.Fatalf("after resolve =\n%s", got)
	}
}

func TestFilesFollowPagePermissions(t *testing.T) {
	u := uiWorkspace(t)
	rec := u.get(t, "", "/files/img.png")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("img.png = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("file CSP = %q", csp)
	}
	if rec := u.get(t, "", "/files/notes.txt"); rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("notes.txt = %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	for who, target := range map[string]string{"": "/files/private.png", "bob": "/files/private.png", "alice": "/files/unused.png"} {
		if rec := u.get(t, who, target); rec.Code != http.StatusNotFound {
			t.Errorf("%q %s = %d", who, target, rec.Code)
		}
	}
	for _, target := range []string{"/files/.auth.md", "/files/plan.md", "/files/../plan.md"} {
		if rec := u.get(t, "alice", target); rec.Code == http.StatusOK {
			t.Errorf("%s served", target)
		}
	}
	if rec := u.get(t, "alice", "/files/private.png"); rec.Code != http.StatusOK {
		t.Fatalf("admin private.png = %d", rec.Code)
	}
}

// The CSP forbids inline script and style; the new pages must not need them.
func TestInterfacePagesNeedNoInlineCode(t *testing.T) {
	u := uiWorkspace(t)
	form := editForm(t, u.get(t, "bob", "/edit/plan.md?mode=source").Body.String())
	base, _ := base64.StdEncoding.DecodeString(form.Get("base"))
	if err := os.WriteFile(filepath.Join(u.dir, "plan.md"), []byte(strings.Replace(string(base), "Ship", "Release", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	form.Set("mode", "source")
	form.Set("source", strings.Replace(string(base), "Ship", "Launch", 1))
	conflict := u.post(t, "bob", "/save/plan.md", form, nil).Body.String()
	pages := map[string]string{
		"detail":   u.get(t, "bob", "/p/plan.md").Body.String(),
		"edit":     u.get(t, "bob", "/edit/plan.md").Body.String(),
		"source":   u.get(t, "bob", "/edit/plan.md?mode=source").Body.String(),
		"view":     u.get(t, "bob", "/p/board.md").Body.String(),
		"open":     u.get(t, "", "/p/open.md").Body.String(),
		"conflict": conflict,
		"missing":  u.get(t, "", "/p/nope.md").Body.String(),
	}
	for name, body := range pages {
		lower := strings.ToLower(body)
		for _, banned := range []string{"<style", " style=", "javascript:", "hx-on", "hx-live"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
		if regexp.MustCompile(`\son[a-z]+=`).MatchString(lower) {
			t.Errorf("%s has an inline event handler", name)
		}
		for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(lower, -1) {
			if !strings.Contains(tag, `src="/assets/`) {
				t.Errorf("%s has an inline or foreign script: %s", name, tag)
			}
		}
	}
	if !strings.Contains(pages["conflict"], "jk-conflict") {
		t.Fatal("conflict page not rendered")
	}
}

// uploadForm posts a multipart upload as who, with a token unless noToken.
func (u *uiTeam) upload(t *testing.T, who, target, name string, data []byte, noToken bool) *httptest.ResponseRecorder {
	t.Helper()
	var cookies []*http.Cookie
	if c := u.cookies[who]; c != nil {
		cookies = append(cookies, c)
	}
	token, cookies := formToken(t, u.h, cookies)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if !noToken {
		_ = mw.WriteField("_csrf", token)
	}
	if name != "" {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, target, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, r)
	return rec
}

func TestUploadForm(t *testing.T) {
	u := uiWorkspace(t)
	before := u.read(t, "plan.md")
	if rec := u.upload(t, "bob", "/upload/plan.md", "chart.png", []byte("png"), true); rec.Code != http.StatusForbidden {
		t.Fatalf("upload without a token = %d", rec.Code)
	}
	for who, want := range map[string]int{"carol": 403, "dave": 403, "": 401} {
		if rec := u.upload(t, who, "/upload/plan.md", "chart.png", []byte("png"), false); rec.Code != want {
			t.Errorf("%q upload = %d, want %d", who, rec.Code, want)
		}
	}
	if rec := u.upload(t, "bob", "/upload/plan.md", "", nil, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("upload without a file = %d", rec.Code)
	}
	u.s.maxUpload = 2
	if rec := u.upload(t, "bob", "/upload/plan.md", "chart.png", []byte("png"), false); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload = %d", rec.Code)
	}
	if rec := u.upload(t, "bob", "/upload/plan.md", ".env", []byte("x"), false); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("dotfile upload = %d", rec.Code)
	}
	if u.read(t, "plan.md") != before {
		t.Fatal("a refused upload changed the page")
	}
	if _, err := os.Stat(filepath.Join(u.dir, "chart.png")); !os.IsNotExist(err) {
		t.Fatal("a refused upload wrote a file")
	}
	u.s.maxUpload = 1 << 20
	if rec := u.upload(t, "bob", "/upload/plan.md", "chart.png", []byte("png"), false); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d\n%s", rec.Code, rec.Body.String())
	}
	if got := u.read(t, "plan.md"); !strings.HasSuffix(got, "Then measure it.\n\n![[chart.png]]\n") {
		t.Fatalf("plan.md = %q", got)
	}
	// The embedded file is now served to readers of plan.md only.
	if rec := u.get(t, "dave", "/files/chart.png"); rec.Code != http.StatusOK {
		t.Fatalf("reader fetch = %d", rec.Code)
	}
	if rec := u.get(t, "", "/files/chart.png"); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous fetch = %d", rec.Code)
	}
	if !strings.Contains(u.get(t, "bob", "/p/plan.md").Body.String(), `enctype="multipart/form-data"`) || strings.Contains(u.get(t, "carol", "/p/plan.md").Body.String(), "Upload and embed") {
		t.Fatal("upload form offered to the wrong callers")
	}
}

func TestPageEventsNotifyOnce(t *testing.T) {
	u := uiWorkspace(t)
	srv := httptest.NewServer(u.h)
	defer srv.Close()
	rev := regexp.MustCompile(`data-rev="([0-9a-f]+)"`).FindStringSubmatch(u.get(t, "bob", "/p/plan.md").Body.String())[1]
	if !strings.Contains(u.get(t, "bob", "/edit/plan.md").Body.String(), `hx-sse:connect="/events/page/plan.md?mode=edit&amp;rev=`+rev+`"`) {
		t.Fatal("the editor does not listen for changes")
	}
	open := func(who string) (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events/page/plan.md?mode=edit&rev="+rev, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c := u.cookies[who]; c != nil {
			req.AddCookie(c)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp, cancel
	}
	// A caller who may not read the page gets nothing, and the stream ends.
	resp, cancel := open("")
	body, err := io.ReadAll(resp.Body)
	cancel()
	resp.Body.Close()
	if err != nil || len(body) != 0 {
		t.Fatalf("anonymous stream: %q %v", body, err)
	}
	resp, cancel = open("bob")
	defer cancel()
	defer resp.Body.Close()
	time.Sleep(1200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(u.dir, "plan.md"), []byte(strings.Replace(u.read(t, "plan.md"), "Ship", "Release", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(all), `data: <div id="jk-live"`) || !strings.Contains(string(all), "changed after you opened the editor") || strings.Count(string(all), "data:") != 1 {
		t.Fatalf("stream = %q", all)
	}
}

// Every state-changing route refuses a request without an anti-forgery
// token and a cross-site request with one, so a route added later cannot
// quietly skip the check.
func TestEveryPostNeedsAntiForgeryToken(t *testing.T) {
	u := uiWorkspace(t)
	before := u.read(t, "plan.md")
	own, _ := formToken(t, u.h, []*http.Cookie{u.cookies["bob"]})
	for _, target := range []string{"/login", "/logout", "/save/plan.md", "/comment/plan.md", "/upload/plan.md"} {
		send := func(form url.Values, mod func(*http.Request)) int {
			r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(u.cookies["bob"])
			if mod != nil {
				mod(r)
			}
			rec := httptest.NewRecorder()
			u.h.ServeHTTP(rec, r)
			return rec.Code
		}
		form := url.Values{"token": {u.tokens["bob"]}, "op": {"add"}, "anchor": {"Ship"}, "text": {"forged"}}
		if code := send(form, nil); code != http.StatusForbidden {
			t.Errorf("POST %s without a token = %d", target, code)
		}
		withToken := cloneValues(form)
		withToken.Set("_csrf", own)
		if code := send(withToken, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); code != http.StatusForbidden {
			t.Errorf("cross-origin POST %s = %d", target, code)
		}
	}
	if u.read(t, "plan.md") != before || len(u.get(t, "bob", "/p/plan.md").Body.String()) == 0 {
		t.Fatal("a refused request changed the page")
	}
	if _, ok := u.s.sessions.lookup(u.cookies["bob"].Value); !ok {
		t.Fatal("a refused logout ended the session")
	}
}

// A body larger than a route accepts is refused as such, before the form is
// parsed: a cut-off body must not read as a missing token.
func TestOversizedBodyIsRefusedAsSuch(t *testing.T) {
	u := uiWorkspace(t)
	own, _ := formToken(t, u.h, []*http.Cookie{u.cookies["bob"]})
	r := httptest.NewRequest(http.MethodPost, "/save/plan.md", strings.NewReader("_csrf="+own+"&source="+strings.Repeat("a", maxFormBody)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(u.cookies["bob"])
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized save = %d %q", rec.Code, rec.Body.String())
	}
	u.s.maxUpload = 16
	if rec := u.upload(t, "bob", "/upload/plan.md", "big.bin", bytes.Repeat([]byte("x"), maxFormBody+32), false); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload = %d", rec.Code)
	}
}

// The page stream follows the session and the page's policy like the list
// stream: it never extends the session, it ends when the credential is
// revoked or the page can no longer be read, sending nothing, and it counts
// against the connection limit.
func TestPageEventsFollowSessionAndAccess(t *testing.T) {
	u := uiWorkspace(t)
	rev := regexp.MustCompile(`data-rev="([0-9a-f]+)"`).FindStringSubmatch(u.get(t, "dave", "/p/plan.md").Body.String())[1]
	for i := 0; i < maxStreams; i++ {
		u.s.streams <- struct{}{}
	}
	if rec := u.get(t, "dave", "/events/page/plan.md?rev="+rev); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stream over the limit = %d", rec.Code)
	}
	for i := 0; i < maxStreams; i++ {
		<-u.s.streams
	}
	srv := httptest.NewServer(u.h)
	defer srv.Close()
	open := func(who string) *http.Response {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events/page/plan.md?rev="+rev, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(u.cookies[who])
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	closedWithout := func(resp *http.Response, what string) {
		t.Helper()
		done := make(chan []byte, 1)
		go func() {
			b, _ := io.ReadAll(resp.Body)
			done <- b
		}()
		select {
		case b := <-done:
			if len(b) != 0 {
				t.Fatalf("stream sent %q after %s", b, what)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("stream stayed open after %s", what)
		}
	}
	dave, bob := open("dave"), open("bob")
	before, _ := u.s.sessions.lookup(u.cookies["bob"].Value)
	time.Sleep(2500 * time.Millisecond) // a few stream ticks
	if after, ok := u.s.sessions.lookup(u.cookies["bob"].Value); !ok || !after.expires.Equal(before.expires) {
		t.Fatal("the page stream extended the session")
	}
	ws, err := u.s.cache.load()
	if err != nil {
		t.Fatal(err)
	}
	// bob's credential is revoked; then dave loses read on plan.md, which
	// also changes the page, and is told nothing.
	if err := ws.RevokeCredentials("bob"); err != nil {
		t.Fatal(err)
	}
	closedWithout(bob, "revocation")
	if _, ok := u.s.sessions.lookup(u.cookies["bob"].Value); ok {
		t.Fatal("session of a revoked credential was kept")
	}
	if err := ws.SetPermission("alice", "plan", "read"); err != nil {
		t.Fatal(err)
	}
	closedWithout(dave, "losing read access")
}

func TestCreatePageForm(t *testing.T) {
	u := uiWorkspace(t)
	if rec := u.get(t, "", "/new"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /new = %d", rec.Code)
	}
	if body := u.get(t, "bob", "/new?type=task").Body.String(); !strings.Contains(body, `name="type" value="task"`) {
		t.Fatalf("task form not offered:\n%s", body)
	}
	for _, tc := range []struct{ path, typ, body, want string }{
		{"notes/meeting", "document", "# Meeting\n\nNotes.", "# Meeting\n\nNotes.\n"},
		{"tasks/review", "task", "", "---\ntype: task\nstatus: todo\n---\n# review\n"},
		{"dan", "identity", "# Dan", "---\ntype: identity\n---\n# Dan\n"},
	} {
		rec := u.post(t, "bob", "/create", url.Values{"path": {tc.path}, "type": {tc.typ}, "body": {tc.body}}, nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/p/"+tc.path+".md" {
			t.Fatalf("create %s = %d %s\n%s", tc.path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
		if got := u.read(t, tc.path+".md"); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.path, got, tc.want)
		}
	}
	rec := u.post(t, "bob", "/create", url.Values{"path": {"dan"}, "type": {"document"}, "body": {"x"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("duplicate = %d\n%s", rec.Code, rec.Body.String())
	}
	if rec := u.post(t, "bob", "/create", url.Values{"path": {"../out"}, "type": {"document"}}, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("escaping path = %d", rec.Code)
	}
	if rec := u.post(t, "", "/create", url.Values{"path": {"x"}, "type": {"document"}}, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create = %d", rec.Code)
	}
}

func TestRenameAndDeleteForms(t *testing.T) {
	u := uiWorkspace(t)
	// Only a writer sees the forms.
	if body := u.get(t, "carol", "/p/plan.md").Body.String(); strings.Contains(body, "jk-rename") {
		t.Fatal("commenter offered the rename form")
	}
	if body := u.get(t, "bob", "/p/plan.md").Body.String(); !strings.Contains(body, "jk-rename") || !strings.Contains(body, "jk-delete") {
		t.Fatal("writer not offered the forms")
	}
	if rec := u.post(t, "carol", "/rename/plan.md", url.Values{"to": {"plans/v1"}}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("commenter rename = %d", rec.Code)
	}
	rec := u.post(t, "bob", "/rename/plan.md", url.Values{"to": {"plans/v1"}}, htmx)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") != "/p/plans/v1.md" {
		t.Fatalf("rename = %d %q\n%s", rec.Code, rec.Header().Get("HX-Push-Url"), rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Renamed plan.md to plans/v1.md.", "Rewritten to keep pointing", `<a href="/p/open.md">open.md</a>`, "<code>plan</code>", "<code>v1</code>"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in rename report:\n%s", want, body)
		}
	}
	if !strings.Contains(u.read(t, "open.md"), "[[v1]]") {
		t.Fatal("reference not rewritten")
	}
	if rec := u.post(t, "bob", "/rename/plans/v1.md", url.Values{"to": {"open"}}, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("rename onto an existing page = %d", rec.Code)
	}
	// Deletion is refused while comments are open, and reports broken links.
	if _, err := u.s.cache.load(); err != nil {
		t.Fatal(err)
	}
	u.post(t, "carol", "/comment/plans/v1.md", url.Values{"op": {"add"}, "anchor": {"Ship"}, "text": {"Why?"}}, nil)
	rec = u.post(t, "bob", "/delete/plans/v1.md", nil, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "unresolved comment") {
		t.Fatalf("delete with comments = %d\n%s", rec.Code, rec.Body.String())
	}
	u.post(t, "carol", "/comment/plans/v1.md", url.Values{"op": {"resolve"}, "id": {"c1"}}, nil)
	rec = u.post(t, "bob", "/delete/plans/v1.md", nil, htmx)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") != "/" {
		t.Fatalf("delete = %d\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Deleted plans/v1.md.") || !strings.Contains(rec.Body.String(), "No longer resolve") || !strings.Contains(rec.Body.String(), "open.md") {
		t.Fatalf("delete report:\n%s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(u.dir, "plans", "v1.md")); !os.IsNotExist(err) {
		t.Fatal("page not deleted")
	}
	if rec := u.post(t, "", "/delete/open.md", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous delete = %d", rec.Code)
	}
}

func TestForYouProblemsSearchAndCounts(t *testing.T) {
	u := uiWorkspace(t)
	if err := os.WriteFile(filepath.Join(u.dir, "t1.md"), []byte("---\ntype: task\nstatus: doing\nassignee: bob\n---\n# First task\n\nAsk @carol. [[nowhere]]\n\n<!--comment:c1-->Ask<!--/comment:c1-->\n\n<!--comment-thread:c1\n@carol: ok\n-->\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if rec := u.get(t, "", "/me"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /me = %d", rec.Code)
	}
	body := u.get(t, "bob", "/me").Body.String()
	if !strings.Contains(body, "Tasks assigned to you") || !strings.Contains(body, `<a href="/p/t1.md">First task</a>`) || !strings.Contains(body, "1 open") {
		t.Fatalf("bob's for-you page:\n%s", body)
	}
	if body := u.get(t, "carol", "/me").Body.String(); !strings.Contains(body, "Pages that mention you") || !strings.Contains(body, "First task") || strings.Contains(body, "Tasks assigned to you</h3><div") {
		t.Fatalf("carol's for-you page:\n%s", body)
	}
	// Problems are filtered to readable pages and the hidden ones counted.
	if err := os.WriteFile(filepath.Join(u.dir, "secret.md"), []byte("---\npermissions:\n  admin: alice\n---\n# Secret\n\n[[gone]]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body = u.get(t, "", "/problems").Body.String()
	if !strings.Contains(body, `unresolved &#34;nowhere&#34;`) || strings.Contains(body, "gone") {
		t.Fatalf("problems page:\n%s", body)
	}
	if !strings.Contains(body, "1 report(s) concern pages you may not read") {
		t.Fatalf("hidden count missing:\n%s", body)
	}
	if body := u.get(t, "alice", "/problems").Body.String(); !strings.Contains(body, `unresolved &#34;gone&#34;`) || strings.Contains(body, "you may not read") {
		t.Fatalf("admin's problems page:\n%s", body)
	}
	// Search by title or path; the list and its stream carry the query.
	body = u.get(t, "", "/?q=FIRST").Body.String()
	if !strings.Contains(body, "First task") || strings.Contains(body, "Second task") || !strings.Contains(body, `hx-sse:connect="/events?q=FIRST"`) {
		t.Fatalf("search:\n%s", body)
	}
	if body := u.get(t, "", "/pages?q=t2&type=task").Body.String(); !strings.Contains(body, "Second task") || strings.Contains(body, "First task") {
		t.Fatalf("search by path:\n%s", body)
	}
	if body := u.get(t, "", "/").Body.String(); !strings.Contains(body, `jk-comment-count" data-variant="outline" title="unresolved comments">1 open</span>`) {
		t.Fatalf("comment count missing:\n%s", body)
	}
	for name, target := range map[string]string{"new": "/new", "me": "/me", "problems": "/problems", "search": "/?q=task"} {
		lower := strings.ToLower(u.get(t, "bob", target).Body.String())
		for _, banned := range []string{"<style", " style=", "javascript:", "hx-on", "hx-live"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
		if regexp.MustCompile(`\son[a-z]+=`).MatchString(lower) {
			t.Errorf("%s has an inline event handler", name)
		}
	}
}
