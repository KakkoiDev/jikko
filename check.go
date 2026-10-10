package jikko

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
