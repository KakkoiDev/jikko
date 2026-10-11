package jikko

import (
	"strings"
	"testing"
)

func TestSourceAnchorMapsRenderedSelections(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "ship.md", "# Ship it\n")
	writeTest(t, d, "doc.md", "# Plan\n\n"+
		"Ship the **first version** soon, see [[ship|the ship task]] and [[alice]] or [[nowhere]] with `some code` here.\n"+
		"A second   line ~~gone~~ *em* [a link](https://x.example) ![pic](img.png) @alice.\n\n"+
		"> quoted **text** here\n\n"+
		"- [ ] a task item with *style*\n\n"+
		"Twice here. Twice here.\n\n"+
		"Anchored <!--comment:c1-->already **bold**<!--/comment:c1--> text.\n\n<!--comment-thread:c1\n@alice: hi\n-->\n\n"+
		"```\nnot **prose** at all\n```\n")
	w := openTest(t, d)
	for _, tc := range []struct{ selected, want, err string }{
		{"the first version soon", "the **first version** soon", ""},
		{"first version", "**first version**", ""},
		{"first", "**first version**", ""},
		{"first version soon", "**first version** soon", ""},
		{"with some", "with `some code`", ""},
		{"ship task and Alice", "[[ship|the ship task]] and [[alice]]", ""},
		{"the ship", "[[ship|the ship task]]", ""},
		{"nowhere with some code here", "[[nowhere]] with `some code` here", ""},
		{"second line gone em a link pic", "second   line ~~gone~~ *em* [a link](https://x.example) ![pic](img.png)", ""},
		{"quoted text here", "quoted **text** here", ""},
		{"task item with style", "task item with *style*", ""},
		{"already bold", "already **bold**", ""},
		{"already bold text", "", "crosses the edge"},
		{"Ship the", "Ship the", ""},
		{"Twice here", "", "more than once"},
		{"not prose at all", "", "not found"},
		{"here. A second", "", "single line"},
		{"   ", "", "select some text"},
	} {
		got, err := w.SourceAnchor("", "doc", tc.selected)
		switch {
		case tc.err != "":
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("SourceAnchor(%q) = %q, %v; want error containing %q", tc.selected, got, err, tc.err)
			}
		case err != nil:
			t.Errorf("SourceAnchor(%q): %v", tc.selected, err)
		case got != tc.want:
			t.Errorf("SourceAnchor(%q) = %q, want %q", tc.selected, got, tc.want)
		}
	}
	// The mapped excerpt is one AddComment accepts.
	excerpt, err := w.SourceAnchor("alice", "doc", "the first version soon")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddComment("alice", "doc", excerpt, "Define soon."); err != nil {
		t.Fatalf("AddComment(%q): %v", excerpt, err)
	}
	if _, err := w.SourceAnchor("", "secret", "x"); err == nil {
		t.Fatal("missing page not reported")
	}
}
