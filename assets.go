package jikko

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ProblemReference reports a link or embed that resolves to nothing.
const ProblemReference = "reference"

// assetIndex lists the workspace's non-Markdown files, the targets of embeds
// such as ![[diagram.png]] (specification §5.2). Harness state is excluded,
// and so are symbolic links, which a reference must not follow out of the
// workspace.
type assetIndex struct {
	paths  map[string]bool
	byBase map[string][]string
}

func (w *Workspace) assets() (assetIndex, error) {
	idx := assetIndex{paths: map[string]bool{}, byBase: map[string][]string{}}
	err := filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".data" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.EqualFold(filepath.Ext(p), ".md") || strings.EqualFold(d.Name(), ".auth.md") {
			return nil
		}
		rel, err := filepath.Rel(w.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		idx.paths[rel] = true
		idx.byBase[path.Base(rel)] = append(idx.byBase[path.Base(rel)], rel)
		return nil
	})
	return idx, err
}

// resolve finds a file reference the way pages resolve: a path from the
// workspace root or from the referring page's folder, or a bare name that
// matches exactly one file.
func (idx assetIndex) resolve(from, ref string) (string, bool, bool) {
	ref = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(ref)), "/")
	for _, candidate := range []string{path.Clean(ref), path.Join(path.Dir(from), ref)} {
		if idx.paths[candidate] {
			return candidate, true, false
		}
	}
	if strings.Contains(ref, "/") {
		return "", false, false
	}
	matches := idx.byBase[ref]
	if len(matches) == 1 {
		return matches[0], true, false
	}
	return "", false, len(matches) > 1
}

// UnresolvedReferences reports every [[link]] and ![[embed]] that names
// neither a page nor a file of the workspace, and every work-model reference
// of a Task that does not resolve to what its field requires (specification
// §2.2).
func (w *Workspace) UnresolvedReferences() ([]Problem, error) {
	var idx *assetIndex
	var idxErr error
	resolveFile := func(from, ref string) (bool, bool) {
		if idx == nil && idxErr == nil {
			built, err := w.assets()
			idx, idxErr = &built, err
		}
		if idxErr != nil {
			return false, false
		}
		_, ok, ambiguous := idx.resolve(from, ref)
		return ok, ambiguous
	}
	var out []Problem
	for _, p := range w.sorted() {
		for _, ref := range append(append([]string{}, p.Links...), p.Embeds...) {
			if _, ok := w.Resolve(ref); ok {
				continue
			}
			if isAssetRef(ref) {
				ok, ambiguous := resolveFile(p.Path, ref)
				if ok {
					continue
				}
				if ambiguous {
					out = append(out, Problem{Path: p.Path, Kind: ProblemReference, Message: fmt.Sprintf("ambiguous %q; give its path", ref)})
					continue
				}
			}
			out = append(out, Problem{Path: p.Path, Kind: ProblemReference, Message: fmt.Sprintf("unresolved %q", ref)})
		}
	}
	out = append(out, w.workReferenceProblems(resolveFile)...)
	if idxErr != nil {
		return nil, idxErr
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ReadableFile resolves a workspace file the actor may fetch: a
// non-Markdown file that a page the actor may read links or embeds. Files
// carry no access policy of their own, so they take it from the pages that
// use them; a file no readable page references is not served at all, and
// neither is harness state, a symbolic link, or a dotfile. It returns the
// file's path on disk.
func (w *Workspace) ReadableFile(actor, rel string) (string, bool) {
	rel = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(rel)), "/")
	if rel == "" || reservedPath(rel) {
		return "", false
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	idx, err := w.assets()
	if err != nil || !idx.paths[rel] {
		return "", false
	}
	for _, p := range w.sorted() {
		if !w.Allowed(actor, p, Read) {
			continue
		}
		refs := append(append([]string{}, p.Links...), p.Embeds...)
		if p.Kind == Task {
			proofs, _ := taskRefs(p, "proof")
			refs = append(refs, proofs...)
		}
		for _, ref := range refs {
			if f, ok, _ := idx.resolve(p.Path, ref); ok && f == rel {
				return filepath.Join(w.Root, filepath.FromSlash(rel)), true
			}
		}
	}
	return "", false
}
