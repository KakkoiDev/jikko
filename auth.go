package jikko

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Credential struct {
	Identity  string `yaml:"identity" json:"identity"`
	TokenHash string `yaml:"token_hash" json:"token_hash"`
}

type AuthFile struct { Credentials []Credential `yaml:"credentials" json:"credentials"` }

func tokenHash(token string) string { sum := sha256.Sum256([]byte(token)); return hex.EncodeToString(sum[:]) }

func authPath(root string) string { return filepath.Join(root, ".auth.md") }

func LoadAuth(root string) (*AuthFile, error) {
	b, err := os.ReadFile(authPath(root))
	if errors.Is(err, os.ErrNotExist) { return &AuthFile{}, nil }
	if err != nil { return nil, err }
	meta, _, err := parseMarkdown(string(b)); if err != nil { return nil, err }
	b, err = yaml.Marshal(meta); if err != nil { return nil, err }
	var a AuthFile; if err := yaml.Unmarshal(b, &a); err != nil { return nil, err }
	return &a, nil
}

func saveAuth(root string, a *AuthFile) error {
	b, err := yaml.Marshal(a); if err != nil { return err }
	content := "---\n" + string(b) + "---\n"
	if err := os.WriteFile(authPath(root), []byte(content), 0600); err != nil { return err }
	return ensureAuthGitignored(root)
}

func ensureAuthGitignored(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
	for _, line := range strings.Split(string(b), "\n") { if strings.TrimSpace(line) == ".auth.md" { return nil } }
	prefix := string(b); if prefix != "" && !strings.HasSuffix(prefix, "\n") { prefix += "\n" }
	return os.WriteFile(path, []byte(prefix+".auth.md\n"), 0644)
}

// CreateCredential creates a bearer token for an existing individual Identity.
// The token is returned once; only its SHA-256 hash is persisted.
func (w *Workspace) CreateCredential(identity string) (string, error) {
	p, ok := w.ResolveIdentity(identity); if !ok { return "", fmt.Errorf("identity %q not found or ambiguous", identity) }
	if len(p.Members) != 0 { return "", errors.New("groups cannot authenticate directly") }
	raw := make([]byte, 32); if _, err := rand.Read(raw); err != nil { return "", err }
	token := "jk_" + base64.RawURLEncoding.EncodeToString(raw)
	a, err := LoadAuth(w.Root); if err != nil { return "", err }
	a.Credentials = append(a.Credentials, Credential{Identity: strings.TrimSuffix(p.Path, ".md"), TokenHash: tokenHash(token)})
	if err := saveAuth(w.Root, a); err != nil { return "", err }
	return token, nil
}

func (w *Workspace) Authenticate(token string) (*Page, bool) {
	a, err := LoadAuth(w.Root); if err != nil { return nil, false }
	h := tokenHash(token)
	for _, c := range a.Credentials {
		if c.TokenHash != h { continue }
		p, ok := w.ResolveIdentity(c.Identity); if ok && len(p.Members) == 0 { return p, true }
	}
	return nil, false
}

func (w *Workspace) RevokeCredentials(identity string) error {
	p, ok := w.ResolveIdentity(identity); if !ok { return fmt.Errorf("identity %q not found or ambiguous", identity) }
	a, err := LoadAuth(w.Root); if err != nil { return err }
	stem := strings.TrimSuffix(p.Path, ".md")
	out := a.Credentials[:0]
	for _, c := range a.Credentials { if c.Identity != stem { out = append(out, c) } }
	a.Credentials = out
	return saveAuth(w.Root, a)
}
