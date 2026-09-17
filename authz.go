package jikko

import (
	"fmt"
	"strings"
)

type Capability int

const (
	Read Capability = iota + 1
	Comment
	Write
	Admin
)

func ParseCapability(s string) (Capability, bool) {
	switch strings.ToLower(s) {
	case "read": return Read, true
	case "comment": return Comment, true
	case "write": return Write, true
	case "admin": return Admin, true
	default: return 0, false
	}
}

func (w *Workspace) ValidateIdentities() error {
	state := map[string]int{}
	var visit func(*Page) error
	visit = func(p *Page) error {
		if p.Kind != Identity { return nil }
		if state[p.Path] == 1 { return fmt.Errorf("identity membership cycle at %s", p.Path) }
		if state[p.Path] == 2 { return nil }
		state[p.Path] = 1
		for _, ref := range p.Members {
			m, ok := w.ResolveIdentity(ref)
			if !ok { return fmt.Errorf("%s: unresolved identity member %q", p.Path, ref) }
			if err := visit(m); err != nil { return err }
		}
		state[p.Path] = 2
		return nil
	}
	for _, p := range w.Pages { if err := visit(p); err != nil { return err } }
	return nil
}

// MemberOf reports whether individual is directly or transitively a member of group.
func (w *Workspace) MemberOf(individual, group string) bool {
	person, ok := w.ResolveIdentity(individual); if !ok { return false }
	g, ok := w.ResolveIdentity(group); if !ok { return false }
	return w.containsIdentity(g, person.Path, map[string]bool{})
}

func (w *Workspace) containsIdentity(group *Page, target string, seen map[string]bool) bool {
	if seen[group.Path] { return false }; seen[group.Path] = true
	for _, ref := range group.Members {
		m, ok := w.ResolveIdentity(ref); if !ok { continue }
		if m.Path == target || w.containsIdentity(m, target, seen) { return true }
	}
	return false
}

func permissionMap(p *Page) map[string]any {
	m, _ := p.Metadata["permissions"].(map[string]any); return m
}

// Allowed implements admin -> write -> comment -> read. No permissions mapping means open by Jikko.
func (w *Workspace) Allowed(actor string, p *Page, want Capability) bool {
	perms := permissionMap(p)
	if len(perms) == 0 { return true }
	actorPage, ok := w.ResolveIdentity(actor); if !ok || len(actorPage.Members) != 0 { return false }
	for name, raw := range perms {
		grant, ok := ParseCapability(name); if !ok || grant < want { continue }
		for _, subject := range stringList(raw) {
			target, ok := w.ResolveIdentity(subject); if !ok { continue }
			if target.Path == actorPage.Path || w.MemberOf(actor, subject) { return true }
		}
	}
	return false
}

func (w *Workspace) ValidatePermissions() error {
	for _, p := range w.Pages {
		for name, raw := range permissionMap(p) {
			if _, ok := ParseCapability(name); !ok { return fmt.Errorf("%s: unknown permission %q", p.Path, name) }
			for _, ref := range stringList(raw) {
				if _, ok := w.ResolveIdentity(ref); !ok { return fmt.Errorf("%s: unresolved permission identity %q", p.Path, ref) }
			}
		}
	}
	return nil
}
