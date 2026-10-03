package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	jikko "github.com/KakkoiDev/jikko"
	jikkoweb "github.com/KakkoiDev/jikko/web"
)

const (
	sessionCookie = "jikko_session"
	sessionTTL    = 12 * time.Hour
	// streamMaxAge bounds one live connection so a stream cannot be held open
	// forever. The browser reconnects on its own.
	streamMaxAge = 30 * time.Minute
	// maxStreams caps concurrent event streams, each of which costs a
	// goroutine and a ticker.
	maxStreams = 64
	// maxLoginBody bounds the login form an unauthenticated caller may send.
	maxLoginBody = 4 << 10
)

// cache holds the parsed workspace and re-reads it only when the Markdown tree
// changes, so a live connection costs a directory stat rather than a full
// parse every tick.
type cache struct {
	dir   string
	mu    sync.Mutex
	stamp string
	ws    *jikko.Workspace
	err   error
	ready bool
}

func (c *cache) load() (*jikko.Workspace, error) {
	stamp, err := treeStamp(c.dir)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready && stamp == c.stamp {
		return c.ws, c.err
	}
	ws, err := jikko.Open(c.dir)
	if err == nil && len(ws.Problems) > 0 {
		log.Printf("%d workspace problem(s); run `jikko check` for detail", len(ws.Problems))
	}
	c.stamp, c.ws, c.err, c.ready = stamp, ws, err, true
	return ws, err
}

func treeStamp(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".data" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sessionStore exchanges a permanent bearer token for a short-lived browser
// session, so the token itself is never stored in a cookie or replayed on
// every request. Each session remembers the credential it was created from,
// so revoking that credential ends the session too.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionEntry
}

type sessionEntry struct {
	identity   string
	credential string
	expires    time.Time
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]sessionEntry{}} }

func (s *sessionStore) create(identity, credential string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	s.m[id] = sessionEntry{identity: identity, credential: credential, expires: now.Add(sessionTTL)}
	return id, nil
}

// lookup returns a live session without extending it.
func (s *sessionStore) lookup(id string) (sessionEntry, bool) {
	if id == "" {
		return sessionEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || time.Now().After(e.expires) {
		delete(s.m, id)
		return sessionEntry{}, false
	}
	return e, true
}

// touch slides a live session's expiry forward.
func (s *sessionStore) touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[id]; ok && !time.Now().After(e.expires) {
		e.expires = time.Now().Add(sessionTTL)
		s.m[id] = e
	}
}

func (s *sessionStore) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

type server struct {
	cache      *cache
	sessions   *sessionStore
	tmpl       *template.Template
	trustProxy bool
	streams    chan struct{}
	// csrfKey signs the anti-forgery tokens of this process's forms.
	csrfKey []byte
}

// pageData is what every template renders. Only the fields of the page being
// shown are set.
type pageData struct {
	Pages []*jikko.Page
	Query string
	Actor string
	// CSRF is the anti-forgery token every form posts back.
	CSRF    string
	Flash   string
	Error   string
	Message string
	Detail  *detailView
	Edit    *editView
}

func serve(args []string) error {
	var addr *string
	var trustProxy *bool
	args, dir, _, err := flags("serve", args, func(f *flag.FlagSet) {
		addr = f.String("addr", "127.0.0.1:8080", "listen address")
		trustProxy = f.Bool("behind-proxy", false, "trust X-Forwarded-Proto from a reverse proxy when setting cookie Secure")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko serve [flags]")
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}

	s := newServer(root, *trustProxy)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: it would sever live event streams. streamMaxAge
		// bounds their lifetime instead.
	}
	log.Printf("Jikko: http://%s", *addr)
	return srv.ListenAndServe()
}

func newServer(root string, trustProxy bool) *server {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &server{
		cache:      &cache{dir: root},
		sessions:   newSessionStore(),
		tmpl:       template.Must(template.New("jikko").Funcs(templateFuncs).ParseFS(jikkoweb.Templates, "templates/*.html")),
		trustProxy: trustProxy,
		streams:    make(chan struct{}, maxStreams),
		csrfKey:    key,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/pages", s.pages)
	mux.HandleFunc("/events", s.events)
	mux.HandleFunc("GET /p/{path...}", s.pageDetail)
	mux.HandleFunc("GET /edit/{path...}", s.editPage)
	mux.HandleFunc("POST /save/{path...}", s.savePage)
	mux.HandleFunc("POST /comment/{path...}", s.commentPage)
	mux.HandleFunc("GET /files/{path...}", s.serveFile)
	mux.HandleFunc("/", s.index)
	return securityHeaders(mux)
}

// contentSecurityPolicy allows only the server's own scripts, styles, and
// connections. The page has no inline script, style, or event handler, and
// uses no hx-on expressions, so HTMX needs neither 'unsafe-inline' nor
// 'unsafe-eval'; its indicator styles are a constructed stylesheet, which
// style-src does not govern. data: images cover the icons inlined in the
// Basecoat stylesheet.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets the headers every response carries: a strict content
// security policy, no MIME sniffing, no framing, and no cross-origin referrer.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// fail answers a request that could not be served. The detail goes to the log,
// never to the client, and the process stays up: one unreadable file must not
// take the workspace offline.
func (s *server) fail(w http.ResponseWriter, err error) {
	log.Printf("request failed: %v", err)
	http.Error(w, "the workspace could not be read; see the server log", http.StatusServiceUnavailable)
}

// actor identifies the caller from a bearer token or a browser session.
//
// A session is only as good as the credential it was created from: on every
// request the credential must still be stored and still map to the same
// individual Identity. A session whose credential was revoked, or whose
// Identity was deleted, renamed, or turned into a group, is dropped for good,
// so it cannot come back if a same-named Identity reappears.
//
// extend slides the session's expiry. Only requests a person makes extend it;
// a live event stream re-checks the session without keeping it alive.
func (s *server) actor(r *http.Request, ws *jikko.Workspace, extend bool) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		p, err := ws.Authenticate(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(p.Path, ".md")
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	e, ok := s.sessions.lookup(c.Value)
	if !ok {
		return ""
	}
	p, err := ws.CredentialIdentity(e.credential)
	if err != nil || strings.TrimSuffix(p.Path, ".md") != e.identity {
		s.sessions.drop(c.Value)
		return ""
	}
	if extend {
		s.sessions.touch(c.Value)
	}
	return e.identity
}

func (s *server) data(r *http.Request, extend bool) (pageData, error) {
	ws, err := s.cache.load()
	if err != nil {
		return pageData{}, err
	}
	q := r.URL.Query()
	kind, err := parseKindFlag(q.Get("type"))
	if err != nil {
		kind = ""
	}
	filters := url.Values{}
	if v := string(kind); v != "" {
		filters.Set("type", v)
	}
	if v := q.Get("status"); v != "" {
		filters.Set("status", v)
	}
	query := ""
	if encoded := filters.Encode(); encoded != "" {
		query = "?" + encoded
	}
	actor := s.actor(r, ws, extend)
	return pageData{Pages: visiblePages(ws, actor, ws.List(kind, q.Get("status"))), Query: query, Actor: actor}, nil
}

// render executes a template for the caller and also reports who the caller
// was, so a live stream can notice when that changes.
func (s *server) render(name string, r *http.Request, extend bool) (string, string, error) {
	return s.renderCSRF(name, r, extend, "")
}

func (s *server) renderCSRF(name string, r *http.Request, extend bool, csrf string) (string, string, error) {
	data, err := s.data(r, extend)
	if err != nil {
		return "", "", err
	}
	data.CSRF = csrf
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", "", err
	}
	return b.String(), data.Actor, nil
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	out, _, err := s.renderCSRF("page", r, true, s.csrfFor(w, r))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, out)
}

func (s *server) pages(w http.ResponseWriter, r *http.Request) {
	out, _, err := s.render("pages", r, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, out)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A token is a few dozen bytes; there is no reason to buffer megabytes of
	// form body from an unauthenticated caller.
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if !s.postAllowed(w, r) {
		return
	}
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	p, credential, err := ws.AuthenticateCredential(r.FormValue("token"))
	if err != nil {
		log.Printf("login rejected: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	id, err := s.sessions.create(strings.TrimSuffix(p.Path, ".md"), credential)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, s.cookie(r, id, int(sessionTTL.Seconds())))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if !s.postAllowed(w, r) {
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.drop(c.Value)
	}
	http.SetCookie(w, s.cookie(r, "", -1))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secure(r),
	}
}

func (s *server) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sameOrigin rejects cross-site form posts. Fetch metadata decides where the
// browser sends it; otherwise an Origin header must match the host. A request
// carrying neither is not a browser form post.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		http.Error(w, "too many live connections", http.StatusServiceUnavailable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// A stream never extends the session it runs under, and it closes as soon
	// as the caller it started for is no longer who the request authenticates
	// as: a revoked credential, a removed Identity, or an expired session. The
	// browser then reconnects with whatever access it still has.
	previous, actor, err := s.render("pages", r, false)
	if err != nil {
		return
	}
	expiry := time.After(streamMaxAge)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expiry:
			return
		case <-ticker.C:
		}
		current, now, err := s.render("pages", r, false)
		if err == nil && now != actor {
			return
		}
		if err != nil || current == previous {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", sseData(current)); err != nil {
			return
		}
		flusher.Flush()
		previous = current
	}
}

// sseData collapses a rendered fragment onto one data line. Every character
// the event-stream grammar treats as a line break has to go, the bare carriage
// return included, or page content could terminate the line early and forge
// event fields.
func sseData(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}

func assetHandler() http.Handler {
	files := http.FileServer(http.FS(jikkoweb.Assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			// The asset directory is not a listing.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	})
}
