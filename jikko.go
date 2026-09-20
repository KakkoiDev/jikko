package jikko

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Kind string

const (
	Document Kind = "document"
	Task     Kind = "task"
	View     Kind = "view"
	Identity Kind = "identity"
)

// ParseKind maps a frontmatter `type` value to a Kind. An absent type is a Document.
func ParseKind(s string) (Kind, bool) {
	switch s {
	case "", "document": return Document, true
	case "task": return Task, true
	case "view": return View, true
	case "identity": return Identity, true
	default: return "", false
	}
}

// Problem categories. Problems are reported rather than fatal: one malformed
// file must never make the rest of a workspace unusable.
const (
	ProblemParse       = "parse"
	ProblemStructure   = "structure"
	ProblemIdentity    = "identity"
	ProblemPermissions = "permissions"
)

type Problem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (p Problem) Error() string { return p.Path + ": " + p.Message }

type Page struct {
	Path      string         `json:"path"`
	Title     string         `json:"title"`
	Kind      Kind           `json:"type"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Body      string         `json:"body,omitempty"`
	Links     []string       `json:"links,omitempty"`
	Embeds    []string       `json:"embeds,omitempty"`
	Backlinks []string       `json:"backlinks,omitempty"`
	Members   []string       `json:"members,omitempty"`

	// Restricted reports whether the file carries a `permissions` mapping.
	// Jikko imposes no restriction on a file without one.
	Restricted bool `json:"restricted,omitempty"`

	// aclUsable is false when a `permissions` key is present but cannot be
	// evaluated at all. Such a file is denied to everyone rather than opened.
	aclUsable bool
	// rev fingerprints the bytes this page was parsed from, for optimistic
	// concurrency control on mutation.
	rev string
	// symlink marks a page reached through a symbolic link. It is readable
	// but never written through, so a mutation cannot escape the workspace.
	symlink bool
}

type Workspace struct {
	Root     string
	Pages    map[string]*Page
	Problems []Problem

	byStem map[string][]*Page
}

// Open scans a workspace into memory. It fails only on I/O errors: content
// defects are collected in Problems and reported by `jikko check`, and any
// page whose access policy cannot be evaluated is denied to everyone.
func Open(root string) (*Workspace, error) {
	root, err := filepath.Abs(root)
	if err != nil { return nil, err }
	w := &Workspace{Root: root, Pages: map[string]*Page{}}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".data" { return filepath.SkipDir }
			return nil
		}
		if d.Name() == ".auth.md" { return nil }
		if !strings.EqualFold(filepath.Ext(p), ".md") { return nil }
		rel, err := filepath.Rel(root, p)
		if err != nil { return err }
		rel = filepath.ToSlash(rel)
		page, problems := parseFile(p, rel, d)
		w.Pages[rel] = page
		w.Problems = append(w.Problems, problems...)
		return nil
	})
	if err != nil { return nil, err }
	w.index()
	w.deriveBacklinks()
	w.checkIdentities()
	w.checkPermissions()
	sort.SliceStable(w.Problems, func(i, j int) bool { return w.Problems[i].Path < w.Problems[j].Path })
	return w, nil
}

func (w *Workspace) report(pagePath, kind, format string, args ...any) {
	w.Problems = append(w.Problems, Problem{Path: pagePath, Kind: kind, Message: fmt.Sprintf(format, args...)})
}

func parseFile(fullPath, rel string, d fs.DirEntry) (*Page, []Problem) {
	var problems []Problem
	add := func(kind, format string, args ...any) {
		problems = append(problems, Problem{Path: rel, Kind: kind, Message: fmt.Sprintf(format, args...)})
	}
	p := &Page{Path: rel, Kind: Document, Metadata: map[string]any{}, aclUsable: true, symlink: d.Type()&fs.ModeSymlink != 0}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		p.Title = stemOf(rel)
		add(ProblemParse, "unreadable: %v", err)
		return p, problems
	}
	p.rev = fingerprint(raw)

	front, body, hasFront := splitFrontmatter(raw)
	p.Body = string(body)
	if !hasFront && bytes.HasPrefix(raw, []byte("---")) {
		add(ProblemParse, `frontmatter opens with "---" but no closing "---" line was found; all metadata was ignored`)
	}
	if hasFront && len(bytes.TrimSpace(front)) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(front, &doc); err != nil {
			add(ProblemParse, "invalid YAML frontmatter: %v", err)
		} else if m := mappingOf(&doc); m == nil {
			add(ProblemParse, "frontmatter must be a YAML mapping")
		} else if err := m.Decode(&p.Metadata); err != nil {
			add(ProblemParse, "invalid YAML frontmatter: %v", err)
			p.Metadata = map[string]any{}
		}
	}

	if raw, ok := p.Metadata["type"]; ok {
		s, isString := raw.(string)
		kind, known := ParseKind(s)
		if !isString || !known {
			add(ProblemStructure, "unknown type %v; treated as a document", raw)
		} else {
			p.Kind = kind
		}
	}
	p.Title = titleOf(p.Body, rel)
	p.Links, p.Embeds = refsIn(p.Body)

	if p.Kind == View && strings.TrimSpace(p.Body) != "" {
		add(ProblemStructure, "views must not contain a Markdown body")
	}
	if p.Kind == Identity {
		members, ok := stringList(p.Metadata["members"])
		if !ok {
			add(ProblemIdentity, "members must name an identity or a list of identities")
		}
		p.Members = members
	}

	if value, present := p.Metadata["permissions"]; present {
		p.Restricted = true
		switch m := value.(type) {
		case map[string]any:
			if len(m) == 0 {
				p.aclUsable = false
				add(ProblemPermissions, "permissions mapping is empty, so the file is denied to everyone")
			}
		default:
			p.aclUsable = false
			add(ProblemPermissions, "permissions must be a YAML mapping of capability to identity, so the file is denied to everyone")
		}
	}
	return p, problems
}

// mappingOf returns the mapping node of a decoded YAML document, or nil.
func mappingOf(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) != 1 { return nil }
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode { return nil }
	return n
}

var fence = []byte("---")

// splitFrontmatter separates a YAML frontmatter block from the body. Both the
// opening and the closing delimiter must be a line containing exactly "---".
// LF and CRLF files are both accepted and the body is returned byte-exact.
func splitFrontmatter(src []byte) (front, body []byte, ok bool) {
	first, rest, found := bytes.Cut(src, []byte("\n"))
	if !found || !bytes.Equal(bytes.TrimSuffix(first, []byte("\r")), fence) { return nil, src, false }
	for offset := 0; offset < len(rest); {
		line, advance := nextLine(rest[offset:])
		if bytes.Equal(bytes.TrimSuffix(line, []byte("\r")), fence) { return rest[:offset], rest[offset+advance:], true }
		offset += advance
	}
	return nil, src, false
}

func nextLine(b []byte) (line []byte, advance int) {
	if i := bytes.IndexByte(b, '\n'); i >= 0 { return b[:i], i + 1 }
	return b, len(b)
}

func fingerprint(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

// stringList normalizes a scalar or sequence of identity references. It
// reports false when the value cannot be read as either, so callers can fail
// closed instead of silently treating a malformed field as absent.
func stringList(v any) ([]string, bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case string:
		if strings.TrimSpace(x) == "" { return nil, true }
		return []string{x}, true
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok { return out, false }
			if strings.TrimSpace(s) != "" { out = append(out, s) }
		}
		return out, true
	case []string:
		return append([]string(nil), x...), true
	default:
		return nil, false
	}
}

var (
	refPattern       = regexp.MustCompile(`(!?)\[\[([^\]]+)\]\]`)
	inlineCodePattern = regexp.MustCompile("`+[^`]*`+")
	fencePattern     = regexp.MustCompile("^ {0,3}(```+|~~~+)")
)

// scanProse calls visit for each body line that is ordinary prose, skipping
// fenced code blocks and stripping inline code spans. Markdown inside code is
// documentation about Jikko, not a reference or a heading.
func scanProse(body string, visit func(line string)) {
	open := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if open != "" {
			if strings.HasPrefix(strings.TrimSpace(line), open) { open = "" }
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil { open = m[1]; continue }
		visit(inlineCodePattern.ReplaceAllString(line, ""))
	}
}

func refsIn(body string) (links, embeds []string) {
	scanProse(body, func(line string) {
		for _, m := range refPattern.FindAllStringSubmatch(line, -1) {
			ref := strings.TrimSpace(strings.SplitN(m[2], "|", 2)[0])
			if ref == "" { continue }
			if m[1] == "!" { embeds = append(embeds, ref) } else { links = append(links, ref) }
		}
	})
	return links, embeds
}

func titleOf(body, pagePath string) string {
	title := ""
	scanProse(body, func(line string) {
		if title == "" && strings.HasPrefix(line, "# ") { title = strings.TrimSpace(strings.TrimPrefix(line, "# ")) }
	})
	if title != "" { return title }
	return stemOf(pagePath)
}

func stemOf(pagePath string) string {
	base := path.Base(pagePath)
	return strings.TrimSuffix(base, path.Ext(base))
}

// index builds the reference lookup so Resolve is constant time. Both the full
// stem and the bare basename address a page; a name matching more than one
// page is ambiguous and resolves to nothing.
func (w *Workspace) index() {
	w.byStem = make(map[string][]*Page, len(w.Pages)*2)
	for pagePath, p := range w.Pages {
		stem := strings.TrimSuffix(pagePath, ".md")
		w.byStem[stem] = append(w.byStem[stem], p)
		if base := path.Base(stem); base != stem { w.byStem[base] = append(w.byStem[base], p) }
	}
}

func (w *Workspace) Resolve(ref string) (*Page, bool) {
	ref = strings.TrimSuffix(strings.TrimSpace(filepath.ToSlash(ref)), ".md")
	if ref == "" { return nil, false }
	if matches := w.byStem[ref]; len(matches) == 1 { return matches[0], true }
	return nil, false
}

func (w *Workspace) ResolveIdentity(ref string) (*Page, bool) {
	p, ok := w.Resolve(ref)
	return p, ok && p.Kind == Identity
}

func (w *Workspace) deriveBacklinks() {
	for _, source := range w.sorted() {
		for _, ref := range append(append([]string{}, source.Links...), source.Embeds...) {
			if target, ok := w.Resolve(ref); ok { target.Backlinks = append(target.Backlinks, source.Path) }
		}
	}
	for _, p := range w.Pages {
		sort.Strings(p.Backlinks)
		p.Backlinks = dedupe(p.Backlinks)
	}
}

func dedupe(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || in[i-1] != s { out = append(out, s) }
	}
	return out
}

// sorted returns pages in deterministic path order.
func (w *Workspace) sorted() []*Page {
	out := make([]*Page, 0, len(w.Pages))
	for _, p := range w.Pages { out = append(out, p) }
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (w *Workspace) List(kind Kind, status string) []*Page {
	out := []*Page{}
	for _, p := range w.sorted() {
		if kind != "" && p.Kind != kind { continue }
		if status != "" && !matchesStatus(p.Metadata["status"], status) { continue }
		out = append(out, p)
	}
	return out
}

// matchesStatus compares against the rendered scalar so a value YAML typed as
// a number, bool, or date is still selectable from the command line.
func matchesStatus(value any, want string) bool {
	if value == nil { return false }
	if s, ok := value.(string); ok { return s == want }
	return fmt.Sprint(value) == want
}
