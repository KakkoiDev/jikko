package jikko

import "fmt"

// CanChangeMembers applies the conservative v1 privilege-escalation rule.
// If group membership participates in any file ACL, the actor must already
// administer every affected file before changing that membership.
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
