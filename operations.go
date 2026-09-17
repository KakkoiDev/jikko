package jikko

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func identityRef(p *Page) string { return strings.TrimSuffix(p.Path, ".md") }

func (w *Workspace) ActorForToken(token string) (string, error) {
	p, ok := w.Authenticate(token)
	if !ok { return "", errors.New("authentication failed") }
	return identityRef(p), nil
}

func (w *Workspace) AuthorizeToken(token string, p *Page, want Capability) (string, error) {
	actor, err := w.ActorForToken(token)
	if err != nil { return "", err }
	if !w.Allowed(actor, p, want) { return "", fmt.Errorf("%s is not allowed to perform this operation on %s", actor, p.Path) }
	return actor, nil
}

func renderMarkdown(meta map[string]any, body string) ([]byte, error) {
	if len(meta) == 0 { return []byte(body), nil }
	b, err := yaml.Marshal(meta)
	if err != nil { return nil, err }
	return []byte("---\n" + string(b) + "---\n" + body), nil
}

func (w *Workspace) SetMetadata(actor, ref, key string, value any) error {
	p, ok := w.Resolve(ref)
	if !ok { return fmt.Errorf("reference %q not found or ambiguous", ref) }
	want := Write
	if key == "permissions" { want = Admin }
	if !w.Allowed(actor, p, want) { return fmt.Errorf("%s lacks %v on %s", actor, want, p.Path) }
	meta := make(map[string]any, len(p.Metadata)+1)
	for k, v := range p.Metadata { meta[k] = v }
	meta[key] = value
	content, err := renderMarkdown(meta, p.Body)
	if err != nil { return err }
	if err := os.WriteFile(filepath.Join(w.Root, filepath.FromSlash(p.Path)), content, 0644); err != nil { return err }
	return nil
}

func (w *Workspace) SetMetadataWithToken(token, ref, key string, value any) error {
	actor, err := w.ActorForToken(token)
	if err != nil { return err }
	return w.SetMetadata(actor, ref, key, value)
}

func (w *Workspace) ReplaceBody(actor, ref, body string) error {
	p, ok := w.Resolve(ref)
	if !ok { return fmt.Errorf("reference %q not found or ambiguous", ref) }
	if p.Kind == View { return errors.New("views must not contain a Markdown body") }
	if !w.Allowed(actor, p, Write) { return fmt.Errorf("%s lacks write on %s", actor, p.Path) }
	content, err := renderMarkdown(p.Metadata, body)
	if err != nil { return err }
	return os.WriteFile(filepath.Join(w.Root, filepath.FromSlash(p.Path)), content, 0644)
}

func (w *Workspace) ReplaceBodyWithToken(token, ref, body string) error {
	actor, err := w.ActorForToken(token)
	if err != nil { return err }
	return w.ReplaceBody(actor, ref, body)
}
