package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpload(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "docs/design.md", "# Design\n\nText.")
	writeTest(t, d, "locked.md", "---\npermissions:\n  read: bob\n  admin: alice\n---\n# Locked\n")
	w := openTest(t, d)

	res, err := w.Upload("bob", "/home/bob/diagram.png", []byte("png"), UploadOptions{Into: "design"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "docs/diagram.png" || res.Embed != "diagram.png" || res.Into != "docs/design.md" || res.Size != 3 {
		t.Fatalf("result = %+v", res)
	}
	if got := readFile(t, d, "docs/design.md"); got != "# Design\n\nText.\n\n![[diagram.png]]\n" {
		t.Fatalf("design.md = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "docs/diagram.png")); string(b) != "png" {
		t.Fatal("file not written")
	}
	if unresolved, _ := w.UnresolvedReferences(); len(unresolved) != 0 {
		t.Fatalf("unresolved: %+v", unresolved)
	}
	// A second file of the same name elsewhere is embedded by its path.
	res, err = w.Upload("bob", "diagram.png", []byte("png2"), UploadOptions{Path: "other/diagram.png", Into: "design"})
	if err != nil || res.Embed != "other/diagram.png" {
		t.Fatalf("%+v %v", res, err)
	}
	// Without a page, the file goes to the root.
	if res, err := w.Upload("bob", "notes.pdf", []byte("pdf"), UploadOptions{}); err != nil || res.Path != "notes.pdf" || res.Into != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUploadRefusals(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [bob]\n---\n")
	writeTest(t, d, "locked.md", "---\npermissions:\n  read: bob\n  admin: alice\n---\n# Locked\n")
	writeTest(t, d, "hidden.md", "---\npermissions:\n  admin: alice\n---\n# Hidden\n")
	writeTest(t, d, "board.md", "---\ntype: view\n---\n")
	if err := os.WriteFile(filepath.Join(d, "taken.png"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, d)
	w := openTest(t, d)
	for _, tc := range []struct {
		actor, name string
		size        int
		opt         UploadOptions
		want        string
	}{
		{"", "a.png", 1, UploadOptions{}, "authentication"},
		{"crew", "a.png", 1, UploadOptions{}, "authentication"},
		{"bob", "big.png", 11, UploadOptions{MaxBytes: 10}, "maximum upload size is 10"},
		{"bob", "a.png", 1, UploadOptions{Into: "locked"}, "lacks write"},
		{"bob", "a.png", 1, UploadOptions{Into: "hidden"}, "not permitted"},
		{"bob", "a.png", 1, UploadOptions{Into: "board"}, "views"},
		{"bob", "taken.png", 1, UploadOptions{}, "already exists"},
		{"bob", "page.md", 1, UploadOptions{}, "jikko create"},
		{"bob", "x", 1, UploadOptions{Path: "../escape.png"}, "inside the workspace"},
		{"bob", "x", 1, UploadOptions{Path: "/abs.png"}, "inside the workspace"},
		{"bob", "x", 1, UploadOptions{Path: ".git/config"}, "reserved"},
		{"bob", "x", 1, UploadOptions{Path: ".env"}, "hidden"},
		{"bob", "x", 1, UploadOptions{Path: "a|b.png"}, "cannot be referenced"},
	} {
		_, err := w.Upload(tc.actor, tc.name, make([]byte, tc.size), tc.opt)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc, err, tc.want)
		}
	}
	sameFiles(t, before, snapshotFiles(t, d))
}
