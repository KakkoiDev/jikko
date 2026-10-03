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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// problem reports whether any recorded problem for a page mentions substr.
func problem(w *Workspace, pagePath, substr string) bool {
	for _, p := range w.Problems {
		if p.Path == pagePath && strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}

func TestWorkspaceSemantics(t *testing.T) {
	root := t.TempDir()
	write(t, root, "architecture.md", "---\nstatus: draft\ncustom: kept\n---\n# Architecture\n")
	write(t, root, "tasks/auth.md", "---\ntype: task\nstatus: todo\n---\n# Authentication\nSee [[architecture]].\n")
	write(t, root, "open-work.md", "---\ntype: view\nfilter:\n  type: task\n---\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["architecture.md"].Kind != Document {
		t.Fatal("status must not imply task")
	}
	if w.Pages["architecture.md"].Metadata["custom"] != "kept" {
		t.Fatal("unknown metadata lost")
	}
	if w.Pages["tasks/auth.md"].Kind != Task || w.Pages["open-work.md"].Kind != View {
		t.Fatal("explicit types not classified")
	}
	arch, ok := w.Resolve("architecture")
	if !ok {
		t.Fatal("reference not resolved")
	}
	if len(arch.Backlinks) != 1 || arch.Backlinks[0] != "tasks/auth.md" {
		t.Fatalf("backlinks = %#v", arch.Backlinks)
	}
	if got := w.List(Task, "todo"); len(got) != 1 || got[0].Path != "tasks/auth.md" {
		t.Fatalf("list = %#v", got)
	}
	if len(w.Problems) != 0 {
		t.Fatalf("clean workspace reported problems: %#v", w.Problems)
	}
}

func TestAmbiguousReference(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/design.md", "# A\n")
	write(t, root, "b/design.md", "# B\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Resolve("design"); ok {
		t.Fatal("ambiguous bare reference resolved")
	}
	if p, ok := w.Resolve("a/design"); !ok || p.Path != "a/design.md" {
		t.Fatal("qualified reference failed")
	}
}

// A view with a body is an authoring error, but reporting it must not make the
// rest of the workspace unreadable: one bad file used to fail the whole scan.
func TestViewBodyReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bad.md", "---\ntype: view\n---\n# Not pure\n")
	write(t, root, "good.md", "# Fine\n")
	w, err := Open(root)
	if err != nil {
		t.Fatalf("one malformed file must not fail the scan: %v", err)
	}
	if !problem(w, "bad.md", "views must not contain a Markdown body") {
		t.Fatalf("problems = %#v", w.Problems)
	}
	if _, ok := w.Resolve("good"); !ok {
		t.Fatal("the rest of the workspace must stay readable")
	}
}

// CRLF files must parse. Treating their frontmatter as body silently discarded
// type and, worse, access policy.
func TestCRLFFrontmatter(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\r\ntype: identity\r\n---\r\n# Alice\r\n")
	write(t, root, "secret.md", "---\r\npermissions:\r\n  read: alice\r\n---\r\n# Secret\r\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["alice.md"].Kind != Identity {
		t.Fatal("CRLF frontmatter ignored")
	}
	p := w.Pages["secret.md"]
	if !p.Restricted {
		t.Fatal("CRLF access policy ignored")
	}
	if w.Allowed("nobody", p, Read) {
		t.Fatal("CRLF file is open to everyone")
	}
	if !w.Allowed("alice", p, Read) {
		t.Fatal("CRLF grant not honoured")
	}
}

// Frontmatter ends at the first line that is exactly "---". A thematic break
// later in the body is body, and the metadata before it is untouched.
func TestFrontmatterTerminatorIsExact(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nstatus: todo\n---\n# X\n\n---\n\nMore.\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	p := w.Pages["x.md"]
	if len(w.Problems) != 0 {
		t.Fatalf("valid frontmatter reported problems: %#v", w.Problems)
	}
	if p.Kind != Task || p.Metadata["status"] != "todo" {
		t.Fatalf("metadata = %#v", p.Metadata)
	}
	if !strings.Contains(p.Body, "\n---\n") {
		t.Fatalf("thematic break lost from body: %q", p.Body)
	}
}

// A line merely starting with "---" is not a terminator. Treating it as one cut
// the frontmatter short, and because the truncated prefix still parsed, every
// property after it vanished into the body without a word.
func TestInvalidFrontmatterReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nnote: |\n  a\n----\n  b\nstatus: todo\n---\n# X\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !problem(w, "x.md", "invalid YAML frontmatter") {
		t.Fatalf("problems = %#v", w.Problems)
	}
	if w.Pages["x.md"].Metadata["note"] == "a" {
		t.Fatal("frontmatter was silently truncated at the false terminator")
	}
}

func TestUnterminatedFrontmatterReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: task\nstatus: todo\n# X\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !problem(w, "x.md", "no closing") {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

// Markdown inside a code fence documents Jikko; it does not reference anything.
func TestCodeBlocksAreNotReferences(t *testing.T) {
	root := t.TempDir()
	write(t, root, "target.md", "# Target\n")
	write(t, root, "guide.md", "# Guide\n\n```md\n# Example heading\nSee [[target]].\n```\n\nInline `[[target]]` too, but [[target]] here counts.\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	guide := w.Pages["guide.md"]
	if guide.Title != "Guide" {
		t.Fatalf("title taken from a fenced heading: %q", guide.Title)
	}
	if len(guide.Links) != 1 {
		t.Fatalf("links = %#v, want only the prose reference", guide.Links)
	}
	if got := w.Pages["target.md"].Backlinks; len(got) != 1 {
		t.Fatalf("backlinks = %#v", got)
	}
}

func TestUnknownTypeReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.md", "---\ntype: taks\n---\n# X\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["x.md"].Kind != Document {
		t.Fatal("unknown type must fall back to document")
	}
	if !problem(w, "x.md", "unknown type") {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

func TestStatusFilterMatchesTypedScalars(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "---\ntype: task\nstatus: 2\n---\n# A\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.List(Task, "2"); len(got) != 1 {
		t.Fatalf("a YAML number status is unselectable: %#v", got)
	}
}

// An access policy YAML cannot read is not the absence of a policy. A syntax
// error or a duplicate key anywhere in the frontmatter used to discard the
// whole mapping and publish the file to everyone.
func TestUnparseablePolicyDeniesEveryone(t *testing.T) {
	for name, src := range map[string]string{
		"syntax error":          "---\npermissions:\n  read: alice\nstatus: [\n---\n# S\n",
		"duplicate key":         "---\npermissions:\n  read: alice\ntitle: a\ntitle: b\n---\n# S\n",
		"duplicate policy":      "---\npermissions:\n  read: alice\npermissions:\n  read: bob\n---\n# S\n",
		"flow mapping":          "---\n{permissions: {read: alice}, status: [}\n---\n# S\n",
		"unterminated":          "---\npermissions:\n  read: alice\n# S\n",
		"quoted key syntax err": "---\n\"permissions\":\n  read: alice\n bad: [\n---\n# S\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			write(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
			write(t, d, "s.md", src)
			w, err := Open(d)
			if err != nil {
				t.Fatal(err)
			}
			p := w.Pages["s.md"]
			if !p.Restricted {
				t.Fatal("unparseable policy treated as absent")
			}
			for _, actor := range []string{"alice", "nobody", ""} {
				if w.Allowed(actor, p, Read) {
					t.Fatalf("%q may read a file whose policy cannot be evaluated", actor)
				}
			}
			if !problem(w, "s.md", "denied to everyone") {
				t.Fatalf("problems = %#v", w.Problems)
			}
		})
	}
}

// Broken frontmatter with no sign of a policy stays open: failing closed on
// every typo would hide ordinary documents for no security benefit.
func TestUnparseableFrontmatterWithoutPolicyStaysOpen(t *testing.T) {
	d := t.TempDir()
	write(t, d, "s.md", "---\nstatus: [\n---\n# S\n\nThe permissions: section is prose.\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	p := w.Pages["s.md"]
	if p.Restricted || !w.Allowed("nobody", p, Read) {
		t.Fatal("a parse error without a policy must not hide the file")
	}
	if !problem(w, "s.md", "invalid YAML") {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

// Editors on some platforms write a UTF-8 byte order mark. The frontmatter
// behind it used to be read as body, silently dropping type and access policy.
func TestByteOrderMarkFrontmatter(t *testing.T) {
	d := t.TempDir()
	write(t, d, "alice.md", "\ufeff---\ntype: identity\n---\n# Alice\n")
	write(t, d, "secret.md", "\ufeff---\r\npermissions:\r\n  read: alice\r\n---\r\n# Secret\r\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["alice.md"].Kind != Identity {
		t.Fatal("BOM hid the identity type")
	}
	p := w.Pages["secret.md"]
	if !p.Restricted || w.Allowed("nobody", p, Read) {
		t.Fatal("BOM-prefixed policy ignored; file is open to everyone")
	}
	if !w.Allowed("alice", p, Read) {
		t.Fatal("BOM-prefixed grant not honoured")
	}
	if p.Title != "Secret" || strings.Contains(p.Body, "permissions") {
		t.Fatalf("body = %q", p.Body)
	}
}

// A first line that merely starts with dashes is a thematic break, not an
// unterminated frontmatter block.
func TestLongThematicBreakIsNotFrontmatter(t *testing.T) {
	d := t.TempDir()
	write(t, d, "x.md", "-----\n# X\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Problems) != 0 {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

// Fenced code documents syntax; mentions in it address nobody, and a closing
// fence must use the same marker as the opening one.
func TestFencesAndInlineCodeHideReferences(t *testing.T) {
	d := t.TempDir()
	write(t, d, "t.md", "# T\n")
	write(t, d, "g.md", "# G\n\n~~~\n```\n[[t]]\n~~~\n\n````\n```\n[[t]]\n````\n\n``a [[t]] b``\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Pages["g.md"].Links; len(got) != 0 {
		t.Fatalf("links inside code = %#v", got)
	}
}

func TestReferenceAliasAndPathResolution(t *testing.T) {
	d := t.TempDir()
	write(t, d, "docs/design.md", "# Design\n")
	write(t, d, "a.md", "# A\n[[design|the design]] and ![[docs/design.md]] and [[ ]] and [[design]]\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	a := w.Pages["a.md"]
	if len(a.Links) != 2 || a.Links[0] != "design" || len(a.Embeds) != 1 {
		t.Fatalf("links=%#v embeds=%#v", a.Links, a.Embeds)
	}
	if got := w.Pages["docs/design.md"].Backlinks; len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("backlinks must be deduplicated: %#v", got)
	}
	for _, ref := range []string{"design", "docs/design", "docs/design.md", " design "} {
		if _, ok := w.Resolve(ref); !ok {
			t.Fatalf("Resolve(%q) failed", ref)
		}
	}
	for _, ref := range []string{"", ".md", "../design"} {
		if _, ok := w.Resolve(ref); ok {
			t.Fatalf("Resolve(%q) succeeded", ref)
		}
	}
}

func TestMalformedIdentityMembersReported(t *testing.T) {
	d := t.TempDir()
	write(t, d, "g.md", "---\ntype: identity\nmembers: {a: b}\n---\n")
	write(t, d, "h.md", "---\ntype: identity\nmembers: [alice, 3]\n---\n")
	write(t, d, "i.md", "---\ntype: identity\nmembers: [ghost]\n---\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"g.md", "h.md"} {
		if !problem(w, p, "members must name") {
			t.Fatalf("%s: problems = %#v", p, w.Problems)
		}
	}
	if !problem(w, "i.md", `unresolved identity member "ghost"`) {
		t.Fatalf("problems = %#v", w.Problems)
	}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate missed problems")
	}
}

func TestNonMappingFrontmatterReported(t *testing.T) {
	d := t.TempDir()
	write(t, d, "x.md", "---\n- a\n- b\n---\n# X\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if !problem(w, "x.md", "must be a YAML mapping") {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

// Embeds are file-oriented (specification §5.2): an asset that exists is a
// resolved reference, found by path from the root or the page's folder, or by
// a bare name that matches one file.
func TestAssetReferencesResolve(t *testing.T) {
	d := t.TempDir()
	write(t, d, "notes/page.md", "# Page\n![[diagram.png]] ![[notes/local.pdf]] ![[local.pdf]] [[media/demo.mp4]] ![[missing.png]] ![[dup.png]] [[missing-page]] ![[secret.png]]\n")
	for _, name := range []string{"assets/diagram.png", "notes/local.pdf", "media/demo.mp4", "a/dup.png", "b/dup.png", ".git/secret.png"} {
		write(t, d, name, "x")
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.UnresolvedReferences()
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, p := range got {
		msgs = append(msgs, p.Message)
	}
	want := []string{`unresolved "missing-page"`, `unresolved "missing.png"`, `ambiguous "dup.png"; give its path`, `unresolved "secret.png"`}
	if len(msgs) != len(want) {
		t.Fatalf("problems = %q, want %q", msgs, want)
	}
	for _, m := range want {
		found := false
		for _, g := range msgs {
			found = found || g == m
		}
		if !found {
			t.Fatalf("problems = %q, missing %q", msgs, m)
		}
	}
}
