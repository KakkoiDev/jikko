package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	// refreshInterval is how often a live list is rendered again when
	// nothing changed, so "3m ago" does not go stale.
	refreshInterval = 30 * time.Second
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
	// checked is when the tree was last stamped. Live streams ask every
	// second each; one stamp per stampInterval serves them all.
	checked time.Time

	// autoCommit records every mutation as an attributed Git commit.
	autoCommit bool
}

// stampInterval is how long a tree stamp is trusted before the tree is
// walked again.
const stampInterval = 500 * time.Millisecond

// load returns the current workspace, stamping the tree first so a request
// always sees the files as they are.
func (c *cache) load() (*jikko.Workspace, error) {
	ws, _, err := c.refresh(0)
	return ws, err
}

// loadStamped is load for a live stream: it returns the stamp too, so the
// stream can tell whether anything changed, and trusts a stamp taken within
// stampInterval, so many streams cost one walk between them.
func (c *cache) loadStamped() (*jikko.Workspace, string, error) {
	return c.refresh(stampInterval)
}

func (c *cache) refresh(trust time.Duration) (*jikko.Workspace, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready && trust > 0 && time.Since(c.checked) < trust {
		return c.ws, c.stamp, c.err
	}
	stamp, err := treeStamp(c.dir)
	if err != nil {
		return nil, "", err
	}
	c.checked = time.Now()
	if c.ready && stamp == c.stamp {
		return c.ws, c.stamp, c.err
	}
	ws, err := jikko.Open(c.dir)
	if err == nil {
		ws.AutoCommit = c.autoCommit
		if len(ws.Problems) > 0 {
			log.Printf("%d workspace problem(s); run `jikko check` for detail", len(ws.Problems))
		}
	}
	c.stamp, c.ws, c.err, c.ready = stamp, ws, err, true
	return ws, stamp, err
}

// treeStamp fingerprints the names, sizes and modification times of the
// workspace's files -- pages and assets alike, since an asset dropped into
// the tree changes what embeds resolve to -- skipping what the workspace
// ignores.
func treeStamp(dir string) (string, error) {
	h := sha256.New()
	err := jikko.WalkFiles(dir, func(p, rel string, d fs.DirEntry) error {
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// liveStamp fingerprints everything the home page shows besides the tree:
// presence records and the sessions people hold.
func (s *server) liveStamp(ws *jikko.Workspace) string {
	h := sha256.New()
	if entries, err := os.ReadDir(filepath.Join(ws.Root, ".data", "presence")); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				fmt.Fprintf(h, "%s\x00%d\n", e.Name(), info.ModTime().UnixNano())
			}
		}
	}
	active := s.sessions.active()
	for _, name := range sortedNames(active) {
		fmt.Fprintf(h, "%s\x00%d\n", name, active[name].Unix()/60)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sortedNames(m map[string]time.Time) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type server struct {
	cache      *cache
	sessions   *sessionStore
	tmpl       *template.Template
	trustProxy bool
	streams    chan struct{}
	// csrfKey signs the anti-forgery tokens of the forms. It is stored under
	// .data so a restart does not invalidate forms already open.
	csrfKey []byte
	// maxUpload is the maximum size of an uploaded file.
	maxUpload int64
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
	// Status is the HTTP status of a message page.
	Status int
	New    *newView
	// Problems is the check report page.
	Problems *problemsView
	// Report says what a rename or deletion did to references.
	Report *changeReport
	// Online is who is here: running agents and people with a live session.
	Online      []presenceView
	HereSummary string
	// The viewer, for the header.
	ActorName, ActorInitials, ActorURL string
	// Home page only.
	Needs []needView
	Cards []cardView
	// Empty is a workspace with no pages at all: the first-run screen.
	Empty bool
}

func serve(args []string) error {
	var addr, maxUpload *string
	var trustProxy, autoCommit *bool
	args, dir, err := flagsNoToken("serve", args, func(f *flag.FlagSet) {
		addr = f.String("addr", "127.0.0.1:8080", "listen address")
		maxUpload = f.String("max-upload", os.Getenv("JIKKO_MAX_UPLOAD"), "maximum upload size, such as 25M (or JIKKO_MAX_UPLOAD)")
		trustProxy = f.Bool("behind-proxy", false, "trust X-Forwarded-Proto from a reverse proxy when setting cookie Secure")
		autoCommit = f.Bool("auto-commit", false, "record every change as a Git commit with Jikko-Actor and Jikko-Operation trailers")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("serve")
	}
	if _, err := openWorkspace(*dir); err != nil {
		return err
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	warnWithoutGit(root)
	if *autoCommit && !jikko.GitBacked(root) {
		return fmt.Errorf("--auto-commit: %w", jikko.ErrNoGit)
	}

	limit, err := parseSize(*maxUpload)
	if err != nil {
		return err
	}
	s := newServer(root, *trustProxy)
	s.maxUpload = limit
	s.cache.autoCommit = *autoCommit
	srv := &http.Server{
		Addr:              *addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: it would sever live event streams. streamMaxAge
		// bounds their lifetime instead.
	}
	// Bind before announcing, so a bad address fails instead of logging
	// a URL nobody can open.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	log.Printf("serving %s at http://%s", root, ln.Addr())
	return srv.Serve(ln)
}

func newServer(root string, trustProxy bool) *server {
	return &server{
		cache:      &cache{dir: root},
		sessions:   openSessionStore(root),
		tmpl:       template.Must(template.New("jikko").Funcs(templateFuncs).ParseFS(jikkoweb.Templates, "templates/*.html")),
		trustProxy: trustProxy,
		streams:    make(chan struct{}, maxStreams),
		csrfKey:    loadKey(root),
		maxUpload:  jikko.DefaultMaxUpload,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/login", s.login)
	mux.HandleFunc("/logout", s.logout)
	mux.HandleFunc("/pages", s.pages)
	mux.HandleFunc("/events", s.events)
	mux.HandleFunc("GET /new", s.newPage)
	mux.HandleFunc("POST /create", s.createPage)
	mux.HandleFunc("POST /rename/{path...}", s.renamePage)
	mux.HandleFunc("POST /delete/{path...}", s.deletePage)
	mux.HandleFunc("GET /problems", s.problems)
	mux.HandleFunc("GET /p/{path...}", s.pageDetail)
	mux.HandleFunc("GET /edit/{path...}", s.editPage)
	mux.HandleFunc("POST /save/{path...}", s.savePage)
	mux.HandleFunc("POST /comment/{path...}", s.commentPage)
	mux.HandleFunc("POST /status/{path...}", s.setStatus)
	mux.HandleFunc("POST /upload/{path...}", s.uploadFile)
	mux.HandleFunc("GET /events/page/{path...}", s.pageEvents)
	mux.HandleFunc("GET /events/activity/{path...}", s.activityEvents)
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
	s.plainOrPage(w, nil, http.StatusServiceUnavailable, "The workspace could not be read; see the server log.")
}

// plainOrPage answers with a styled message page, or with plain text when
// the page itself cannot be rendered or the request is not for a page.
func (s *server) plainOrPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	name := "page"
	if r != nil && isHTMX(r) {
		name = "message"
	}
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, pageData{Status: status, Message: message}); err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
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
	pages := visiblePages(ws, actor, ws.List(kind, q.Get("status")))
	data := pageData{Pages: pages, Cards: s.cards(ws, pages), Query: query, Actor: actor, Needs: s.needs(ws, actor), Empty: len(ws.Pages) == 0}
	s.decorate(&data, ws)
	return data, nil
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
		s.plainOrPage(w, r, http.StatusNotFound, "There is nothing at this address.")
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
		s.plainOrPage(w, r, http.StatusMethodNotAllowed, "Log in with the form at the top of the workspace page.")
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
		s.refuse(w, r, http.StatusUnauthorized, "", "That token was not accepted. Check that it was pasted whole and that its identity still exists.")
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
		s.plainOrPage(w, r, http.StatusMethodNotAllowed, "Log out with the button at the top of the workspace page.")
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

// openStream starts a server-sent event stream within the connection limit.
// It returns the flusher and a release function, or answers the request
// itself and returns false.
func (s *server) openStream(w http.ResponseWriter) (http.Flusher, func(), bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, nil, false
	}
	select {
	case s.streams <- struct{}{}:
	default:
		http.Error(w, "too many live connections", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, func() { <-s.streams }, true
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, release, ok := s.openStream(w)
	if !ok {
		return
	}
	defer release()

	// A stream never extends the session it runs under, and it closes as soon
	// as the caller it started for is no longer who the request authenticates
	// as: a revoked credential, a removed Identity, or an expired session. The
	// browser then reconnects with whatever access it still has.
	previous, actor, err := s.render("pages", r, false)
	if err != nil {
		return
	}
	// The list is rendered again only when the tree, presence or sessions
	// changed, or every refreshInterval so relative times stay current; a
	// tick that finds nothing changed costs a cached stamp and a session
	// check.
	ws, stamp, _ := s.cache.loadStamped()
	key := stamp + s.liveStamp(ws)
	rendered := time.Now()
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
		ws, stamp, err := s.cache.loadStamped()
		if err != nil {
			continue
		}
		if s.actor(r, ws, false) != actor {
			return
		}
		if next := stamp + s.liveStamp(ws); next == key && time.Since(rendered) < refreshInterval {
			continue
		} else {
			key = next
		}
		current, now, err := s.render("pages", r, false)
		rendered = time.Now()
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
