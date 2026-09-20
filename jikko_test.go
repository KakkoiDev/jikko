package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte(content), 0644); err != nil { t.Fatal(err) }
}

// problem reports whether any recorded problem for a page mentions substr.
func problem(w *Workspace, pagePath, substr string) bool {
	for _, p := range w.Problems {
		if p.Path == pagePath && strings.Contains(p.Message, substr) { return true }
	}
	return false
}

func TestWorkspaceSemantics(t *testing.T) {
	root := t.TempDir()
	write(t, root, "architecture.md", "---\nstatus: draft\ncustom: kept\n---\n# Architecture\n")
	write(t, root, "tasks/auth.md", "---\ntype: task\nstatus: todo\n---\n# Authentication\nSee [[architecture]].\n")
	write(t, root, "open-work.md", "---\ntype: view\nfilter:\n  type: task\n---\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if w.Pages["architecture.md"].Kind != Document { t.Fatal("status must not imply task") }
	if w.Pages["architecture.md"].Metadata["custom"] != "kept" { t.Fatal("unknown metadata lost") }
	if w.Pages["tasks/auth.md"].Kind != Task || w.Pages["open-work.md"].Kind != View { t.Fatal("explicit types not classified") }
	arch, ok := w.Resolve("architecture"); if !ok { t.Fatal("reference not resolved") }
	if len(arch.Backlinks) != 1 || arch.Backlinks[0] != "tasks/auth.md" { t.Fatalf("backlinks = %#v", arch.Backlinks) }
	if got := w.List(Task, "todo"); len(got) != 1 || got[0].Path != "tasks/auth.md" { t.Fatalf("list = %#v", got) }
	if len(w.Problems) != 0 { t.Fatalf("clean workspace reported problems: %#v", w.Problems) }
}

func TestAmbiguousReference(t *testing.T) {
	root := t.TempDir(); write(t, root, "a/design.md", "# A\n"); write(t, root, "b/design.md", "# B\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if _, ok := w.Resolve("design"); ok { t.Fatal("ambiguous bare reference resolved") }
	if p, ok := w.Resolve("a/design"); !ok || p.Path != "a/design.md" { t.Fatal("qualified reference failed") }
}

// A view with a body is an authoring error, but reporting it must not make the
// rest of the workspace unreadable: one bad file used to fail the whole scan.
func TestViewBodyReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bad.md", "---\ntype: view\n---\n# Not pure\n")
	write(t, root, "good.md", "# Fine\n")
	w, err := Open(root)
	if err != nil { t.Fatalf("one malformed file must not fail the scan: %v", err) }
	if !problem(w, "bad.md", "views must not contain a Markdown body") { t.Fatalf("problems = %#v", w.Problems) }
	if _, ok := w.Resolve("good"); !ok { t.Fatal("the rest of the workspace must stay readable") }
}

// CRLF files must parse. Treating their frontmatter as body silently discarded
// type and, worse, access policy.
func TestCRLFFrontmatter(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\r\ntype: identity\r\n---\r\n# Alice\r\n")
	write(t, root, "secret.md", "---\r\npermissions:\r\n  read: alice\r\n---\r\n# Secret\r\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if w.Pages["alice.md"].Kind != Identity { t.Fatal("CRLF frontmatter ignored") }
	p := w.Pages["secret.md"]
	if !p.Restricted { t.Fatal("CRLF access policy ignored") }
	if w.Allowed("nobody", p, Read) { t.Fatal("CRLF file is open to everyone") }
	if !w.Allowed("alice", p, Read) { t.Fatal("CRLF grant not honoured") }
}

// Frontmatter ends at the first line that is exactly "---". A thematic break
// later in the body is body, and the metadata before it is untouched.
func TestFrontmatterTerminatorIsExact(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nstatus: todo\n---\n# X\n\n---\n\nMore.\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	p := w.Pages["x.md"]
	if len(w.Problems) != 0 { t.Fatalf("valid frontmatter reported problems: %#v", w.Problems) }
	if p.Kind != Task || p.Metadata["status"] != "todo" { t.Fatalf("metadata = %#v", p.Metadata) }
	if !strings.Contains(p.Body, "\n---\n") { t.Fatalf("thematic break lost from body: %q", p.Body) }
}

// A line merely starting with "---" is not a terminator. Treating it as one cut
// the frontmatter short, and because the truncated prefix still parsed, every
// property after it vanished into the body without a word.
func TestInvalidFrontmatterReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nnote: |\n  a\n----\n  b\nstatus: todo\n---\n# X\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if !problem(w, "x.md", "invalid YAML frontmatter") { t.Fatalf("problems = %#v", w.Problems) }
	if w.Pages["x.md"].Metadata["note"] == "a" { t.Fatal("frontmatter was silently truncated at the false terminator") }
}

func TestUnterminatedFrontmatterReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nstatus: todo\n# X\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if !problem(w, "x.md", "no closing") { t.Fatalf("problems = %#v", w.Problems) }
}

// Markdown inside a code fence documents Jikko; it does not reference anything.
func TestCodeBlocksAreNotReferences(t *testing.T) {
	root := t.TempDir()
	write(t, root, "target.md", "# Target\n")
	write(t, root, "guide.md", "# Guide\n\n```md\n# Example heading\nSee [[target]].\n```\n\nInline `[[target]]` too, but [[target]] here counts.\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	guide := w.Pages["guide.md"]
	if guide.Title != "Guide" { t.Fatalf("title taken from a fenced heading: %q", guide.Title) }
	if len(guide.Links) != 1 { t.Fatalf("links = %#v, want only the prose reference", guide.Links) }
	if got := w.Pages["target.md"].Backlinks; len(got) != 1 { t.Fatalf("backlinks = %#v", got) }
}

func TestUnknownTypeReported(t *testing.T) {
	root := t.TempDir(); write(t, root, "x.md", "---\ntype: taks\n---\n# X\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if w.Pages["x.md"].Kind != Document { t.Fatal("unknown type must fall back to document") }
	if !problem(w, "x.md", "unknown type") { t.Fatalf("problems = %#v", w.Problems) }
}

func TestStatusFilterMatchesTypedScalars(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "---\ntype: task\nstatus: 2\n---\n# A\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if got := w.List(Task, "2"); len(got) != 1 { t.Fatalf("a YAML number status is unselectable: %#v", got) }
}
