package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TreeEntry is the deliberately small representation exposed to agents.
// Forbidden pages are omitted entirely so the tree cannot leak their existence.
type TreeEntry struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Type  Kind   `json:"type"`
}

// Tree returns every Markdown page the actor may read, in deterministic path order.
func (w *Workspace) Tree(actor string) []TreeEntry {
	out := []TreeEntry{}
	for _, p := range w.sorted() {
		if !w.Allowed(actor, p, Read) {
			continue
		}
		out = append(out, TreeEntry{Path: p.Path, Title: p.Title, Type: p.Kind})
	}
	return out
}

// ReadMany resolves several references without leaking whether a failed
// reference was absent, ambiguous, or forbidden. Results preserve input order.
func (w *Workspace) ReadMany(actor string, refs ...string) ([]*Page, error) {
	out := make([]*Page, 0, len(refs))
	for _, ref := range refs {
		p, ok := w.Resolve(ref)
		if !ok || !w.Allowed(actor, p, Read) {
			return nil, fmt.Errorf("reference %q not found, ambiguous, or not permitted", ref)
		}
		out = append(out, p)
	}
	return out, nil
}

var mentionPattern = regexp.MustCompile(`(^|[^[:alnum:]_./-])@([[:alnum:]_./-]+)`)

// Mentions returns readable pages that address actor directly or through one
// of its Identity groups. Mention indexes may later cache this derived result;
// Markdown remains the source of truth.
func (w *Workspace) Mentions(actor string) []*Page {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return nil
	}
	names := map[string]bool{
		strings.TrimSuffix(person.Path, ".md"):                true,
		strings.TrimSuffix(filepath.Base(person.Path), ".md"): true,
	}
	for _, candidate := range w.sorted() {
		if candidate.Kind != Identity || len(candidate.Members) == 0 {
			continue
		}
		if w.containsIdentity(candidate, person.Path, map[string]bool{}) {
			names[strings.TrimSuffix(candidate.Path, ".md")] = true
			names[strings.TrimSuffix(filepath.Base(candidate.Path), ".md")] = true
		}
	}
	var out []*Page
	for _, p := range w.sorted() {
		if !w.Allowed(actor, p, Read) {
			continue
		}
		found := false
		scanProse(p.Body, func(line string) {
			if found {
				return
			}
			for _, m := range mentionPattern.FindAllStringSubmatch(line, -1) {
				if names[m[2]] {
					found = true
					return
				}
			}
		})
		if found {
			out = append(out, p)
		}
	}
	return out
}

// CreatePage creates a Markdown source page through the same validation model
// as other semantic mutations. Creation requires an authenticated individual.
// New pages are open by default unless their supplied frontmatter says otherwise.
//
// Creation is judged by its effect like any other mutation: a new page can
// introduce an Identity that an existing grant or group already names, so it
// is rejected if it would raise anyone's access to a file the creator may not
// administer. The creator administers the page it creates.
func (w *Workspace) CreatePage(actor, pagePath, content string) error {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return fmt.Errorf("authentication required as an individual identity")
	}
	actor = identityRef(person)
	pagePath = filepath.ToSlash(strings.TrimSpace(pagePath))
	if filepath.Ext(pagePath) == "" {
		pagePath += ".md"
	}
	if strings.ToLower(filepath.Ext(pagePath)) != ".md" {
		return fmt.Errorf("Jikko source pages must use .md")
	}
	clean := filepath.ToSlash(filepath.Clean(pagePath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(pagePath) {
		return fmt.Errorf("page path must stay inside the workspace")
	}
	if reservedPath(clean) {
		return fmt.Errorf("%s is reserved for the harness and is not workspace source", clean)
	}
	target := filepath.Join(w.Root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(w.Root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("page path must stay inside the workspace")
	}
	// A directory on the way may be a symbolic link. Resolve the part that
	// exists before creating anything, so nothing is written outside the root.
	if err := w.containedDir(filepath.Dir(target)); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%s already exists", clean)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	before := w.snapshot()
	if err := writeAtomic(target, []byte(content)); err != nil {
		return err
	}
	proposed, err := Open(w.Root)
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	adminBefore := func(p string) bool { return p == clean || before.admins[p][actor] }
	if err := before.diff(proposed.snapshot(), adminBefore); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("creation rejected: %w", err)
	}
	*w = *proposed
	return nil
}

// reservedPath reports whether a workspace-relative path belongs to harness
// state rather than source: Git internals, derived .data, or credentials.
// Open never indexes these, so a page created there would be invisible.
func reservedPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".data") || strings.EqualFold(part, ".auth.md") {
			return true
		}
	}
	return false
}

// containedDir verifies that dir, with symbolic links resolved in the longest
// prefix that already exists, lies inside the workspace root.
func (w *Workspace) containedDir(dir string) error {
	root, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		return err
	}
	existing := dir
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("page path must stay inside the workspace")
	}
	return nil
}

// AccessiblePaths is useful to harnesses that only need names and want to
// avoid serializing titles/types. It is kept deterministic for reproducible prompts.
func (w *Workspace) AccessiblePaths(actor string) []string {
	entries := w.Tree(actor)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}
