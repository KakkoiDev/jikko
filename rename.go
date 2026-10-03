package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RenameOptions adjusts Rename.
type RenameOptions struct {
	// NoRewrite moves the file without rewriting any reference and reports
	// the references that will no longer resolve (specification §7). It is
	// still refused if it would break a membership or an access policy,
	// because that would silently change who may do what.
	NoRewrite bool
}

// RenameResult reports what a rename did. Each list holds only references
// in pages the actor may read; Hidden counts the others.
type RenameResult struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Rewritten lists the references rewritten to keep pointing at the
	// same page or file.
	Rewritten []RefChange `json:"rewritten"`
	// Broken lists references that no longer resolve to what they did.
	Broken []RefChange `json:"broken"`
	// Captured lists references that resolved to nothing and now resolve,
	// so their meaning changed.
	Captured []RefChange `json:"captured"`
	Hidden   int         `json:"hidden,omitempty"`
}

// DeleteOptions adjusts Delete.
type DeleteOptions struct {
	// Prune removes a deleted Identity from every group's members and from
	// every access policy instead of refusing the deletion. Each removal is
	// judged like any other change of membership or policy.
	Prune bool
}

// DeleteResult reports what a deletion did, filtered like RenameResult.
type DeleteResult struct {
	Path string `json:"path"`
	// Pruned lists the membership and policy entries removed with Prune.
	Pruned []RefChange `json:"pruned"`
	// Broken lists references to the deleted page that remain in place.
	Broken []RefChange `json:"broken"`
	// Captured lists references that were ambiguous and now resolve.
	Captured []RefChange `json:"captured"`
	Hidden   int         `json:"hidden,omitempty"`
}

// Rename moves a page and rewrites every authored reference that resolved to
// it -- [[links]], ![[embeds]], the work-model fields, and, for an Identity,
// group members, access policies, assignees, and @mentions -- so it keeps
// resolving to the same page. References to other pages that the move would
// make ambiguous are rewritten to their full path, and the moved page's own
// relative asset embeds are rewritten from the workspace root.
//
// Rename needs write on the page. Rewriting another page is an edit of that
// page and needs write on it, and admin where its access policy changes; the
// rename is refused, with the pages named, rather than half done. The whole
// change is staged and judged like any other mutation: a rename that would
// change anyone's effective access is rejected.
//
// Credentials of a renamed Identity follow it, so its tokens keep working
// and cannot be claimed by a new Identity given the old name.
func (w *Workspace) Rename(actor, ref, to string, opt RenameOptions) (*RenameResult, error) {
	defer w.lock()()
	cur, err := Open(w.Root)
	if err != nil {
		return nil, err
	}
	actor, err = cur.individual(actor)
	if err != nil {
		return nil, err
	}
	p, err := cur.readable(actor, ref)
	if err != nil {
		return nil, err
	}
	if !cur.Allowed(actor, p, Write) {
		return nil, fmt.Errorf("%s lacks write permission on %s", actor, p.Path)
	}
	if _, err := cur.safePath(p); err != nil {
		return nil, err
	}
	clean, target, err := cur.newPagePath(to)
	if err != nil {
		return nil, err
	}
	if clean == p.Path {
		return nil, fmt.Errorf("%s is already at that path", p.Path)
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("%s already exists", clean)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	r, err := cur.relinker(map[string]string{p.Path: clean}, nil)
	if err != nil {
		return nil, err
	}
	r.rewrite = !opt.NoRewrite
	plan, err := cur.relink(actor, r, p.Path)
	if err != nil {
		return nil, err
	}
	plan.write[clean] = plan.source[p.Path]
	if next, ok := plan.write[p.Path]; ok {
		plan.write[clean] = next
		delete(plan.write, p.Path)
	}
	c := change{write: plan.write, remove: map[string]bool{p.Path: true}, moved: map[string]string{clean: p.Path}}
	oldTarget := filepath.Join(cur.Root, filepath.FromSlash(p.Path))
	mode := os.FileMode(0644)
	if info, err := os.Stat(oldTarget); err == nil {
		mode = info.Mode().Perm()
	}
	err = w.stage(actor, "rename", c, plan.unchanged, func() error {
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%s already exists", clean)
		}
		if err := writeAtomicPerm(target, c.write[clean], mode); err != nil {
			return err
		}
		if err := plan.apply(cur.Root, clean); err != nil {
			return err
		}
		if err := os.Remove(oldTarget); err != nil {
			return err
		}
		if p.Kind == Identity {
			return moveCredentials(cur.Root, identityRef(p), strings.TrimSuffix(clean, ".md"))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := &RenameResult{From: p.Path, To: clean}
	if actor == identityRef(p) {
		actor = strings.TrimSuffix(clean, ".md")
	}
	res.Rewritten, res.Hidden = w.visibleChanges(actor, r.Changed, res.Hidden)
	res.Broken, res.Hidden = w.visibleChanges(actor, r.Broken, res.Hidden)
	res.Captured, res.Hidden = w.visibleChanges(actor, r.Captured, res.Hidden)
	return res, nil
}

// Delete removes a page. It needs write on the page, as the permission
// semantics define, and is staged and judged like any other mutation.
//
// A page with unresolved comments is refused: its discussion would vanish
// from current Markdown without anyone resolving it (specification §8).
//
// An Identity named by a group's members or by an access policy is refused
// and the references are reported, because deleting it would silently
// change who may do what. With Prune the references are removed instead,
// which needs write on each group and admin on each policy changed, and is
// still refused if it would leave a file with no administrator or widen
// anyone's access. Other references to the page -- links, embeds, assignees,
// mentions -- are left in place and reported as broken, as `jikko check`
// would report them. The credentials of a deleted Identity are revoked.
func (w *Workspace) Delete(actor, ref string, opt DeleteOptions) (*DeleteResult, error) {
	defer w.lock()()
	cur, err := Open(w.Root)
	if err != nil {
		return nil, err
	}
	actor, err = cur.individual(actor)
	if err != nil {
		return nil, err
	}
	p, err := cur.readable(actor, ref)
	if err != nil {
		return nil, err
	}
	if !cur.Allowed(actor, p, Write) {
		return nil, fmt.Errorf("%s lacks write permission on %s", actor, p.Path)
	}
	if _, err := cur.safePath(p); err != nil {
		return nil, err
	}
	if n := len(p.Comments); n > 0 {
		return nil, fmt.Errorf("%s has %d unresolved comment(s); resolve them before deleting the page", p.Path, n)
	}
	r, err := cur.relinker(nil, map[string]bool{p.Path: true})
	if err != nil {
		return nil, err
	}
	r.rewrite = false
	if opt.Prune {
		r.drop = map[string]bool{p.Path: true}
	}
	plan, err := cur.relink(actor, r, "")
	if err != nil {
		return nil, err
	}
	if p.Kind == Identity && !opt.Prune {
		var blocking []RefChange
		for _, b := range r.Broken {
			if b.Field == "members" || strings.HasPrefix(b.Field, "permissions.") {
				blocking = append(blocking, b)
			}
		}
		if len(blocking) > 0 {
			shown, hidden := w.visibleChanges(actor, blocking, 0)
			var where []string
			for _, b := range shown {
				where = append(where, b.Path+" ("+b.Field+")")
			}
			if hidden > 0 {
				where = append(where, fmt.Sprintf("%d reference(s) in pages you cannot read", hidden))
			}
			return nil, fmt.Errorf("%s is named by %s; deleting it would change who may do what. Pass --prune to remove those references", p.Path, strings.Join(where, ", "))
		}
	}
	c := change{write: plan.write, remove: map[string]bool{p.Path: true}}
	target := filepath.Join(cur.Root, filepath.FromSlash(p.Path))
	err = w.stage(actor, "deletion", c, plan.unchanged, func() error {
		if err := plan.apply(cur.Root, ""); err != nil {
			return err
		}
		if err := os.Remove(target); err != nil {
			return err
		}
		if p.Kind == Identity {
			return dropCredentials(cur.Root, identityRef(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res := &DeleteResult{Path: p.Path}
	res.Pruned, res.Hidden = w.visibleChanges(actor, r.Dropped, res.Hidden)
	res.Broken, res.Hidden = w.visibleChanges(actor, r.Broken, res.Hidden)
	res.Captured, res.Hidden = w.visibleChanges(actor, r.Captured, res.Hidden)
	return res, nil
}

// individual resolves an actor to the reference of an individual Identity.
func (w *Workspace) individual(actor string) (string, error) {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return "", errors.New("authentication required as an individual identity")
	}
	return identityRef(person), nil
}

// readable resolves a reference the actor may read, without disclosing
// whether a failure was an absent, ambiguous, or forbidden page.
func (w *Workspace) readable(actor, ref string) (*Page, error) {
	p, ok := w.Resolve(ref)
	if !ok || !w.Allowed(actor, p, Read) {
		return nil, fmt.Errorf("reference %q not found, ambiguous, or not permitted", ref)
	}
	return p, nil
}

// relinkPlan holds the page rewrites a rename or deletion needs.
type relinkPlan struct {
	write  map[string][]byte
	source map[string][]byte
	revs   map[string]string
	paths  map[string]string // page path -> file path
}

// relink rewrites every page through r and checks that the actor may make
// each rewrite. subject is the page being moved, whose own rewrite is
// covered by write on it and is returned under its old path.
func (w *Workspace) relink(actor string, r *relinker, subject string) (*relinkPlan, error) {
	plan := &relinkPlan{write: map[string][]byte{}, source: map[string][]byte{}, revs: map[string]string{}, paths: map[string]string{}}
	var denied []string
	hidden := 0
	for _, q := range w.sorted() {
		if r.removed[q.Path] {
			continue
		}
		file := filepath.Join(w.Root, filepath.FromSlash(q.Path))
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if fingerprint(raw) != q.Rev {
			return nil, fmt.Errorf("%s changed on disk after it was read; retry", q.Path)
		}
		plan.revs[q.Path] = q.Rev
		if q.Path == subject {
			plan.source[q.Path] = raw
		}
		next, acl, err := rewriteRefs(raw, r.retarget(q.Path))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", q.Path, err)
		}
		if bytes.Equal(next, raw) {
			continue
		}
		need := Write
		if acl {
			need = Admin
		}
		if !w.Allowed(actor, q, need) {
			if w.Allowed(actor, q, Read) {
				denied = append(denied, fmt.Sprintf("%s (needs %s)", q.Path, need))
			} else {
				hidden++
			}
			continue
		}
		if q.Path != subject {
			if _, err := w.safePath(q); err != nil {
				return nil, fmt.Errorf("cannot rewrite the references in %s: %w", q.Path, err)
			}
		}
		plan.write[q.Path] = next
		plan.paths[q.Path] = file
	}
	if hidden > 0 {
		denied = append(denied, fmt.Sprintf("%d page(s) you cannot read", hidden))
	}
	if len(denied) > 0 {
		sort.Strings(denied)
		return nil, fmt.Errorf("its references would have to be rewritten in pages you may not edit: %s", strings.Join(denied, ", "))
	}
	return plan, nil
}

// unchanged is a stage precheck: every page read for the plan still holds
// the bytes it was read from.
func (plan *relinkPlan) unchanged(current *Workspace) error {
	for pagePath, rev := range plan.revs {
		if p, ok := current.Pages[pagePath]; !ok || p.Rev != rev {
			return fmt.Errorf("%s changed on disk after it was read; retry", pagePath)
		}
	}
	return nil
}

// apply writes the rewritten pages, except skip, which the caller writes.
func (plan *relinkPlan) apply(root, skip string) error {
	for _, pagePath := range sortedKeys(plan.write) {
		if pagePath == skip {
			continue
		}
		file, ok := plan.paths[pagePath]
		if !ok {
			continue
		}
		if err := writeAtomic(file, plan.write[pagePath]); err != nil {
			return err
		}
	}
	return nil
}

// visibleChanges appends to hidden the changes in pages the actor may not
// read and returns the rest, so a report never discloses a forbidden path.
func (w *Workspace) visibleChanges(actor string, changes []RefChange, hidden int) ([]RefChange, int) {
	out := []RefChange{}
	for _, c := range changes {
		if p, ok := w.Pages[c.Path]; ok && w.Allowed(actor, p, Read) {
			out = append(out, c)
		} else {
			hidden++
		}
	}
	return out, hidden
}
