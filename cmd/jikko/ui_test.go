package main

import (
	"bytes"
	"encoding/base64"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
