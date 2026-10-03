package jikko

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// refKind tells what an authored reference may point at.
type refKind int

const (
	// pageReference names a page or a file: [[link]], ![[embed]], and the
	// work-model fields depends_on, blocked_by, and proof.
	pageReference refKind = iota
	// identityReference names an Identity: members, permissions, assignee,
	// and @mentions.
	identityReference
)

// identityFields and pageFields are the frontmatter keys whose values are
// references. permissions is handled separately because it is a mapping.
var (
	identityFields = []string{"members", "assignee"}
	pageFields     = []string{"depends_on", "blocked_by", "proof"}
)

// RefChange describes one authored reference that a semantic operation
// rewrote, removed, or would leave pointing elsewhere.
type RefChange struct {
	// Path is the page holding the reference, as it is after the operation.
	Path string `json:"path"`
	// Field is "body" or the frontmatter key, such as "permissions.read".
	Field string `json:"field"`
	From  string `json:"from"`
	// To is the rewritten reference; empty when the reference was removed or
	// is reported rather than rewritten.
	To string `json:"to,omitempty"`
}

// refAction is what a retarget function decides for one reference.
type refAction int

const (
	keepRef refAction = iota
	replaceRef
	dropRef
)

// retargetFunc decides what happens to one reference. field is "body" or the
// frontmatter key holding it.
type retargetFunc func(field string, kind refKind, ref string) (string, refAction, error)

// rewriteRefs applies fn to every authored reference in a page source: links,
// embeds, and mentions in prose, and the reference-valued frontmatter keys.
// Only frontmatter that actually changes is re-encoded. acl reports whether
// the permissions policy changed. References inside code are documentation
// and are left alone, exactly as they are never resolved.
func rewriteRefs(raw []byte, fn retargetFunc) (out []byte, acl bool, err error) {
	front, body, hasFront := splitFrontmatter(raw)
	if !hasFront && !opensFrontmatter(raw) {
		body = bytes.TrimPrefix(raw, bom)
	}
	head := raw[:len(raw)-len(body)]
	nextBody, err := rewriteBodyRefs(string(body), fn)
	if err != nil {
		return nil, false, err
	}
	out = append(append([]byte(nil), head...), nextBody...)
	if !hasFront || len(bytes.TrimSpace(front)) == 0 {
		return out, false, nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(front, &doc) != nil || mappingOf(&doc) == nil {
		// Frontmatter that cannot be read holds no resolvable reference.
		return out, false, nil
	}
	changed := false
	next, err := rewriteFrontmatter(out, func(m *yaml.Node) error {
		for i := 0; i+1 < len(m.Content); i += 2 {
			key, value := m.Content[i].Value, m.Content[i+1]
			var c, drop bool
			var err error
			switch {
			case key == "permissions" && value.Kind == yaml.MappingNode:
				for j := 0; j+1 < len(value.Content); {
					cc, d, err := rewriteNodeRefs(value.Content[j+1], "permissions."+value.Content[j].Value, identityReference, fn)
					if err != nil {
						return err
					}
					if cc {
						c, acl = true, true
					}
					if d {
						value.Content = append(value.Content[:j], value.Content[j+2:]...)
						continue
					}
					j += 2
				}
			case contains(identityFields, key):
				c, drop, err = rewriteNodeRefs(value, key, identityReference, fn)
			case contains(pageFields, key):
				c, drop, err = rewriteNodeRefs(value, key, pageReference, fn)
			}
			if err != nil {
				return err
			}
			changed = changed || c
			if drop {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				i -= 2
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return out, false, nil
	}
	return next, acl, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// rewriteNodeRefs rewrites the references held by one frontmatter value: a
// scalar, or a sequence of them. A page reference may be written bare or
// wrapped as "[[target]]"; YAML reads an unquoted [[target]] as a nested
// sequence, which is accepted too. drop reports that the whole value should
// be removed because its only reference was dropped.
func rewriteNodeRefs(n *yaml.Node, field string, kind refKind, fn retargetFunc) (changed, drop bool, err error) {
	switch n.Kind {
	case yaml.ScalarNode:
		prefix, target, suffix, ok := splitRefValue(n.Value, kind)
		if !ok {
			return false, false, nil
		}
		next, action, err := fn(field, kind, target)
		switch {
		case err != nil:
			return false, false, err
		case action == dropRef:
			return true, true, nil
		case action == replaceRef:
			n.Value, n.Tag, n.Style = prefix+next+suffix, "!!str", quotedStyle(n.Style)
			return true, false, nil
		}
		return false, false, nil
	case yaml.SequenceNode:
		kept := n.Content[:0]
		for _, item := range n.Content {
			c, d, err := rewriteNodeRefs(item, field, kind, fn)
			if err != nil {
				return false, false, err
			}
			changed = changed || c
			if !d {
				kept = append(kept, item)
			}
		}
		n.Content = kept
		return changed, changed && len(n.Content) == 0, nil
	}
	return false, false, nil
}

// quotedStyle keeps a quoted scalar quoted and leaves any other style for the
// encoder to choose.
func quotedStyle(s yaml.Style) yaml.Style {
	if s&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		return s
	}
	return 0
}

// splitRefValue finds the reference inside a frontmatter value. For page
// references it unwraps "[[target|alias]]" and "![[target]]".
func splitRefValue(v string, kind refKind) (prefix, target, suffix string, ok bool) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "", "", "", false
	}
	if kind == pageReference && strings.HasSuffix(trimmed, "]]") {
		for _, open := range []string{"![[", "[["} {
			if strings.HasPrefix(trimmed, open) {
				inner := trimmed[len(open) : len(trimmed)-2]
				target, alias, _ := strings.Cut(inner, "|")
				if strings.TrimSpace(target) == "" {
					return "", "", "", false
				}
				rest := ""
				if alias != "" || strings.Contains(inner, "|") {
					rest = "|" + alias
				}
				return open, strings.TrimSpace(target), rest + "]]", true
			}
		}
	}
	if kind == pageReference && isExternalRef(trimmed) {
		return "", "", "", false
	}
	return "", trimmed, "", true
}

// isExternalRef reports a reference that points outside the workspace: a URL
// or a "scheme:value" proof such as "commit:1a2b3c".
func isExternalRef(ref string) bool {
	if strings.Contains(ref, "://") {
		return true
	}
	scheme, rest, found := strings.Cut(ref, ":")
	if !found || rest == "" || scheme == "" {
		return false
	}
	for _, r := range scheme {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// rewriteBodyRefs applies fn to every [[link]], ![[embed]], and @mention in
// the prose of a body, leaving code and everything else byte for byte.
func rewriteBodyRefs(body string, fn retargetFunc) (string, error) {
	masked := proseMask(body)
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	var spans [][2]int
	for _, m := range refPattern.FindAllStringSubmatchIndex(masked, -1) {
		inner := body[m[4]:m[5]]
		if strings.Contains(inner, "\n") {
			continue
		}
		spans = append(spans, [2]int{m[0], m[1]})
		target, _, _ := strings.Cut(inner, "|")
		trimmed := strings.TrimSpace(target)
		if trimmed == "" {
			continue
		}
		next, action, err := fn("body", pageReference, trimmed)
		if err != nil {
			return "", err
		}
		if action == replaceRef {
			at := m[4] + strings.Index(target, trimmed)
			edits = append(edits, edit{at, at + len(trimmed), next})
		}
	}
	for _, m := range mentionPattern.FindAllStringSubmatchIndex(masked, -1) {
		name := strings.TrimRight(body[m[4]:m[5]], "./-")
		if name == "" {
			continue
		}
		inside := false
		for _, s := range spans {
			if m[4] >= s[0] && m[4] < s[1] {
				inside = true
			}
		}
		if inside {
			continue
		}
		next, action, err := fn("body", identityReference, name)
		if err != nil {
			return "", err
		}
		if action == replaceRef {
			edits = append(edits, edit{m[4], m[4] + len(name), next})
		}
	}
	if len(edits) == 0 {
		return body, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b strings.Builder
	last := 0
	for _, e := range edits {
		b.WriteString(body[last:e.start])
		b.WriteString(e.text)
		last = e.end
	}
	b.WriteString(body[last:])
	return b.String(), nil
}

// stemIndex maps every name that addresses a page -- its path without ".md"
// and its bare basename -- to the page paths it matches, as Workspace.index
// does for a workspace that does not exist yet.
func stemIndex(paths []string) map[string][]string {
	idx := make(map[string][]string, len(paths)*2)
	for _, p := range paths {
		stem := strings.TrimSuffix(p, ".md")
		idx[stem] = append(idx[stem], p)
		if base := path.Base(stem); base != stem {
			idx[base] = append(idx[base], p)
		}
	}
	return idx
}

func resolveIn(idx map[string][]string, ref string) (string, bool) {
	ref = strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(ref, "\\", "/")), ".md")
	if matches := idx[ref]; ref != "" && len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// relinker retargets the references of a workspace whose pages are about to
// move or disappear. Every reference that resolves now keeps resolving to the
// same page or file afterwards, by being rewritten if necessary.
type relinker struct {
	w      *Workspace
	assets assetIndex
	// moved maps an old page path to its new one; removed lists deleted pages.
	moved   map[string]string
	removed map[string]bool
	after   map[string][]string
	kinds   map[string]Kind

	// rewrite is false for an operation that only reports what would break.
	rewrite bool
	// drop lists removed identities whose members and permissions entries
	// are pruned instead of being left dangling.
	drop map[string]bool

	Changed  []RefChange
	Dropped  []RefChange
	Broken   []RefChange
	Captured []RefChange
}

func (w *Workspace) relinker(moved map[string]string, removed map[string]bool) (*relinker, error) {
	assets, err := w.assets()
	if err != nil {
		return nil, err
	}
	r := &relinker{w: w, assets: assets, moved: moved, removed: removed, kinds: map[string]Kind{}, rewrite: true}
	var paths []string
	for _, p := range w.sorted() {
		if removed[p.Path] {
			continue
		}
		next := r.newPath(p.Path)
		paths = append(paths, next)
		r.kinds[next] = p.Kind
	}
	r.after = stemIndex(paths)
	return r, nil
}

func (r *relinker) newPath(pagePath string) string {
	if next, ok := r.moved[pagePath]; ok {
		return next
	}
	return pagePath
}

// before resolves a reference as it reads now, from the page at from, and
// returns where it should point afterwards: "page:<path>" or "file:<path>",
// or "" when it resolves to nothing. A reference to a removed page returns
// "gone:<path>".
func (r *relinker) before(from string, kind refKind, ref string) string {
	if p, ok := r.w.Resolve(ref); ok {
		if kind == identityReference && p.Kind != Identity {
			return ""
		}
		if r.removed[p.Path] {
			return "gone:" + p.Path
		}
		return "page:" + r.newPath(p.Path)
	}
	if kind == pageReference && isAssetRef(ref) {
		if f, ok, _ := r.assets.resolve(from, ref); ok {
			return "file:" + f
		}
	}
	return ""
}

// after resolves a reference as it would read afterwards from the page's new
// location.
func (r *relinker) afterRef(from string, kind refKind, ref string) string {
	if p, ok := resolveIn(r.after, ref); ok {
		if kind == identityReference && r.kinds[p] != Identity {
			return ""
		}
		return "page:" + p
	}
	if kind == pageReference && isAssetRef(ref) {
		if f, ok, _ := r.assets.resolve(from, ref); ok {
			return "file:" + f
		}
	}
	return ""
}

func isAssetRef(ref string) bool {
	ext := strings.ToLower(path.Ext(ref))
	return ext != "" && ext != ".md"
}

// spell returns the shortest reference, in the style of original, that
// resolves to target afterwards: the bare name when the original was bare and
// the name is unambiguous, otherwise the full path.
func (r *relinker) spell(original, target string) (string, bool) {
	kind, p, _ := strings.Cut(target, ":")
	if kind == "file" {
		return p, true
	}
	stem := strings.TrimSuffix(p, ".md")
	suffix := ""
	if strings.HasSuffix(strings.TrimSpace(original), ".md") {
		suffix = ".md"
	}
	candidates := []string{stem}
	if !strings.Contains(original, "/") {
		candidates = []string{path.Base(stem), stem}
	}
	for _, c := range candidates {
		if got, ok := resolveIn(r.after, c); ok && got == p {
			return c + suffix, true
		}
	}
	return "", false
}

// retarget returns the retarget function for one page, now at from and
// afterwards at r.newPath(from).
func (r *relinker) retarget(from string) retargetFunc {
	to := r.newPath(from)
	return func(field string, kind refKind, ref string) (string, refAction, error) {
		want := r.before(from, kind, ref)
		got := r.afterRef(to, kind, ref)
		change := RefChange{Path: to, Field: field, From: ref}
		if want == "" {
			if got != "" {
				// An unresolved reference would start resolving: the
				// meaning of authored text changes, so say so.
				change.To = strings.SplitN(got, ":", 2)[1]
				r.Captured = append(r.Captured, change)
			}
			return "", keepRef, nil
		}
		if want == got {
			return "", keepRef, nil
		}
		if gone, ok := strings.CutPrefix(want, "gone:"); ok {
			if r.drop[gone] && field != "body" && field != "assignee" && kind == identityReference {
				r.Dropped = append(r.Dropped, change)
				return "", dropRef, nil
			}
			r.Broken = append(r.Broken, change)
			return "", keepRef, nil
		}
		if !r.rewrite {
			r.Broken = append(r.Broken, change)
			return "", keepRef, nil
		}
		next, ok := r.spell(ref, want)
		if !ok {
			return "", keepRef, fmt.Errorf("no reference would resolve unambiguously to %s; choose another name", strings.SplitN(want, ":", 2)[1])
		}
		change.To = next
		r.Changed = append(r.Changed, change)
		return next, replaceRef, nil
	}
}
