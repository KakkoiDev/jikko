package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	jikko "github.com/KakkoiDev/jikko"
)

const (
	// preSessionCookie carries a random value for a caller without a
	// session, so the login form has an anti-forgery token too.
	preSessionCookie = "jikko_pre"
	// maxFormBody bounds an edit or comment form: a page source, its base
	// copy, and a few fields.
	maxFormBody = 8 << 20
)

var templateFuncs = template.FuncMap{
	"pageURL":    func(p string) string { return jikko.DefaultLinks.Page(p) },
	"editURL":    func(p string) string { return "/edit/" + escapedPath(p) },
	"saveURL":    func(p string) string { return "/save/" + escapedPath(p) },
	"commentURL": func(p string) string { return "/comment/" + escapedPath(p) },
	"short": func(rev string) string {
		if len(rev) > 12 {
			return rev[:12]
		}
		return rev
	},
	"prop": func(it jikko.ViewItem, key string) string {
		switch v := it.Metadata[key].(type) {
		case nil:
			return ""
		case []any:
			parts := make([]string, 0, len(v))
			for _, x := range v {
				parts = append(parts, fmt.Sprint(x))
			}
			return strings.Join(parts, ", ")
		default:
			return fmt.Sprint(v)
		}
	},
}

// escapedPath is a page path for a URL: the same escaping as page links.
func escapedPath(p string) string {
	return strings.TrimPrefix(jikko.DefaultLinks.Page(p), "/p/")
}

// ---------------------------------------------------------------------------
// Anti-forgery
//
// Every state-changing form carries a token. For a caller with a session it
// is an HMAC of the session id; for a caller without one, of a random
// pre-session cookie. Both cookies are HttpOnly and SameSite=Strict, so a
// cross-site page can neither read the value nor make the browser send it,
// and the server key never leaves the process. Requests authenticated with
// an Authorization header carry no ambient credential and need no token.

func (s *server) sign(kind, value string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte(kind + "\x00" + value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// csrfFor returns the token for the forms of a page about to be rendered,
// setting a pre-session cookie first if the caller has neither cookie.
func (s *server) csrfFor(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, ok := s.sessions.lookup(c.Value); ok {
			return s.sign("session", c.Value)
		}
	}
	if c, err := r.Cookie(preSessionCookie); err == nil && len(c.Value) >= 32 {
		return s.sign("pre", c.Value)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: preSessionCookie, Value: value, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure(r),
	})
	return s.sign("pre", value)
}

// validCSRF checks the token a form posted against the caller's cookies.
func (s *server) validCSRF(r *http.Request) bool {
	got := r.PostFormValue("_csrf")
	if got == "" {
		got = r.Header.Get("X-CSRF-Token")
	}
	if got == "" {
		return false
	}
	var want []string
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, ok := s.sessions.lookup(c.Value); ok {
			want = append(want, s.sign("session", c.Value))
		}
	}
	if c, err := r.Cookie(preSessionCookie); err == nil && len(c.Value) >= 32 {
		want = append(want, s.sign("pre", c.Value))
	}
	ok := false
	for _, w := range want {
		if subtle.ConstantTimeCompare([]byte(got), []byte(w)) == 1 {
			ok = true
		}
	}
	return ok
}

// postAllowed rejects a state-changing request that is cross-site or lacks
// a valid anti-forgery token. It answers the request itself when it refuses.
func (s *server) postAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return false
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	if !s.validCSRF(r) {
		http.Error(w, "missing or invalid anti-forgery token; reload the page and try again", http.StatusForbidden)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Views

type detailView struct {
	Path, Title, Kind, Rev string
	HTML                   template.HTML
	Fields                 []jikko.Field
	Notices                []notice
	Comments               []commentView
	Backlinks              []*jikko.Page
	View                   *jikko.ViewResult
	CanWrite, CanComment   bool
}

type notice struct{ Kind, Message string }

type commentView struct {
	ID, Anchor string
	Entries    []entryView
}

type entryView struct {
	Author string
	HTML   template.HTML
}

type editView struct {
	Path, Title, Mode string
	Rev               string
	// Base is the source the edit started from, base64-encoded so the form
	// returns it byte for byte, for a three-way merge without Git history.
	Base     string
	Source   string
	Body     string
	Fields   []jikko.Field
	IsView   bool
	Error    string
	Conflict *conflictView
}

type conflictView struct {
	BaseKnown bool
	Against   string
	Yours     string
	Fields    []sideBySide
	Body      []sideBySide
}

type sideBySide struct {
	ID, Key              string
	Line                 int
	Base, Current, Yours string
}

func (s *server) detail(ws *jikko.Workspace, actor string, p *jikko.Page) *detailView {
	d := &detailView{
		Path: p.Path, Title: p.Title, Kind: string(p.Kind), Rev: p.Rev,
		CanWrite:   actor != "" && ws.Allowed(actor, p, jikko.Write),
		CanComment: actor != "" && p.Kind != jikko.View && ws.Allowed(actor, p, jikko.Comment),
	}
	r := ws.Renderer(actor, jikko.DefaultLinks)
	if src, _, err := ws.Source(actor, p.Path); err == nil {
		d.Fields = jikko.SourceFields(src)
	}
	if p.Kind == jikko.View {
		res, err := ws.EvaluateView(actor, p.Path)
		if err != nil {
			d.Notices = append(d.Notices, notice{"problem", err.Error()})
		} else {
			d.View = res
		}
	} else {
		d.HTML = r.Page(p)
	}
	for _, c := range p.Comments {
		cv := commentView{ID: c.ID, Anchor: c.Anchor}
		for _, e := range c.Entries {
			author := e.Author
			if author == "" {
				author = "?"
			}
			cv.Entries = append(cv.Entries, entryView{Author: author, HTML: r.Inline(e.Text)})
		}
		d.Comments = append(d.Comments, cv)
	}
	for _, b := range p.Backlinks {
		if q, ok := ws.Pages[b]; ok && ws.Allowed(actor, q, jikko.Read) {
			d.Backlinks = append(d.Backlinks, q)
		}
	}
	for _, problem := range ws.Problems {
		if problem.Path == p.Path {
			d.Notices = append(d.Notices, notice{"problem", problem.Message})
		}
	}
	for _, warning := range ws.Warnings() {
		if warning.Path == p.Path {
			d.Notices = append(d.Notices, notice{"warning", warning.Message})
		}
	}
	return d
}

// editFor builds the edit form showing shown, an edit of base at rev.
func editFor(p *jikko.Page, mode string, shown, base []byte, rev string) *editView {
	if mode != "source" {
		mode = "fields"
	}
	return &editView{
		Path: p.Path, Title: p.Title, Mode: mode, Rev: rev,
		Base:   base64.StdEncoding.EncodeToString(base),
		Source: string(shown), Body: jikko.SourceBody(shown),
		Fields: jikko.SourceFields(shown), IsView: p.Kind == jikko.View,
	}
}

func conflictFor(c jikko.Conflict, yours []byte) *conflictView {
	text := func(v *string) string {
		if v == nil {
			return "(absent)"
		}
		return *v
	}
	cv := &conflictView{BaseKnown: c.BaseKnown, Against: c.CurrentRev, Yours: base64.StdEncoding.EncodeToString(yours)}
	for _, f := range c.Fields {
		cv.Fields = append(cv.Fields, sideBySide{ID: f.ID, Key: f.Key, Base: text(f.Base), Current: text(f.Current), Yours: text(f.Yours)})
	}
	for _, b := range c.Body {
		cv.Body = append(cv.Body, sideBySide{ID: b.ID, Line: b.Line, Base: b.Base, Current: b.Current, Yours: b.Yours})
	}
	return cv
}

// ---------------------------------------------------------------------------
// Responses

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// respond renders data with the given status: the whole page for an
// ordinary request, or only the #page section for an HTMX swap.
func (s *server) respond(w http.ResponseWriter, r *http.Request, status int, data pageData) {
	name := "page"
	if isHTMX(r) {
		switch {
		case data.Detail != nil:
			name = "detail"
		case data.Edit != nil:
			name = "edit"
		default:
			name = "message"
		}
	}
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

// refuse answers with a message page.
func (s *server) refuse(w http.ResponseWriter, r *http.Request, status int, actor, message string) {
	s.respond(w, r, status, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Message: message})
}

const (
	msgNotFound = "That page does not exist, or you may not read it."
	msgLogin    = "Log in to change this page."
)

// ---------------------------------------------------------------------------
// Handlers

func (s *server) pageDetail(w http.ResponseWriter, r *http.Request) {
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.actor(r, ws, true)
	pages, err := ws.ReadMany(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Detail: s.detail(ws, actor, pages[0])})
}

func (s *server) editPage(w http.ResponseWriter, r *http.Request) {
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.actor(r, ws, true)
	src, p, err := ws.Source(actor, r.PathValue("path"))
	switch {
	case err != nil:
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
	case actor == "":
		s.refuse(w, r, http.StatusUnauthorized, actor, msgLogin)
	case !ws.Allowed(actor, p, jikko.Write):
		s.refuse(w, r, http.StatusForbidden, actor, "You may read this page but not edit it.")
	default:
		s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Edit: editFor(p, r.URL.Query().Get("mode"), src, src, p.Rev)})
	}
}

// mutationContext opens a fresh workspace for a mutation and checks the
// request. The cached workspace is shared by concurrent readers and is never
// mutated; the cache notices the change on disk and reloads.
func (s *server) mutationContext(w http.ResponseWriter, r *http.Request) (*jikko.Workspace, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if !s.postAllowed(w, r) {
		return nil, "", false
	}
	ws, err := jikko.Open(s.cache.dir)
	if err != nil {
		s.fail(w, err)
		return nil, "", false
	}
	actor := s.actor(r, ws, true)
	if actor == "" {
		s.refuse(w, r, http.StatusUnauthorized, "", msgLogin)
		return nil, "", false
	}
	return ws, actor, true
}

// done answers a successful mutation: the refreshed page section for an
// HTMX swap, or a redirect to the page for an ordinary form post.
func (s *server) done(w http.ResponseWriter, r *http.Request, ws *jikko.Workspace, actor, pagePath, flash string) {
	if !isHTMX(r) {
		http.Redirect(w, r, jikko.DefaultLinks.Page(pagePath), http.StatusSeeOther)
		return
	}
	pages, err := ws.ReadMany(actor, pagePath)
	if err != nil {
		s.refuse(w, r, http.StatusOK, actor, flash)
		return
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Flash: flash, Detail: s.detail(ws, actor, pages[0])})
}

func (s *server) savePage(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	current, p, err := ws.Source(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	if !ws.Allowed(actor, p, jikko.Write) {
		s.refuse(w, r, http.StatusForbidden, actor, "You may read this page but not edit it.")
		return
	}
	rev := r.PostFormValue("rev")
	base, err := base64.StdEncoding.DecodeString(r.PostFormValue("base"))
	if err != nil || rev == "" {
		s.refuse(w, r, http.StatusBadRequest, actor, "The edit form was incomplete; reload the page and try again.")
		return
	}
	mode := r.PostFormValue("mode")
	opt := jikko.SaveOptions{Base: base}
	var yours []byte
	shownMode := mode
	switch mode {
	case "source":
		yours = []byte(r.PostFormValue("source"))
	case "resolve":
		shownMode = "source"
		yours, err = base64.StdEncoding.DecodeString(r.PostFormValue("yours"))
		if err != nil {
			s.refuse(w, r, http.StatusBadRequest, actor, "The conflict form was incomplete; reload the page and try again.")
			return
		}
		opt.ResolveRev = r.PostFormValue("against")
		opt.Resolve = map[string]string{}
		for key, values := range r.PostForm {
			if id, found := strings.CutPrefix(key, "resolve:"); found && len(values) > 0 {
				opt.Resolve[id] = values[0]
			}
		}
	default:
		shownMode = "fields"
		yours, err = composeFields(base, r, p.Kind == jikko.View)
		if err != nil {
			edit := editFor(p, shownMode, base, base, rev)
			edit.Error = err.Error()
			s.respond(w, r, http.StatusUnprocessableEntity, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Edit: edit})
			return
		}
	}
	res, err := ws.SaveSource(actor, p.Path, rev, yours, opt)
	var conflict *jikko.ConflictError
	switch {
	case errors.As(err, &conflict):
		edit := editFor(p, shownMode, yours, base, rev)
		edit.Conflict = conflictFor(conflict.Conflict, yours)
		s.respond(w, r, http.StatusConflict, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Edit: edit})
	case err != nil:
		edit := editFor(p, shownMode, yours, base, rev)
		edit.Error = err.Error()
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "lacks ") {
			status = http.StatusForbidden
		}
		s.respond(w, r, status, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Edit: edit})
	default:
		flash := "Saved."
		if res.Merged {
			flash = "Saved. The page had changed since you opened it; both edits were merged."
		} else if res.Rev == fingerprintOf(current) {
			flash = "Nothing to save: the page already reads that way."
		}
		s.done(w, r, ws, actor, res.Path, flash)
	}
}

func fingerprintOf(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// composeFields builds the edited source from the fields form: each
// editable property whose value changed is set, removed when its box is
// ticked or its input is gone, a new property is added, and the body is
// replaced.
func composeFields(base []byte, r *http.Request, isView bool) ([]byte, error) {
	edit := jikko.SourceEdit{Set: map[string]string{}}
	for _, f := range jikko.SourceFields(base) {
		if r.PostFormValue("rm:"+f.Key) != "" {
			edit.Remove = append(edit.Remove, f.Key)
			continue
		}
		if !f.Editable {
			continue
		}
		values, present := r.PostForm["f:"+f.Key]
		switch {
		case !present:
			edit.Remove = append(edit.Remove, f.Key)
		case values[0] != f.Value:
			edit.Set[f.Key] = values[0]
		}
	}
	// Properties the form shows that the base lacks: added by an edit that
	// conflicted and is being saved again.
	for key, values := range r.PostForm {
		if k, found := strings.CutPrefix(key, "f:"); found && len(values) > 0 {
			if _, known := edit.Set[k]; !known && !hasField(base, k) {
				edit.Set[k] = values[0]
			}
		}
	}
	if k := strings.TrimSpace(r.PostFormValue("new_key")); k != "" {
		edit.Set[k] = r.PostFormValue("new_value")
	}
	if !isView {
		body := strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")
		edit.Body = &body
	}
	sort.Strings(edit.Remove)
	return jikko.EditSource(base, edit)
}

func hasField(src []byte, key string) bool {
	for _, f := range jikko.SourceFields(src) {
		if f.Key == key {
			return true
		}
	}
	return false
}

func (s *server) commentPage(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	pages, err := ws.ReadMany(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	p := pages[0]
	if !ws.Allowed(actor, p, jikko.Comment) {
		s.refuse(w, r, http.StatusForbidden, actor, "You may read this page but not comment on it.")
		return
	}
	var flash string
	switch r.PostFormValue("op") {
	case "add":
		var id string
		id, err = ws.AddComment(actor, p.Path, strings.TrimSpace(r.PostFormValue("anchor")), r.PostFormValue("text"))
		flash = "Comment " + id + " added."
	case "reply":
		id := r.PostFormValue("id")
		err = ws.ReplyComment(actor, p.Path, id, r.PostFormValue("text"))
		flash = "Replied to " + id + "."
	case "resolve":
		id := r.PostFormValue("id")
		err = ws.ResolveComment(actor, p.Path, id)
		flash = "Resolved " + id + ". Git history keeps the discussion."
	default:
		s.refuse(w, r, http.StatusBadRequest, actor, "Unknown comment operation.")
		return
	}
	if err != nil {
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "lacks ") {
			status = http.StatusForbidden
		}
		// The page may have changed; show it as it is now with the error.
		if fresh, err2 := jikko.Open(s.cache.dir); err2 == nil {
			ws = fresh
		}
		if again, err2 := ws.ReadMany(actor, p.Path); err2 == nil {
			p = again[0]
		}
		s.respond(w, r, status, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Error: err.Error(), Detail: s.detail(ws, actor, p)})
		return
	}
	s.done(w, r, ws, actor, p.Path, flash)
}

// inlineTypes are served for display; anything else is a download.
var inlineTypes = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true,
	".mp4": true, ".webm": true, ".ogv": true, ".mov": true,
	".mp3": true, ".ogg": true, ".oga": true, ".wav": true, ".m4a": true, ".flac": true,
	".pdf": true,
}

// serveFile serves a workspace file to a reader of a page that uses it. The
// response is sandboxed so an SVG or HTML file opened directly cannot run
// script in the application's origin.
func (s *server) serveFile(w http.ResponseWriter, r *http.Request) {
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.actor(r, ws, false)
	file, ok := ws.ReadableFile(actor, r.PathValue("path"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(path.Ext(file))
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-cache")
	ctype := mime.TypeByExtension(ext)
	if ctype == "" || !inlineTypes[ext] {
		ctype = "application/octet-stream"
		h.Set("Content-Disposition", "attachment")
	}
	h.Set("Content-Type", ctype)
	http.ServeContent(w, r, "", info.ModTime(), f)
}
