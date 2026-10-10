package jikko

// AssignedTo returns the readable Tasks whose assignee names actor, or a
// group actor belongs to, in path order. Like Mentions it is derived from
// Markdown each time; no assignment index is stored.
func (w *Workspace) AssignedTo(actor string) []*Page {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return nil
	}
	var out []*Page
	for _, p := range w.sorted() {
		if p.Kind != Task || !w.Allowed(actor, p, Read) {
			continue
		}
		refs, _ := taskRefs(p, "assignee")
		for _, ref := range refs {
			target, ok := w.ResolveIdentity(ref)
			if !ok {
				continue
			}
			if target.Path == person.Path || w.containsIdentity(target, person.Path, map[string]bool{}) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// CheckResults gathers what `jikko check` reports -- workspace problems,
// unresolved references and warnings -- for the pages actor may read, and
// counts the ones it may not. The asset index is read once.
func (w *Workspace) CheckResults(actor string) (problems, warnings []Problem, hidden int, err error) {
	unresolved, err := w.UnresolvedReferences()
	if err != nil {
		return nil, nil, 0, err
	}
	visible := func(in []Problem) []Problem {
		out := []Problem{}
		for _, pr := range in {
			if p, ok := w.Pages[pr.Path]; ok && w.Allowed(actor, p, Read) {
				out = append(out, pr)
			} else {
				hidden++
			}
		}
		return out
	}
	problems = visible(append(append([]Problem{}, w.Problems...), unresolved...))
	warnings = visible(w.Warnings())
	return problems, warnings, hidden, nil
}
