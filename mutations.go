package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SetMetadata performs a semantic metadata mutation. Ordinary properties
// require write; the permissions property requires admin and must be changed
// through SetPermission, because a plain value cannot express a mapping.
//
// The value is interpreted as YAML, so `3` stores a number and `2026-09-20` a
// date. A property that currently holds a sequence accepts a comma-separated
// list.
func (w *Workspace) SetMetadata(actor, ref, key, value string) error {
	p, ok := w.Resolve(ref)
	if !ok {
		return fmt.Errorf("reference %q not found or ambiguous", ref)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("property name required")
	}
	if key == "permissions" {
		return errors.New("permissions is an access-control policy: use `jikko perm <reference> <capability> [identity...]`")
	}
	if !w.Allowed(actor, p, Write) {
		return fmt.Errorf("%s lacks write permission on %s", actor, p.Path)
	}
	return w.mutate(actor, p, func(m *yaml.Node) error { return setMappingValue(m, key, value) })
}

// SetPermission grants a capability on a page to the named identities,
// replacing any existing grant for that capability. Passing no identity
// removes the grant. Changing access-control policy requires admin.
func (w *Workspace) SetPermission(actor, ref, capability string, subjects ...string) error {
	p, ok := w.Resolve(ref)
	if !ok {
		return fmt.Errorf("reference %q not found or ambiguous", ref)
	}
	want, ok := ParseCapability(capability)
	if !ok {
		return fmt.Errorf("unknown capability %q: expected read, comment, write, or admin", capability)
	}
	if !w.Allowed(actor, p, Admin) {
		return fmt.Errorf("%s lacks admin permission on %s", actor, p.Path)
	}
	clean := make([]string, 0, len(subjects))
	for _, s := range subjects {
		s = strings.TrimSuffix(strings.TrimSpace(s), ".md")
		if s == "" {
			continue
		}
		if _, ok := w.ResolveIdentity(s); !ok {
			return fmt.Errorf("identity %q not found or ambiguous", s)
		}
		clean = append(clean, s)
	}
	return w.mutate(actor, p, func(m *yaml.Node) error { return setPermissionEntry(m, want.String(), clean) })
}

// mutate applies edit to a page's frontmatter and commits it only if the
// resulting workspace is no worse than the current one. It enforces optimistic
// concurrency, writes atomically, and rolls back on rejection.
func (w *Workspace) mutate(actor string, p *Page, edit func(*yaml.Node) error) error {
	if actorPage, ok := w.ResolveIdentity(actor); ok {
		actor = strings.TrimSuffix(actorPage.Path, ".md")
	}
	target, err := w.safePath(p)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if fingerprint(raw) != p.rev {
		return fmt.Errorf("%s changed on disk after it was read; re-read the workspace and retry", p.Path)
	}
	next, err := rewriteFrontmatter(raw, edit)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Path, err)
	}
	if bytes.Equal(next, raw) {
		return nil
	}

	before := w.snapshot()
	adminBefore := func(pagePath string) bool { return before.admins[pagePath][actor] }
	if err := writeAtomic(target, next); err != nil {
		return err
	}
	proposed, err := Open(w.Root)
	if err != nil {
		_ = writeAtomic(target, raw)
		return fmt.Errorf("mutation rejected: %w", err)
	}
	if err := before.diff(proposed.snapshot(), adminBefore); err != nil {
		_ = writeAtomic(target, raw)
		return fmt.Errorf("mutation rejected: %w", err)
	}
	*w = *proposed
	return nil
}

// safePath resolves a page to a real file inside the workspace. Jikko never
// writes through a symbolic link, so a mutation cannot reach outside the root.
func (w *Workspace) safePath(p *Page) (string, error) {
	linkErr := fmt.Errorf("%s is a symbolic link; edit the file it points at instead", p.Path)
	if p.symlink {
		return "", linkErr
	}
	target := filepath.Join(w.Root, filepath.FromSlash(p.Path))
	info, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", linkErr
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s resolves outside the workspace", p.Path)
	}
	return target, nil
}

// writeAtomic replaces a file through a temporary file and a rename, so a
// crash or a concurrent reader never observes a half-written page.
func writeAtomic(target string, data []byte) error {
	mode := os.FileMode(0644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".jikko-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// rewriteFrontmatter edits a file's frontmatter in place, preserving key
// order, comments, scalar styles, and the body byte for byte.
func rewriteFrontmatter(raw []byte, edit func(*yaml.Node) error) ([]byte, error) {
	front, body, hasFront := splitFrontmatter(raw)
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if !hasFront {
		if bytes.HasPrefix(raw, []byte("---")) {
			return nil, errors.New(`frontmatter opens with "---" but has no closing "---" line; fix the file before mutating it`)
		}
		body = raw
	} else if len(bytes.TrimSpace(front)) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(front, &doc); err != nil {
			return nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
		}
		if m := mappingOf(&doc); m != nil {
			mapping = m
		} else {
			return nil, errors.New("frontmatter is not a YAML mapping")
		}
	}
	if err := edit(mapping); err != nil {
		return nil, err
	}

	var encoded bytes.Buffer
	enc := yaml.NewEncoder(&encoded)
	enc.SetIndent(2)
	if err := enc.Encode(mapping); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	header := append([]byte("---\n"), encoded.Bytes()...)
	header = append(header, []byte("---\n")...)
	if usesCRLF(front) || (!hasFront && usesCRLF(body)) {
		header = bytes.ReplaceAll(header, []byte("\n"), []byte("\r\n"))
	}
	return append(header, body...), nil
}

func usesCRLF(b []byte) bool { return bytes.Contains(b, []byte("\r\n")) }

// setMappingValue assigns a plain value to a frontmatter key, refusing to
// flatten a nested mapping into a scalar and keeping a sequence a sequence.
func setMappingValue(m *yaml.Node, key, value string) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		switch node := m.Content[i+1]; node.Kind {
		case yaml.MappingNode:
			return fmt.Errorf("%q holds a YAML mapping and cannot be replaced by a plain value", key)
		case yaml.SequenceNode:
			node.Content = scalarNodes(splitList(value))
			node.Tag, node.Value = "!!seq", ""
		default:
			assignScalar(node, value)
		}
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, newValueNode(value))
	return nil
}

// setPermissionEntry sets or clears one capability inside the permissions
// mapping, creating the mapping if the page has none.
func setPermissionEntry(m *yaml.Node, capability string, subjects []string) error {
	var perms *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "permissions" {
			perms = m.Content[i+1]
			break
		}
	}
	if perms == nil {
		if len(subjects) == 0 {
			return nil
		}
		perms = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "permissions"}, perms)
	}
	if perms.Kind != yaml.MappingNode {
		// Recover a malformed policy rather than leaving the file unusable.
		*perms = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for i := 0; i+1 < len(perms.Content); i += 2 {
		if perms.Content[i].Value != capability {
			continue
		}
		if len(subjects) == 0 {
			if len(perms.Content) == 2 {
				return fmt.Errorf("clearing %q would leave an empty policy, which denies everyone; remove the permissions block by hand to reopen the file", capability)
			}
			perms.Content = append(perms.Content[:i], perms.Content[i+2:]...)
		} else {
			*perms.Content[i+1] = *subjectNode(subjects)
		}
		return nil
	}
	if len(subjects) == 0 {
		return nil
	}
	perms.Content = append(perms.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: capability}, subjectNode(subjects))
	return nil
}

func subjectNode(subjects []string) *yaml.Node {
	if len(subjects) == 1 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: subjects[0]}
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: scalarNodes(subjects)}
}

func scalarNodes(values []string) []*yaml.Node {
	out := make([]*yaml.Node, 0, len(values))
	for _, v := range values {
		out = append(out, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	}
	return out
}

func newValueNode(value string) *yaml.Node {
	if items := splitList(value); len(items) > 1 {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: scalarNodes(items)}
	}
	node := &yaml.Node{Kind: yaml.ScalarNode}
	assignScalar(node, value)
	return node
}

// assignScalar lets YAML infer the value's type, so `3` stores a number and
// `2026-09-20` a date. A non-empty input never becomes null, and anything YAML
// cannot read back as a scalar is stored as a string.
func assignScalar(node *yaml.Node, value string) {
	node.Kind, node.Value, node.Tag, node.Style = yaml.ScalarNode, value, "", 0
	var probe any
	if value == "" || yaml.Unmarshal([]byte(value), &probe) != nil || probe == nil {
		node.Tag = "!!str"
		return
	}
	switch probe.(type) {
	case map[string]any, []any:
		node.Tag = "!!str"
	}
}

func splitList(value string) []string {
	if !strings.Contains(value, ",") {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []string{strings.TrimSpace(value)}
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// snapshot captures the effective authorization of a workspace so a mutation
// can be judged by what it would change, not merely by who may write the file.
type snapshot struct {
	problems   map[string]bool
	restricted map[string]bool
	grants     map[string]Capability
	admins     map[string]map[string]bool
}

func (w *Workspace) snapshot() snapshot {
	s := snapshot{
		problems:   map[string]bool{},
		restricted: map[string]bool{},
		grants:     map[string]Capability{},
		admins:     map[string]map[string]bool{},
	}
	for _, p := range w.Problems {
		s.problems[p.Error()] = true
	}
	individuals := w.individuals()
	for _, p := range w.sorted() {
		s.restricted[p.Path] = p.Restricted
		s.admins[p.Path] = map[string]bool{}
		for _, a := range individuals {
			actor := strings.TrimSuffix(a.Path, ".md")
			for c := Admin; c >= Read; c-- {
				if !w.Allowed(actor, p, c) {
					continue
				}
				s.grants[actor+"\x00"+p.Path] = c
				if c == Admin {
					s.admins[p.Path][actor] = true
				}
				break
			}
		}
	}
	return s
}

// diff rejects a proposed workspace that introduces a defect, widens anyone's
// access beyond what the actor may administer, or strips a restricted file of
// its last administrator.
func (before snapshot) diff(after snapshot, adminBefore func(pagePath string) bool) error {
	for _, pagePath := range sortedKeys(before.admins) {
		if len(before.admins[pagePath]) > 0 && after.restricted[pagePath] && len(after.admins[pagePath]) == 0 {
			return fmt.Errorf("it would leave %s with no identity able to administer it; grant admin first", pagePath)
		}
	}
	for _, problem := range sortedKeys(after.problems) {
		if !before.problems[problem] {
			return fmt.Errorf("it would introduce a workspace problem: %s", problem)
		}
	}
	for _, key := range sortedKeys(after.grants) {
		if after.grants[key] <= before.grants[key] {
			continue
		}
		actor, pagePath, _ := strings.Cut(key, "\x00")
		if !adminBefore(pagePath) {
			return fmt.Errorf("it would grant %s %s on %s, which you may not administer", actor, after.grants[key], pagePath)
		}
	}
	for _, pagePath := range sortedKeys(before.restricted) {
		if before.restricted[pagePath] && !after.restricted[pagePath] && !adminBefore(pagePath) {
			return fmt.Errorf("it would remove the access policy of %s, which you may not administer", pagePath)
		}
	}
	return nil
}

// CanChangeMembers applies the conservative rule that changing a group taking
// part in any access-control policy requires admin on every file it affects.
// Mutations are additionally judged by their actual effect; see snapshot.diff.
func (w *Workspace) CanChangeMembers(actor, group string) error {
	g, ok := w.ResolveIdentity(group)
	if !ok {
		return fmt.Errorf("identity %q not found or ambiguous", group)
	}
	if len(g.Members) == 0 {
		return fmt.Errorf("identity %q is not a group", group)
	}
	for _, p := range w.sorted() {
		affected := false
		for _, raw := range Permissions(p) {
			subjects, _ := stringList(raw)
			for _, subject := range subjects {
				s, ok := w.ResolveIdentity(subject)
				if !ok {
					continue
				}
				if s.Path == g.Path || w.containsIdentity(s, g.Path, map[string]bool{}) {
					affected = true
					break
				}
			}
			if affected {
				break
			}
		}
		if affected && !w.Allowed(actor, p, Admin) {
			return fmt.Errorf("%s cannot change %s membership: admin required on %s", actor, group, p.Path)
		}
	}
	return nil
}
