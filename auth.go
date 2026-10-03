package jikko

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrAuthentication is returned for every failed authentication so a caller
// can answer uniformly without disclosing which part of the check failed.
var ErrAuthentication = errors.New("authentication failed")

type Credential struct {
	Identity  string `yaml:"identity" json:"identity"`
	TokenHash string `yaml:"token_hash" json:"token_hash"`
}

type AuthFile struct {
	Credentials []Credential `yaml:"credentials" json:"credentials"`
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func authPath(root string) string { return filepath.Join(root, ".auth.md") }

// LoadAuth reads the harness-local credential file. A file that exists but
// cannot be understood is an error: silently reading it as "no credentials"
// would turn a corrupted file into a workspace nobody can authenticate to.
func LoadAuth(root string) (*AuthFile, error) {
	path := authPath(root)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &AuthFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	front, _, ok := splitFrontmatter(b)
	if !ok {
		return nil, fmt.Errorf("%s: no YAML frontmatter block found", path)
	}
	var a AuthFile
	if err := yaml.Unmarshal(front, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i, c := range a.Credentials {
		if c.Identity == "" || c.TokenHash == "" {
			return nil, fmt.Errorf("%s: credential %d is missing identity or token_hash", path, i+1)
		}
	}
	return &a, nil
}

func saveAuth(root string, a *AuthFile) error {
	b, err := yaml.Marshal(a)
	if err != nil {
		return err
	}
	if err := writeAtomicPerm(authPath(root), []byte("---\n"+string(b)+"---\n"), 0600); err != nil {
		return err
	}
	return ensureAuthGitignored(root)
}

// gitignorePatterns that already exclude the credential file.
var authIgnorePatterns = map[string]bool{".auth.md": true, "/.auth.md": true, "**/.auth.md": true}

func ensureAuthGitignored(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if authIgnorePatterns[strings.TrimSpace(line)] {
			return nil
		}
	}
	prefix := string(b)
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return os.WriteFile(path, []byte(prefix+".auth.md\n"), 0644)
}

// AuthTrackedByGit reports whether .auth.md is already tracked by Git. Adding
// it to .gitignore does not remove credential hashes already in history, so
// the harness warns instead of implying the problem is solved.
func AuthTrackedByGit(root string) bool {
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", ".auth.md")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// CreateCredential creates a bearer token for an existing individual Identity.
// The token is returned once; only its SHA-256 hash is persisted.
func (w *Workspace) CreateCredential(identity string) (string, error) {
	p, ok := w.ResolveIdentity(identity)
	if !ok {
		return "", fmt.Errorf("identity %q not found or ambiguous", identity)
	}
	if len(p.Members) != 0 {
		return "", errors.New("groups cannot authenticate directly")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := "jk_" + base64.RawURLEncoding.EncodeToString(raw)
	// Read-modify-write of .auth.md: without the lock, two concurrent
	// creations could each append to the same old list and one would vanish.
	defer w.lock()()
	a, err := LoadAuth(w.Root)
	if err != nil {
		return "", err
	}
	a.Credentials = append(a.Credentials, Credential{Identity: strings.TrimSuffix(p.Path, ".md"), TokenHash: tokenHash(token)})
	if err := saveAuth(w.Root, a); err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate maps a bearer token to the individual Identity that owns it.
// Every credential is compared in constant time and the whole list is scanned,
// so neither the hash nor the position of a match is observable by timing.
func (w *Workspace) Authenticate(token string) (*Page, error) {
	p, _, err := w.AuthenticateCredential(token)
	return p, err
}

// AuthenticateCredential is Authenticate that also returns the id of the
// credential that matched. A harness that issues something derived from the
// token, such as a browser session, keeps the id so it can later ask
// CredentialIdentity whether that credential still stands.
//
// The id is the credential's stored hash: it identifies the credential
// uniquely and reveals nothing that .auth.md does not already hold, but it is
// not a bearer token and does not authenticate anyone.
func (w *Workspace) AuthenticateCredential(token string) (*Page, string, error) {
	if token == "" {
		return nil, "", ErrAuthentication
	}
	p, err := w.CredentialIdentity(tokenHash(token))
	if err != nil {
		return nil, "", err
	}
	return p, tokenHash(token), nil
}

// CredentialIdentity re-checks a credential by id: it must still be stored
// in .auth.md and still map to an existing individual Identity. Revoking the
// credential, deleting its Identity, or turning that Identity into a group
// all make it fail.
func (w *Workspace) CredentialIdentity(id string) (*Page, error) {
	if id == "" {
		return nil, ErrAuthentication
	}
	a, err := LoadAuth(w.Root)
	if err != nil {
		return nil, err
	}
	want := []byte(id)
	matched := ""
	for _, c := range a.Credentials {
		if subtle.ConstantTimeCompare([]byte(c.TokenHash), want) == 1 {
			matched = c.Identity
		}
	}
	if matched == "" {
		return nil, ErrAuthentication
	}
	p, ok := w.ResolveIdentity(matched)
	if !ok {
		return nil, fmt.Errorf("%w: credential maps to unknown or ambiguous identity %q", ErrAuthentication, matched)
	}
	if len(p.Members) != 0 {
		return nil, fmt.Errorf("%w: identity %q is a group and cannot authenticate directly", ErrAuthentication, matched)
	}
	return p, nil
}

// RevokeCredentials removes every credential of an identity. The identity
// file may already be gone: its credentials are then matched by the name they
// were stored under, because an orphaned credential would authenticate again
// as soon as an identity of that name reappeared.
func (w *Workspace) RevokeCredentials(identity string) error {
	stem := strings.TrimSuffix(strings.TrimSpace(filepath.ToSlash(identity)), ".md")
	p, resolved := w.ResolveIdentity(identity)
	if resolved {
		stem = strings.TrimSuffix(p.Path, ".md")
	}
	defer w.lock()()
	a, err := LoadAuth(w.Root)
	if err != nil {
		return err
	}
	kept := make([]Credential, 0, len(a.Credentials))
	for _, c := range a.Credentials {
		if c.Identity != stem {
			kept = append(kept, c)
		}
	}
	if len(kept) == len(a.Credentials) {
		if !resolved {
			return fmt.Errorf("identity %q not found or ambiguous, and no credential is stored under that name", identity)
		}
		return nil
	}
	a.Credentials = kept
	return saveAuth(w.Root, a)
}
