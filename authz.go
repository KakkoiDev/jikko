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

var capabilityNames = [...]string{"", "read", "comment", "write", "admin"}

func (c Capability) String() string {
	if c < 1 || int(c) >= len(capabilityNames) {
		return fmt.Sprintf("capability(%d)", int(c))
	}
	return capabilityNames[c]
}

func ParseCapability(s string) (Capability, bool) {
	switch strings.ToLower(s) {
	case "read":
		return Read, true
	case "comment":
		return Comment, true
	case "write":
		return Write, true
	case "admin":
		return Admin, true
	default:
		return 0, false
	}
}

// Permissions returns a page's access-control mapping, or nil when the page
// carries none or carries one that cannot be evaluated. Use Page.Restricted to
// tell "no policy" (open) from "unusable policy" (closed).
func Permissions(p *Page) map[string]any {
	if p == nil || !p.Restricted || !p.aclUsable {
		return nil
	}
	m, _ := p.Metadata["permissions"].(map[string]any)
	return m
}

// checkIdentities reports unresolved members and membership cycles. Membership
// resolution itself is cycle-safe, so a defective graph is reported rather
// than made fatal.
func (w *Workspace) checkIdentities() {
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(p *Page, stack []string)
	visit = func(p *Page, stack []string) {
		switch state[p.Path] {
		case visiting:
			w.report(p.Path, ProblemIdentity, "identity membership cycle: %s", strings.Join(append(stack, p.Path), " -> "))
			return
		case done:
			return
		}
		state[p.Path] = visiting
		for _, ref := range p.Members {
			m, ok := w.ResolveIdentity(ref)
			if !ok {
				w.report(p.Path, ProblemIdentity, "unresolved identity member %q", ref)
				continue
			}
			visit(m, append(stack, p.Path))
		}
		state[p.Path] = done
	}
	for _, p := range w.sorted() {
		if p.Kind == Identity {
			visit(p, nil)
		}
	}
}

// checkPermissions reports unknown capabilities and unresolved grant subjects.
// An unresolvable grant is skipped when access is evaluated, so it can only
// withhold access, never widen it -- but it is never silent.
func (w *Workspace) checkPermissions() {
	for _, p := range w.sorted() {
		perms := Permissions(p)
		if perms == nil {
			continue
		}
		for _, name := range sortedKeys(perms) {
			if _, ok := ParseCapability(name); !ok {
				w.report(p.Path, ProblemPermissions, "unknown permission %q", name)
				continue
			}
			subjects, ok := stringList(perms[name])
			if !ok {
				w.report(p.Path, ProblemPermissions, "permission %q must name an identity or a list of identities", name)
				continue
			}
			if len(subjects) == 0 {
				w.report(p.Path, ProblemPermissions, "permission %q names no identity", name)
				continue
			}
			for _, ref := range subjects {
				if _, ok := w.ResolveIdentity(ref); !ok {
					w.report(p.Path, ProblemPermissions, "unresolved permission identity %q; that grant is ignored", ref)
				}
			}
		}
	}
}

// ValidateIdentities returns the first identity-graph problem, if any.
func (w *Workspace) ValidateIdentities() error { return w.firstProblem(ProblemIdentity) }

// ValidatePermissions returns the first access-control problem, if any.
func (w *Workspace) ValidatePermissions() error { return w.firstProblem(ProblemPermissions) }

// Validate returns the first problem of any kind found in the workspace.
func (w *Workspace) Validate() error { return w.firstProblem("") }

func (w *Workspace) firstProblem(kind string) error {
	for _, p := range w.Problems {
		if kind == "" || p.Kind == kind {
			return p
		}
	}
	return nil
}

// MemberOf reports whether individual is directly or transitively a member of group.
func (w *Workspace) MemberOf(individual, group string) bool {
	person, ok := w.ResolveIdentity(individual)
	if !ok {
		return false
	}
	g, ok := w.ResolveIdentity(group)
	if !ok {
		return false
	}
	return w.containsIdentity(g, person.Path, map[string]bool{})
}

func (w *Workspace) containsIdentity(group *Page, target string, seen map[string]bool) bool {
	if seen[group.Path] {
		return false
	}
	seen[group.Path] = true
	for _, ref := range group.Members {
		m, ok := w.ResolveIdentity(ref)
		if !ok {
			continue
		}
		if m.Path == target || w.containsIdentity(m, target, seen) {
			return true
		}
	}
	return false
}

// Allowed implements admin -> write -> comment -> read.
//
// A file with no permissions mapping is open by Jikko. A file whose mapping is
// present but unusable is closed to everyone: an unreadable policy must never
// be read as the absence of a policy.
func (w *Workspace) Allowed(actor string, p *Page, want Capability) bool {
	if p == nil {
		return false
	}
	if !p.Restricted {
		return true
	}
	if !p.aclUsable {
		return false
	}
	actorPage, ok := w.ResolveIdentity(actor)
	if !ok || len(actorPage.Members) != 0 {
		return false
	}
	for name, raw := range Permissions(p) {
		grant, ok := ParseCapability(name)
		if !ok || grant < want {
			continue
		}
		subjects, _ := stringList(raw)
		for _, subject := range subjects {
			target, ok := w.ResolveIdentity(subject)
			if !ok {
				continue
			}
			if target.Path == actorPage.Path || w.containsIdentity(target, actorPage.Path, map[string]bool{}) {
				return true
			}
		}
	}
	return false
}

// individuals lists every identity that can authenticate, in path order.
func (w *Workspace) individuals() []*Page {
	var out []*Page
	for _, p := range w.sorted() {
		if p.Kind == Identity && len(p.Members) == 0 {
			out = append(out, p)
		}
	}
	return out
}

// Administrators lists the individual identities holding effective admin on a page.
func (w *Workspace) Administrators(p *Page) []string {
	var out []string
	for _, a := range w.individuals() {
		if w.Allowed(strings.TrimSuffix(a.Path, ".md"), p, Admin) {
			out = append(out, a.Path)
		}
	}
	return out
}
