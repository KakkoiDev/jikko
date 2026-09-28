package jikko

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestPage(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil { t.Fatal(err) }
	if err := os.WriteFile(p, []byte(body), 0644); err != nil { t.Fatal(err) }
}

func TestTreeFiltersForbiddenPages(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "malrec.md", "---\ntype: identity\n---\n# Malrec\n")
	writeTestPage(t, root, "public.md", "# Public\n")
	writeTestPage(t, root, "secret.md", "---\npermissions:\n  read: malrec\n  admin: malrec\n---\n# Secret\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	got := w.AccessiblePaths("cassian")
	for _, p := range got {
		if p == "secret.md" { t.Fatal("tree leaked forbidden page") }
	}
	if len(got) != 3 { t.Fatalf("got paths %v", got) }
}

func TestReadManyPreservesOrderAndPermissions(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "a.md", "# A\n")
	writeTestPage(t, root, "b.md", "# B\n")
	w, _ := Open(root)
	got, err := w.ReadMany("cassian", "b", "a")
	if err != nil { t.Fatal(err) }
	if got[0].Path != "b.md" || got[1].Path != "a.md" { t.Fatalf("wrong order: %v %v", got[0].Path, got[1].Path) }
}

func TestMentionsIncludesTransitiveGroup(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "north.md", "---\ntype: identity\nmembers: [cassian]\n---\n# North\n")
	writeTestPage(t, root, "alliance.md", "---\ntype: identity\nmembers: [north]\n---\n# Alliance\n")
	writeTestPage(t, root, "message.md", "# Message\n@alliance defend Sol.\n")
	writeTestPage(t, root, "other.md", "# Other\n@malrec hello.\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	got := w.Mentions("cassian")
	found := false
	for _, p := range got {\n\t\tif p.Path == "message.md" {\n\t\t\tfound = true\n\t\t}\n\t}
	if !found { t.Fatalf("transitive group mention missing: %#v", got) }
}

func TestCreatePageValidatesAndRefreshesWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	w, _ := Open(root)
	if err := w.CreatePage("cassian", "memory/talos", "# Talos\nNever forget.\n"); err != nil { t.Fatal(err) }
	p, ok := w.Resolve("memory/talos")
	if !ok || p.Title != "Talos" { t.Fatalf("created page not indexed: %#v %v", p, ok) }
	if err := w.CreatePage("cassian", "../escape.md", "# no\n"); err == nil { t.Fatal("path traversal accepted") }
}
