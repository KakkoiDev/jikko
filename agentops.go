package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
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

// MentionPage is a readable page that addresses an identity, with every
// line that does so.
type MentionPage struct {
	Path    string         `json:"path"`
	Title   string         `json:"title"`
	Type    Kind           `json:"type"`
	Matches []MentionMatch `json:"matches"`
}

// MentionMatch is one line of a page that mentions the identity.
type MentionMatch struct {
	// Line is the 1-based line of the page's source file.
	Line int `json:"line"`
	// Text is the line, trimmed.
	Text string `json:"text"`
	// Comment is the id of the comment thread the line belongs to, when the
	// mention was made in a comment.
	Comment string `json:"comment,omitempty"`
}

// Mentions returns readable pages that address actor directly or through one
// of its Identity groups. Mention indexes may later cache this derived result;
// Markdown remains the source of truth.
func (w *Workspace) Mentions(actor string) []*Page {
	var out []*Page
	for _, m := range w.MentionDetails(actor) {
		out = append(out, w.Pages[m.Path])
	}
	return out
}

// MentionDetails is Mentions with the matching lines of each page, so a
// caller can act on a mention without re-reading the page to find it.
func (w *Workspace) MentionDetails(actor string) []MentionPage {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return nil
	}
	names := map[string]bool{}
	// An identity answers to its path and, when that resolves to it alone, to
	// its bare name. An ambiguous bare name addresses nobody.
	address := func(p *Page) {
		names[identityRef(p)] = true
		if base := stemOf(p.Path); base != identityRef(p) {
			if r, ok := w.Resolve(base); ok && r == p {
				names[base] = true
			}
		}
	}
	address(person)
	for _, candidate := range w.sorted() {
		if candidate.Kind != Identity || len(candidate.Members) == 0 {
			continue
		}
		if w.containsIdentity(candidate, person.Path, map[string]bool{}) {
			address(candidate)
		}
	}
	var out []MentionPage
	for _, p := range w.sorted() {
		if !w.Allowed(actor, p, Read) {
			continue
		}
		// "@name:" in front of a comment message attributes it; it is not a
		// request for that identity's attention.
		body := blankCommentAuthors(p.Body)
		spans, _ := scanComments(p.Body)
		offsets := lineOffsets(body)
		var matches []MentionMatch
		scanProseLines(body, func(i int, line string) {
			for _, m := range mentionPattern.FindAllStringSubmatch(line, -1) {
				// Sentence punctuation is not part of the name: "@alice." and
				// "@alice/" address alice.
				if !names[strings.TrimRight(m[2], "./-")] {
					continue
				}
				match := MentionMatch{Line: p.bodyLine + i, Text: strings.TrimSpace(strings.TrimSuffix(strings.Split(p.Body, "\n")[i], "\r"))}
				for _, s := range spans {
					if s.threadStart >= 0 && offsets[i] >= s.threadStart && offsets[i] < s.threadEnd {
						match.Comment = s.id
					}
				}
				matches = append(matches, match)
				return
			}
		})
		if len(matches) > 0 {
			out = append(out, MentionPage{Path: p.Path, Title: p.Title, Type: p.Kind, Matches: matches})
		}
	}
	return out
}

// lineOffsets returns the byte offset at which each line of body starts.
func lineOffsets(body string) []int {
	offsets := []int{0}
	for i, c := range []byte(body) {
		if c == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// AssignedTo reports whether a page's assignee names identity, directly or
// through a group it belongs to (specification §2.2).
func (w *Workspace) AssignedTo(p *Page, identity string) bool {
	person, ok := w.ResolveIdentity(identity)
	if !ok {
		return false
	}
	refs, _ := taskRefs(p, "assignee")
	for _, ref := range refs {
		target, ok := w.ResolveIdentity(ref)
		if !ok {
			continue
		}
		if target == person || w.containsIdentity(target, person.Path, map[string]bool{}) {
			return true
		}
	}
	return false
}

// NewPageSource composes the source of a new page of a kind from content
// that may or may not carry frontmatter. Content without frontmatter gets a
// `type` for a task, view, or identity, and a task also gets `status: todo`.
// Content with frontmatter keeps it; its declared type must agree with kind.
// A view has no body, so view content without frontmatter is read as the
// frontmatter itself.
func NewPageSource(kind Kind, content string) ([]byte, error) {
	raw := []byte(content)
	if !opensFrontmatter(raw) {
		if kind == View {
			raw = []byte("---\n" + strings.TrimRight(content, "\n") + "\n---\n")
		} else if kind != Document {
			raw = append([]byte("---\n---\n"), raw...)
		}
	}
	page, _ := parseSource("new.md", raw, false)
	if declared, has := page.Metadata["type"]; has {
		if s, _ := declared.(string); s != string(kind) && !(kind == Document && s == "") {
			return nil, fmt.Errorf("the content declares type %v, not %s", declared, kind)
		}
	}
	if kind == Document && !opensFrontmatter(raw) {
		return raw, nil
	}
	return rewriteFrontmatter(raw, func(m *yaml.Node) error {
		if kind != Document {
			if err := setMappingDefault(m, "type", string(kind)); err != nil {
				return err
			}
		}
		if kind == Task {
			return setMappingDefault(m, "status", "todo")
		}
		return nil
	})
}

// setMappingDefault sets a frontmatter key only when it is absent.
func setMappingDefault(m *yaml.Node, key, value string) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return nil
		}
	}
	return setMappingValue(m, key, value)
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
	defer w.lock()()
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return fmt.Errorf("authentication required as an individual identity")
	}
	actor = identityRef(person)
	clean, target, err := w.newPagePath(pagePath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%s already exists", clean)
	} else if !os.IsNotExist(err) {
		return err
	}
	return w.stage(actor, "creation", writes("create", map[string][]byte{clean: []byte(content)}), func(current *Workspace) error {
		if _, exists := current.Pages[clean]; exists {
			return fmt.Errorf("%s already exists", clean)
		}
		return nil
	}, func() error {
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%s already exists", clean)
		}
		return writeAtomicPerm(target, []byte(content), 0644)
	})
}

// newPagePath validates the path of a page about to be created or moved and
// returns it workspace-relative and as a file path. It adds ".md" when the
// path has no extension, keeps it inside the workspace, keeps it out of
// harness state, and refuses to follow a symbolic link out of the root.
func (w *Workspace) newPagePath(pagePath string) (clean, target string, err error) {
	pagePath = filepath.ToSlash(strings.TrimSpace(pagePath))
	if filepath.Ext(pagePath) == "" {
		pagePath += ".md"
	}
	if strings.ToLower(filepath.Ext(pagePath)) != ".md" {
		return "", "", fmt.Errorf("Jikko source pages must use .md")
	}
	clean = filepath.ToSlash(filepath.Clean(pagePath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(pagePath) || strings.HasPrefix(pagePath, "/") {
		return "", "", fmt.Errorf("page path must stay inside the workspace")
	}
	if reservedPath(clean) {
		return "", "", fmt.Errorf("%s is reserved for the harness and is not workspace source", clean)
	}
	if Ignored(w.Root, clean) {
		return "", "", fmt.Errorf("%s is excluded by an ignore file, so a page there would never be read", clean)
	}
	// A path that [[reference]] syntax cannot spell would leave the page
	// addressable by no authored reference.
	if strings.ContainsAny(clean, "[]|") || strings.IndexFunc(clean, unicode.IsControl) >= 0 {
		return "", "", fmt.Errorf("%q: a page path cannot contain [, ], |, or control characters", clean)
	}
	target = filepath.Join(w.Root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(w.Root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("page path must stay inside the workspace")
	}
	// A directory on the way may be a symbolic link. Resolve the part that
	// exists before creating anything, so nothing is written outside the root.
	if err := w.containedDir(filepath.Dir(target)); err != nil {
		return "", "", err
	}
	return clean, target, nil
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
