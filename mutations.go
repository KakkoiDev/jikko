package jikko

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Permissions exposes whether a page carries a Jikko ACL without leaking its subjects.
func Permissions(p *Page) map[string]any { return permissionMap(p) }

// SetMetadata performs a semantic metadata mutation. Ordinary properties require
// write; the permissions property requires admin. The workspace is reopened after
// writing so identity/ACL integrity checks apply to the proposed state.
func (w *Workspace) SetMetadata(actor, ref, key, value string) error {
	p, ok := w.Resolve(ref); if !ok { return fmt.Errorf("reference %q not found or ambiguous", ref) }
	want := Write; if key == "permissions" { want = Admin }
	if !w.Allowed(actor, p, want) { return fmt.Errorf("%s lacks %v permission on %s", actor, want, p.Path) }
	if key == "members" && p.Kind == Identity { if err := w.CanChangeMembers(actor, ref); err != nil { return err } }

	meta := make(map[string]any, len(p.Metadata)+1); for k, v := range p.Metadata { meta[k] = v }; meta[key] = value
	b, err := yaml.Marshal(meta); if err != nil { return err }
	content := "---\n" + string(b) + "---\n" + p.Body
	path := w.Root + string(os.PathSeparator) + p.Path
	old, err := os.ReadFile(path); if err != nil { return err }
	if err := os.WriteFile(path, []byte(content), 0644); err != nil { return err }
	if _, err := Open(w.Root); err != nil { _ = os.WriteFile(path, old, 0644); return fmt.Errorf("mutation rejected: %w", err) }
	return nil
}

// CanChangeMembers applies the conservative v1 privilege-escalation rule.
func (w *Workspace) CanChangeMembers(actor, group string) error {
	g, ok := w.ResolveIdentity(group); if !ok { return fmt.Errorf("identity %q not found or ambiguous", group) }
	if len(g.Members) == 0 { return fmt.Errorf("identity %q is not a group", group) }
	for _, p := range w.Pages {
		affected := false
		for _, raw := range permissionMap(p) {
			for _, subject := range stringList(raw) {
				s, ok := w.ResolveIdentity(subject); if !ok { continue }
				if s.Path == g.Path || w.containsIdentity(s, g.Path, map[string]bool{}) { affected = true; break }
			}
			if affected { break }
		}
		if affected && !w.Allowed(actor, p, Admin) { return fmt.Errorf("%s cannot change %s membership: admin required on %s", actor, group, p.Path) }
	}
	return nil
}

func (c Capability) String() string { return [...]string{"", "read", "comment", "write", "admin"}[c] }

func normalizeIdentityRef(s string) string { return strings.TrimSuffix(strings.TrimSpace(s), ".md") }
