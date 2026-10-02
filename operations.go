package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

func identityRef(p *Page) string { return strings.TrimSuffix(p.Path, ".md") }

func (w *Workspace) ActorForToken(token string) (string, error) {
	p, err := w.Authenticate(token)
	if err != nil {
		return "", err
	}
	return identityRef(p), nil
}

func (w *Workspace) AuthorizeToken(token string, p *Page, want Capability) (string, error) {
	actor, err := w.ActorForToken(token)
	if err != nil {
		return "", err
	}
	if !w.Allowed(actor, p, want) {
		return "", fmt.Errorf("%s is not allowed to perform this operation on %s", actor, p.Path)
	}
	return actor, nil
}

func (w *Workspace) SetMetadataWithToken(token, ref, key, value string) error {
	actor, err := w.ActorForToken(token)
	if err != nil {
		return err
	}
	return w.SetMetadata(actor, ref, key, value)
}

// ReplaceBody replaces a page's Markdown body and keeps its frontmatter bytes
// unchanged. It requires write and is judged like any other mutation, so a body
// that changes the workspace's effective access is rejected.
func (w *Workspace) ReplaceBody(actor, ref, body string) error {
	p, ok := w.Resolve(ref)
	if !ok {
		return fmt.Errorf("reference %q not found or ambiguous", ref)
	}
	if p.Kind == View {
		return errors.New("views must not contain a Markdown body")
	}
	if !w.Allowed(actor, p, Write) {
		return fmt.Errorf("%s lacks write on %s", actor, p.Path)
	}
	return w.mutateFile(actor, p, func(raw []byte) ([]byte, error) {
		_, old, hasFront := splitFrontmatter(raw)
		if !hasFront {
			if bytes.HasPrefix(raw, []byte("---")) {
				return nil, errors.New(`frontmatter opens with "---" but has no closing "---" line; fix the file before mutating it`)
			}
			if strings.HasPrefix(body, "---") {
				return nil, errors.New(`body must not open with a "---" frontmatter fence; use set or perm to change metadata`)
			}
			return []byte(body), nil
		}
		next := append([]byte(nil), raw[:len(raw)-len(old)]...)
		return append(next, body...), nil
	})
}

func (w *Workspace) ReplaceBodyWithToken(token, ref, body string) error {
	actor, err := w.ActorForToken(token)
	if err != nil {
		return err
	}
	return w.ReplaceBody(actor, ref, body)
}
