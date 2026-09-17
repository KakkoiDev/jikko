package jikko

import (
	"errors"
	"io/fs"
	"os"
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
}

type Workspace struct {
	Root  string
	Pages map[string]*Page
}

var refRE = regexp.MustCompile(`(!?)\[\[([^\]]+)\]\]`)

func Open(root string) (*Workspace, error) {
	root, err := filepath.Abs(root)
	if err != nil { return nil, err }
	w := &Workspace{Root: root, Pages: map[string]*Page{}}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".data" { return filepath.SkipDir }
			return nil
		}
		if d.Name() == ".auth.md" { return nil }
		if strings.EqualFold(filepath.Ext(path), ".md") {
			p, err := parseFile(root, path)
			if err != nil { return err }
			w.Pages[p.Path] = p
		}
		return nil
	})
	if err != nil { return nil, err }
	w.deriveBacklinks()
	if err := w.ValidateIdentities(); err != nil { return nil, err }
	return w, nil
}

func parseFile(root, path string) (*Page, error) {
	b, err := os.ReadFile(path)
	if err != nil { return nil, err }
	meta, body, err := parseMarkdown(string(b))
	if err != nil { return nil, err }
	rel, err := filepath.Rel(root, path)
	if err != nil { return nil, err }
	rel = filepath.ToSlash(rel)
	kind := Document
	if t, ok := meta["type"].(string); ok {
		switch t { case "task": kind = Task; case "view": kind = View; case "identity": kind = Identity; case "document", "": kind = Document }
	}
	if kind == View && strings.TrimSpace(body) != "" { return nil, errors.New(rel + ": views must not contain a Markdown body") }
	p := &Page{Path: rel, Title: titleOf(body, rel), Kind: kind, Metadata: meta, Body: body}
	if kind == Identity { p.Members = stringList(meta["members"]) }
	for _, m := range refRE.FindAllStringSubmatch(body, -1) {
		ref := strings.TrimSpace(strings.SplitN(m[2], "|", 2)[0])
		if m[1] == "!" { p.Embeds = append(p.Embeds, ref) } else { p.Links = append(p.Links, ref) }
	}
	return p, nil
}

func parseMarkdown(s string) (map[string]any, string, error) {
	meta := map[string]any{}
	if !strings.HasPrefix(s, "---\n") { return meta, s, nil }
	end := strings.Index(s[4:], "\n---")
	if end < 0 { return meta, s, nil }
	end += 4
	if err := yaml.Unmarshal([]byte(s[4:end]), &meta); err != nil { return nil, "", err }
	body := strings.TrimPrefix(s[end+4:], "\n")
	return meta, body, nil
}

func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		if x == "" { return nil }; return []string{x}
	case []any:
		out := make([]string, 0, len(x)); for _, v := range x { if s, ok := v.(string); ok { out = append(out, s) } }; return out
	case []string:
		return append([]string(nil), x...)
	default:
		return nil
	}
}

func titleOf(body, path string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") { return strings.TrimSpace(strings.TrimPrefix(line, "# ")) }
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (w *Workspace) Resolve(ref string) (*Page, bool) {
	ref = strings.TrimSuffix(filepath.ToSlash(ref), ".md")
	var matches []*Page
	for path, p := range w.Pages {
		stem := strings.TrimSuffix(path, ".md")
		if stem == ref || (!strings.Contains(ref, "/") && strings.TrimSuffix(filepath.Base(path), ".md") == ref) { matches = append(matches, p) }
	}
	return firstUnique(matches)
}

func (w *Workspace) ResolveIdentity(ref string) (*Page, bool) {
	p, ok := w.Resolve(ref); return p, ok && p.Kind == Identity
}

func firstUnique(p []*Page) (*Page, bool) { if len(p) == 1 { return p[0], true }; return nil, false }

func (w *Workspace) deriveBacklinks() {
	for _, source := range w.Pages {
		refs := append(append([]string{}, source.Links...), source.Embeds...)
		for _, ref := range refs { if target, ok := w.Resolve(ref); ok { target.Backlinks = append(target.Backlinks, source.Path) } }
	}
	for _, p := range w.Pages { sort.Strings(p.Backlinks) }
}

func (w *Workspace) List(kind Kind, status string) []*Page {
	out := []*Page{}
	for _, p := range w.Pages {
		if kind != "" && p.Kind != kind { continue }
		if status != "" && p.Metadata["status"] != status { continue }
		out = append(out, p)
	}
	sort.Slice(out, func(i,j int) bool { return out[i].Path < out[j].Path })
	return out
}
