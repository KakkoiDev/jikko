package jikko

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, name, content string) { t.Helper(); path := filepath.Join(root, name); if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }; if err := os.WriteFile(path, []byte(content), 0644); err != nil { t.Fatal(err) } }

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
}

func TestAmbiguousReference(t *testing.T) {
	root := t.TempDir(); write(t, root, "a/design.md", "# A\n"); write(t, root, "b/design.md", "# B\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if _, ok := w.Resolve("design"); ok { t.Fatal("ambiguous bare reference resolved") }
	if p, ok := w.Resolve("a/design"); !ok || p.Path != "a/design.md" { t.Fatal("qualified reference failed") }
}

func TestViewBodyRejected(t *testing.T) {
	root := t.TempDir(); write(t, root, "bad.md", "---\ntype: view\n---\n# Not pure\n")
	if _, err := Open(root); err == nil { t.Fatal("view body should be rejected") }
}
