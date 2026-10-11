package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIgnorePatterns(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		isDir, want   bool
	}{
		{"build/", "build", true, true},
		{"build/", "build", false, false},
		{"build/", "src/build", true, true},
		{"*.log", "a/b/c.log", false, true},
		{"*.log", "a/b/c.logs", false, false},
		{"/vendor", "vendor", true, true},
		{"/vendor", "x/vendor", true, false},
		{"docs/*.md", "docs/a.md", false, true},
		{"docs/*.md", "docs/sub/a.md", false, false},
		{"docs/**", "docs/sub/a.md", false, true},
		{"**/tmp", "a/b/tmp", true, true},
		{"a/**/z", "a/z", false, true},
		{"a/**/z", "a/b/c/z", false, true},
		{"?.md", "a.md", false, true},
		{"?.md", "ab.md", false, false},
		{"[ab].md", "b.md", false, true},
		{"[!ab].md", "b.md", false, false},
		{"notes", "notes/inner/page.md", false, true},
		{"\\#literal", "#literal", false, true},
	} {
		rule, ok := parseIgnoreLine(tc.pattern)
		if !ok {
			t.Fatalf("pattern %q not parsed", tc.pattern)
		}
		got := rule.match.MatchString(tc.path) && (!rule.dirOnly || tc.isDir)
		if got != tc.want {
			t.Errorf("%q against %q (dir %v) = %v, want %v", tc.pattern, tc.path, tc.isDir, got, tc.want)
		}
	}
	for _, blank := range []string{"", "   ", "# comment", "/"} {
		if _, ok := parseIgnoreLine(blank); ok {
			t.Errorf("%q parsed as a pattern", blank)
		}
	}
}

// Ignored directories are never entered: a workspace beside a thousand
// generated files opens as fast as one without them, and node_modules is
// skipped even when nothing says so.
func TestIgnoredDirectoriesAreNotWalked(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, ".gitignore", "# build output\nbuild/\n*.tmp.md\n!keep.tmp.md\n")
	writeTest(t, d, ".jikkoignore", "vendor-docs\n")
	writeTest(t, d, "docs/.gitignore", "drafts/\n")
	writeTest(t, d, "page.md", "# Page\n\n![[pic.png]] ![[gen.png]]\n")
	writeTest(t, d, "keep.tmp.md", "# Kept\n")
	writeTest(t, d, "scratch.tmp.md", "# Scratch\n")
	writeTest(t, d, "docs/guide.md", "# Guide\n")
	writeTest(t, d, "docs/drafts/one.md", "# Draft\n")
	writeTest(t, d, "vendor-docs/readme.md", "# Vendored\n")
	writeTest(t, d, "pic.png", "x")
	writeTest(t, d, "build/gen.png", "x")
	for _, dir := range []string{"build", "node_modules/pkg"} {
		for i := 0; i < 500; i++ {
			writeTest(t, d, fmt.Sprintf("%s/file%03d.md", dir, i), "# Generated\n\n[[nowhere]]\n")
		}
	}
	start := time.Now()
	w := openTest(t, d)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("opening took %v", took)
	}
	want := []string{"docs/guide.md", "keep.tmp.md", "page.md"}
	if got := w.AccessiblePaths(""); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
	if len(w.Problems) != 0 {
		t.Fatalf("problems from ignored pages: %v", w.Problems)
	}
	if _, ok := w.ReadableFile("", "pic.png"); !ok {
		t.Fatal("asset not indexed")
	}
	if _, ok := w.ReadableFile("", "build/gen.png"); ok {
		t.Fatal("ignored asset served")
	}
	zip, err := w.ExportZIP()
	if err != nil {
		t.Fatal(err)
	}
	if len(zip) > 20<<10 {
		t.Fatalf("export is %d bytes; ignored files were included", len(zip))
	}
	for rel, want := range map[string]bool{"build/x.md": true, "node_modules/a/b.md": true, "docs/drafts/x.md": true, "docs/guide.md": false, "scratch.tmp.md": true, "keep.tmp.md": false, ".data/x": true} {
		if got := Ignored(d, rel); got != want {
			t.Errorf("Ignored(%q) = %v, want %v", rel, got, want)
		}
	}
	// A walk that never enters the ignored directories: make one unreadable
	// and the walk must still succeed.
	if os.Getuid() != 0 {
		if err := os.Chmod(filepath.Join(d, "build"), 0); err == nil {
			defer os.Chmod(filepath.Join(d, "build"), 0755)
			if _, err := Open(d); err != nil {
				t.Fatalf("walk entered an ignored directory: %v", err)
			}
		}
	}
}

func TestPagesAreNotCreatedInIgnoredPaths(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, ".gitignore", "build/\n")
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	w := openTest(t, d)
	if err := w.CreatePage("alice", "build/notes", "# Notes\n"); err == nil || !strings.Contains(err.Error(), "ignore file") {
		t.Fatalf("CreatePage into an ignored directory: %v", err)
	}
	if err := w.CreatePage("alice", "node_modules/notes", "# Notes\n"); err == nil {
		t.Fatal("CreatePage into node_modules succeeded")
	}
	if err := w.CreatePage("alice", "notes", "# Notes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Rename("alice", "notes", "build/notes", RenameOptions{}); err == nil {
		t.Fatal("Rename into an ignored directory succeeded")
	}
}
