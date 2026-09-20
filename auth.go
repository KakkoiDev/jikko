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
	if err := writeAtomicMode(authPath(root), []byte("---\n"+string(b)+"---\n"), 0600); err != nil {
		return err
	}
	return ensureAuthGitignored(root)
}

func writeAtomicMode(target string, data []byte, mode os.FileMode) error {
	if err := writeAtomic(target, data); err != nil {
		return err
	}
	return os.Chmod(target, mode)
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
	if token == "" {
		return nil, ErrAuthentication
	}
	a, err := LoadAuth(w.Root)
	if err != nil {
		return nil, err
	}
	want := []byte(tokenHash(token))
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

func (w *Workspace) RevokeCredentials(identity string) error {
	p, ok := w.ResolveIdentity(identity)
	if !ok {
		return fmt.Errorf("identity %q not found or ambiguous", identity)
	}
	a, err := LoadAuth(w.Root)
	if err != nil {
		return err
	}
	stem := strings.TrimSuffix(p.Path, ".md")
	kept := a.Credentials[:0]
	for _, c := range a.Credentials {
		if c.Identity != stem {
			kept = append(kept, c)
		}
	}
	a.Credentials = kept
	return saveAuth(w.Root, a)
}
