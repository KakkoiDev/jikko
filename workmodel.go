package jikko

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// Work-model problem and warning categories (specification §2.2).
const (
	// ProblemWork reports a work-model field that cannot be read.
	ProblemWork = "work"
	// WarningWork marks a Task whose state contradicts its work-model fields.
	WarningWork = "work"
)

// completionRequirements are the values a Task's `requires` may list.
var completionRequirements = map[string]string{
	"assignee": "an assignee",
	"proof":    "proof",
	"review":   "no unresolved comments",
}

var commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// taskRefs returns the references a work-model key holds on a page. ok is
// false when the value is neither a reference nor a list of them.
func taskRefs(p *Page, key string) (refs []string, ok bool) {
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case nil:
			return true
		case string:
			kind := pageReference
			if key == "assignee" {
				kind = identityReference
			}
			if _, target, _, found := splitRefValue(x, kind); found {
				refs = append(refs, target)
			} else if strings.TrimSpace(x) != "" {
				// An external proof such as a URL or commit:<sha>.
				refs = append(refs, strings.TrimSpace(x))
			}
			return true
		case []any:
			for _, item := range x {
				if !walk(item) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	return refs, walk(p.Metadata[key])
}

// workReferenceProblems validates the reference-valued work-model fields of
// every Task: an assignee must name an Identity, depends_on a Task, blocked_by
// a page, and proof a page, a file, a URL, or a commit of the workspace's Git
// history. resolveFile resolves a non-Markdown reference from a page.
func (w *Workspace) workReferenceProblems(resolveFile func(from, ref string) (bool, bool)) []Problem {
	var out []Problem
	report := func(p *Page, format string, args ...any) {
		out = append(out, Problem{Path: p.Path, Kind: ProblemReference, Message: fmt.Sprintf(format, args...)})
	}
	for _, p := range w.sorted() {
		if p.Kind != Task {
			continue
		}
		for _, key := range []string{"assignee", "depends_on", "blocked_by", "proof"} {
			refs, ok := taskRefs(p, key)
			if !ok {
				out = append(out, Problem{Path: p.Path, Kind: ProblemWork, Message: fmt.Sprintf("%s must be a reference or a list of references", key)})
				continue
			}
			for _, ref := range refs {
				target, resolved := w.Resolve(ref)
				switch key {
				case "assignee":
					if !resolved || target.Kind != Identity {
						report(p, "assignee %q is not an identity", ref)
					}
				case "depends_on":
					if !resolved || target.Kind != Task {
						report(p, "depends_on %q is not a task", ref)
					}
				case "blocked_by":
					if !resolved {
						report(p, "unresolved blocked_by %q", ref)
					}
				case "proof":
					if resolved {
						continue
					}
					if sha, isCommit := strings.CutPrefix(ref, "commit:"); isCommit {
						if !commitPattern.MatchString(sha) {
							report(p, "proof %q is not a commit id", ref)
						} else if known, checked := w.commitExists(sha); checked && !known {
							report(p, "proof %q names no commit in this workspace's history", ref)
						}
						continue
					}
					if isExternalRef(ref) {
						continue
					}
					if isAssetRef(ref) {
						if found, ambiguous := resolveFile(p.Path, ref); found {
							continue
						} else if ambiguous {
							report(p, "ambiguous proof %q; give its path", ref)
							continue
						}
					}
					report(p, "unresolved proof %q", ref)
				}
			}
		}
		requires, ok := stringList(p.Metadata["requires"])
		if !ok {
			out = append(out, Problem{Path: p.Path, Kind: ProblemWork, Message: "requires must be a list of completion requirements"})
		}
		for _, r := range requires {
			if _, known := completionRequirements[r]; !known {
				out = append(out, Problem{Path: p.Path, Kind: ProblemWork, Message: fmt.Sprintf("unknown completion requirement %q; expected assignee, proof, or review", r)})
			}
		}
	}
	return out
}

// commitExists asks Git whether sha names a commit. checked is false where
// that cannot be answered: no Git, or a workspace outside a repository.
func (w *Workspace) commitExists(sha string) (known, checked bool) {
	if err := exec.Command("git", "-C", w.Root, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return false, false
	}
	return exec.Command("git", "-C", w.Root, "cat-file", "-e", sha+"^{commit}").Run() == nil, true
}

// WorkWarnings reports Tasks whose state contradicts their work-model
// fields. Like the warning for unresolved comments, these never fail a check
// or refuse a change: completing work is the author's call, and the harness
// surfaces what is unmet.
func (w *Workspace) WorkWarnings() []Problem {
	var out []Problem
	warn := func(p *Page, format string, args ...any) {
		out = append(out, Problem{Path: p.Path, Kind: WarningWork, Message: fmt.Sprintf(format, args...)})
	}
	for _, p := range w.sorted() {
		if p.Kind != Task {
			continue
		}
		assignees, _ := taskRefs(p, "assignee")
		if matchesStatus(p.Metadata["status"], "doing") && len(assignees) == 0 {
			warn(p, "task is in progress but has no assignee")
		}
		if !matchesStatus(p.Metadata["status"], "done") {
			continue
		}
		for _, key := range []string{"depends_on", "blocked_by"} {
			refs, _ := taskRefs(p, key)
			for _, ref := range refs {
				if target, ok := w.Resolve(ref); ok && target.Kind == Task && !matchesStatus(target.Metadata["status"], "done") {
					warn(p, "task is done but %s %s is not", key, target.Path)
				}
			}
		}
		requires, _ := stringList(p.Metadata["requires"])
		for _, r := range requires {
			unmet := false
			switch r {
			case "assignee":
				unmet = len(assignees) == 0
			case "proof":
				proofs, _ := taskRefs(p, "proof")
				unmet = len(proofs) == 0
			case "review":
				unmet = len(p.Comments) > 0
			}
			if unmet {
				warn(p, "task is done but requires %s", completionRequirements[r])
			}
		}
	}
	return out
}

// Warnings returns every warning of the workspace: open discussion on
// completed Tasks and unmet work-model expectations.
func (w *Workspace) Warnings() []Problem {
	out := append([]Problem{}, w.UnresolvedCommentWarnings()...)
	out = append(out, w.WorkWarnings()...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
