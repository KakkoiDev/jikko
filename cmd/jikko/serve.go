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
// every request.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionEntry
}

type sessionEntry struct {
	identity string
	expires  time.Time
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]sessionEntry{}} }

func (s *sessionStore) create(identity string) (string, error) {
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
	s.m[id] = sessionEntry{identity: identity, expires: now.Add(sessionTTL)}
	return id, nil
}

func (s *sessionStore) identity(id string) string {
	if id == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || time.Now().After(e.expires) {
		delete(s.m, id)
		return ""
	}
	e.expires = time.Now().Add(sessionTTL)
	s.m[id] = e
	return e.identity
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
}

type pageData struct {
	Pages []*jikko.Page
	Query string
	Actor string
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
	return &server{
		cache:      &cache{dir: root},
		sessions:   newSessionStore(),
		tmpl:       template.Must(template.New("jikko").Parse(pageHTML + pagesHTML)),
		trustProxy: trustProxy,
		streams:    make(chan struct{}, maxStreams),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/pages", s.pages)
	mux.HandleFunc("/events", s.events)
	mux.HandleFunc("/", s.index)
	return mux
}

// fail answers a request that could not be served. The detail goes to the log,
// never to the client, and the process stays up: one unreadable file must not
// take the workspace offline.
func (s *server) fail(w http.ResponseWriter, err error) {
	log.Printf("request failed: %v", err)
	http.Error(w, "the workspace could not be read; see the server log", http.StatusServiceUnavailable)
}

// actor identifies the caller from a bearer token or a browser session, and
// re-checks that the identity still exists and is still an individual.
func (s *server) actor(r *http.Request, ws *jikko.Workspace) string {
	identity := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		p, err := ws.Authenticate(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
		if err != nil {
			return ""
		}
		identity = strings.TrimSuffix(p.Path, ".md")
	} else if c, err := r.Cookie(sessionCookie); err == nil {
		identity = s.sessions.identity(c.Value)
	}
	if identity == "" {
		return ""
	}
	if p, ok := ws.ResolveIdentity(identity); !ok || len(p.Members) != 0 {
		return ""
	}
	return identity
}

func (s *server) data(r *http.Request) (pageData, error) {
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
	actor := s.actor(r, ws)
	return pageData{Pages: visiblePages(ws, actor, ws.List(kind, q.Get("status"))), Query: query, Actor: actor}, nil
}

func (s *server) render(name string, r *http.Request) (string, error) {
	data, err := s.data(r)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	out, err := s.render("page", r)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, out)
}

func (s *server) pages(w http.ResponseWriter, r *http.Request) {
	out, err := s.render("pages", r)
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
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return
	}
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	p, err := ws.Authenticate(r.FormValue("token"))
	if err != nil {
		log.Printf("login rejected: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}
	id, err := s.sessions.create(strings.TrimSuffix(p.Path, ".md"))
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
	if !sameOrigin(r) {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
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

	previous, err := s.render("pages", r)
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
		current, err := s.render("pages", r)
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

const pageHTML = `{{define "page"}}<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Jikko</title><link rel="stylesheet" href="/assets/basecoat.min.css"><script src="/assets/htmx.min.js"></script></head><body class="bg-background text-foreground"><main class="mx-auto max-w-4xl p-6 md:p-10"><header class="mb-8 flex items-center justify-between"><div><h1 class="text-3xl font-semibold tracking-tight">Jikko</h1><p class="text-muted-foreground">Structured work in plain Markdown.</p></div>{{if .Actor}}<form method="post" action="/logout"><span>{{.Actor}}</span> <button class="btn" data-variant="outline">Log out</button></form>{{else}}<form method="post" action="/login" class="flex gap-2"><input name="token" type="password" placeholder="Token" required><button class="btn">Log in</button></form>{{end}}</header><nav class="mb-6 flex gap-2"><button class="btn" data-variant="outline" hx-get="/pages" hx-target="#pages" hx-swap="outerHTML">All</button><button class="btn" data-variant="outline" hx-get="/pages?type=task" hx-target="#pages" hx-swap="outerHTML">Tasks</button><button class="btn" data-variant="outline" hx-get="/pages?type=view" hx-target="#pages" hx-swap="outerHTML">Views</button></nav>{{template "pages" .}}</main></body></html>{{end}}`
const pagesHTML = `{{define "pages"}}<section id="pages" hx-sse:connect="/events{{.Query}}" hx-swap="outerHTML"><div class="item-group">{{range .Pages}}<article class="item" data-variant="outline"><section><h3>{{.Title}}</h3><p class="text-muted-foreground">{{.Kind}} · {{.Path}}</p></section></article>{{else}}<article class="item" data-variant="outline"><section><p>No pages.</p></section></article>{{end}}</div></section>{{end}}`
