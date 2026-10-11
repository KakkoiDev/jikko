package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Server state that survives a restart lives under .data/, the disposable
// runtime directory (specification §13): the key that signs anti-forgery
// tokens and the table of browser sessions. Losing the directory logs
// everyone out and invalidates open forms, nothing more; the workspace
// keeps its meaning. Both files are written with mode 0600, since a session
// id is as good as the credential it was exchanged for.
const (
	dataDir      = ".data"
	keyFile      = "web-key"
	sessionsFile = "sessions.json"
)

// loadKey returns the server's signing key, creating it on first run. When
// the key cannot be stored -- a read-only workspace, say -- a key for this
// process only is used and the reason logged, so the server still starts.
func loadKey(root string) []byte {
	path := filepath.Join(root, dataDir, keyFile)
	if raw, err := os.ReadFile(path); err == nil {
		if key, err := hex.DecodeString(string(raw)); err == nil && len(key) == 32 {
			return key
		}
		log.Printf("%s is not a usable key; replacing it", path)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	if err := writePrivate(path, []byte(hex.EncodeToString(key))); err != nil {
		log.Printf("the signing key could not be stored (%v); sessions and forms will not survive a restart", err)
	}
	return key
}

// writePrivate writes a file readable by its owner only, creating .data with
// the same restriction, through a temporary file and a rename. It creates
// only .data itself: a workspace that does not exist is not brought into
// being by the server that failed to open it.
func writePrivate(path string, data []byte) error {
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sessionStore exchanges a permanent bearer token for a short-lived browser
// session, so the token itself is never stored in a cookie or replayed on
// every request. Each session remembers the credential it was created from,
// so revoking that credential ends the session too. Sessions are keyed by
// the hash of their id, in memory and on disk alike, so the stored table
// holds no usable secret.
type sessionStore struct {
	mu   sync.Mutex
	m    map[string]sessionEntry
	path string // where the table is persisted; "" keeps it in memory only
}

type sessionEntry struct {
	identity   string
	credential string
	expires    time.Time
	seen       time.Time
}

type storedSession struct {
	Hash       string    `json:"hash"`
	Identity   string    `json:"identity"`
	Credential string    `json:"credential"`
	Expires    time.Time `json:"expires"`
	Seen       time.Time `json:"seen"`
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]sessionEntry{}} }

// openSessionStore loads the sessions persisted under root, dropping the
// expired ones, and persists later changes there.
func openSessionStore(root string) *sessionStore {
	s := newSessionStore()
	s.path = filepath.Join(root, dataDir, sessionsFile)
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("stored sessions could not be read (%v); starting without them", err)
		}
		return s
	}
	var stored []storedSession
	if err := json.Unmarshal(raw, &stored); err != nil {
		log.Printf("stored sessions could not be read (%v); starting without them", err)
		return s
	}
	now := time.Now()
	for _, e := range stored {
		if now.Before(e.Expires) && e.Hash != "" {
			s.m[e.Hash] = sessionEntry{identity: e.Identity, credential: e.Credential, expires: e.Expires, seen: e.Seen}
		}
	}
	return s
}

func sessionHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// persist writes the table. The caller holds s.mu. A failure is logged, not
// returned: the session still works for this process.
func (s *sessionStore) persist() {
	if s.path == "" {
		return
	}
	stored := make([]storedSession, 0, len(s.m))
	for hash, e := range s.m {
		stored = append(stored, storedSession{Hash: hash, Identity: e.identity, Credential: e.credential, Expires: e.expires, Seen: e.seen})
	}
	raw, err := json.Marshal(stored)
	if err == nil {
		err = writePrivate(s.path, raw)
	}
	if err != nil {
		log.Printf("sessions could not be stored (%v); they will not survive a restart", err)
	}
}

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
	s.m[sessionHash(id)] = sessionEntry{identity: identity, credential: credential, expires: now.Add(sessionTTL), seen: now}
	s.persist()
	return id, nil
}

// active reports the identities with a live session and when each last made
// a request, so the interface can show who is here.
func (s *sessionStore) active() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := map[string]time.Time{}
	for _, e := range s.m {
		if now.After(e.expires) {
			continue
		}
		if last, ok := out[e.identity]; !ok || e.seen.After(last) {
			out[e.identity] = e.seen
		}
	}
	return out
}

// lookup returns a live session without extending it.
func (s *sessionStore) lookup(id string) (sessionEntry, bool) {
	if id == "" {
		return sessionEntry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := sessionHash(id)
	e, ok := s.m[hash]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(s.m, hash)
			s.persist()
		}
		return sessionEntry{}, false
	}
	return e, true
}

// touch slides a live session's expiry forward and notes the request. The
// stored table is rewritten only once the expiry has moved by a minute, not
// on every request; "last seen" is precise in memory and approximate across
// a restart.
func (s *sessionStore) touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := sessionHash(id)
	e, ok := s.m[hash]
	if !ok || time.Now().After(e.expires) {
		return
	}
	now := time.Now()
	e.seen = now
	next := now.Add(sessionTTL)
	persist := next.Sub(e.expires) >= time.Minute
	if persist {
		e.expires = next
	}
	s.m[hash] = e
	if persist {
		s.persist()
	}
}

func (s *sessionStore) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := sessionHash(id)
	if _, ok := s.m[hash]; ok {
		delete(s.m, hash)
		s.persist()
	}
}
