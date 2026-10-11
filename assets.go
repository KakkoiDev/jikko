package jikko

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ProblemReference reports a link or embed that resolves to nothing.
const ProblemReference = "reference"

// assetIndex lists the workspace's non-Markdown files, the targets of embeds
// such as ![[diagram.png]] (specification §5.2). Harness state and ignored
// files are excluded, and so are symbolic links, which a reference must not
// follow out of the workspace.
type assetIndex struct {
	paths  map[string]bool
	byBase map[string][]string
}

// fileIndex caches the asset index and the pages using each file for one
// workspace state. It is built on first use and dropped when the workspace
// adopts a new state.
type fileIndex struct {
	once sync.Once
	idx  assetIndex
	err  error
	// usedBy maps a file to the pages that link, embed, show or cite it.
	usedBy map[string][]string
}

func (w *Workspace) assets() (assetIndex, error) {
	fi := w.fileIndex()
	return fi.idx, fi.err
}

func (w *Workspace) fileIndex() *fileIndex {
	w.filesMu.Lock()
	if w.files == nil {
		w.files = &fileIndex{}
	}
	fi := w.files
	w.filesMu.Unlock()
	fi.once.Do(func() {
		fi.idx, fi.err = scanAssets(w.Root)
		if fi.err != nil {
			return
		}
		fi.usedBy = map[string][]string{}
		for _, p := range w.sorted() {
			for _, ref := range w.fileRefs(p) {
				if f, ok, _ := fi.idx.resolve(p.Path, ref); ok {
					fi.usedBy[f] = append(fi.usedBy[f], p.Path)
				}
			}
		}
	})
	return fi
}

// fileRefs lists the references of a page that may name a file: links,
// embeds, Markdown images, and a Task's proof.
func (w *Workspace) fileRefs(p *Page) []string {
	refs := append(append([]string{}, p.Links...), p.Embeds...)
	refs = append(refs, imageSources(p.Body)...)
	if p.Kind == Task {
		proofs, _ := taskRefs(p, "proof")
		refs = append(refs, proofs...)
	}
	return refs
}

func scanAssets(root string) (assetIndex, error) {
	idx := assetIndex{paths: map[string]bool{}, byBase: map[string][]string{}}
	err := WalkFiles(root, func(p, rel string, d fs.DirEntry) error {
		if !d.Type().IsRegular() || strings.EqualFold(filepath.Ext(p), ".md") || strings.EqualFold(d.Name(), ".auth.md") {
			return nil
		}
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
	fi := w.fileIndex()
	if fi.err != nil || !fi.idx.paths[rel] {
		return "", false
	}
	for _, pagePath := range fi.usedBy[rel] {
		if p, ok := w.Pages[pagePath]; ok && w.Allowed(actor, p, Read) {
			return filepath.Join(w.Root, filepath.FromSlash(rel)), true
		}
	}
	return "", false
}
