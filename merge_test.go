package jikko

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeLines(t *testing.T) {
	base := "a\nb\nc\nd\ne\nf\n"
	for _, tc := range []struct {
		name, current, yours, want string
		conflicts                  int
	}{
		{"separate edits", "a\nB\nc\nd\ne\nf\n", "a\nb\nc\nd\nE\nf\n", "a\nB\nc\nd\nE\nf\n", 0},
		{"same edit twice", "a\nB\nc\nd\ne\nf\n", "a\nB\nc\nd\ne\nf\n", "a\nB\nc\nd\ne\nf\n", 0},
		{"only yours", base, "x\na\nb\nc\nd\ne\nf\ny\n", "x\na\nb\nc\nd\ne\nf\ny\n", 0},
		{"only current", "a\nc\nd\ne\nf\n", base, "a\nc\nd\ne\nf\n", 0},
		{"insertions apart", "a\nb\nNEW1\nc\nd\ne\nf\n", "a\nb\nc\nd\ne\nNEW2\nf\n", "a\nb\nNEW1\nc\nd\ne\nNEW2\nf\n", 0},
		{"delete and edit apart", "a\nc\nd\ne\nf\n", "a\nb\nc\nd\ne\nF\n", "a\nc\nd\ne\nF\n", 0},
		{"same line differently", "a\nB1\nc\nd\ne\nf\n", "a\nB2\nc\nd\ne\nf\n", "", 1},
		{"adjacent lines", "a\nB\nc\nd\ne\nf\n", "a\nb\nC\nd\ne\nf\n", "", 1},
		{"delete against edit", "a\nc\nd\ne\nf\n", "a\nB\nc\nd\ne\nf\n", "", 1},
		{"insert at same place", "a\nb\nX\nc\nd\ne\nf\n", "a\nb\nY\nc\nd\ne\nf\n", "", 1},
		{"two conflicts", "A1\nb\nc\nd\ne\nF1\n", "A2\nb\nc\nd\ne\nF2\n", "", 2},
		{"final newline removed", "a\nb\nc\nd\ne\nf", "a\nB\nc\nd\ne\nf\n", "a\nB\nc\nd\ne\nf", 0},
		{"final newline against last line", "a\nb\nc\nd\ne\nf", "a\nb\nc\nd\ne\nF\n", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, conflicts := mergeLines(base, tc.current, tc.yours, nil)
			if len(conflicts) != tc.conflicts {
				t.Fatalf("conflicts = %+v", conflicts)
			}
			if tc.conflicts == 0 && got != tc.want {
				t.Fatalf("merged = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeLinesConflictShape(t *testing.T) {
	_, conflicts := mergeLines("one\ntwo\nthree\n", "one\nTWO (current)\nthree\n", "one\nTWO (yours)\nthree\n", nil)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	c := conflicts[0]
	if c.ID != "body:0" || c.Line != 2 || c.Base != "two\n" || c.Current != "TWO (current)\n" || c.Yours != "TWO (yours)\n" {
		t.Fatalf("conflict = %+v", c)
	}
}

// Edits made by each side to separate regions, kept apart by at least one
// untouched line, always merge cleanly into the text with both edits.
func TestMergeLinesProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 300; round++ {
		n := 6 + rng.Intn(40)
		base := make([]string, n)
		for i := range base {
			base[i] = fmt.Sprintf("line %d\n", i)
		}
		split := 2 + rng.Intn(n-4)
		current := editRegion(rng, base, 0, split-1, "c")
		yours := editRegion(rng, base, split+1, n, "y")
		// The expected text applies both region edits to the base.
		want := append(append(append([]string{}, current[:len(current)-(n-(split-1))]...), base[split-1:split+1]...), yours[split+1:]...)
		got, conflicts := mergeLines(join(base), join(current), join(yours), nil)
		if len(conflicts) != 0 || got != join(want) {
			t.Fatalf("round %d: conflicts %+v\nbase %q\ncurrent %q\nyours %q\ngot %q\nwant %q", round, conflicts, join(base), join(current), join(yours), got, join(want))
		}
	}
}

// editRegion returns base with lines [from, to) replaced, deleted, or
// extended at random. Lines outside the region are untouched.
func editRegion(rng *rand.Rand, base []string, from, to int, tag string) []string {
	out := append([]string{}, base[:from]...)
	for i := from; i < to; i++ {
		switch rng.Intn(4) {
		case 0:
			out = append(out, base[i])
		case 1:
			out = append(out, fmt.Sprintf("%s edit %d\n", tag, i))
		case 2: // deleted
		case 3:
			out = append(out, base[i], fmt.Sprintf("%s insert %d\n", tag, i))
		}
	}
	return append(out, base[to:]...)
}

func TestMergeFrontmatterPerKey(t *testing.T) {
	base := "---\ntype: task\n# the status\nstatus: todo\ndue: 2026-09-20\ntags: [a]\n---\n# Task\n\nBody.\n"
	t.Run("different keys", func(t *testing.T) {
		current := "---\ntype: task\n# the status\nstatus: doing\ndue: 2026-09-20\ntags: [a]\n---\n# Task\n\nBody.\n"
		yours := "---\ntype: task\n# the status\nstatus: todo\ndue: 2026-10-01\ntags: [a]\nassignee: alice\n---\n# Task\n\nBody, edited.\n"
		merged, conflict, err := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if err != nil || conflict != nil {
			t.Fatalf("%v %+v", err, conflict)
		}
		want := "---\ntype: task\n# the status\nstatus: doing\ndue: 2026-10-01\ntags: [a]\nassignee: alice\n---\n# Task\n\nBody, edited.\n"
		if string(merged) != want {
			t.Fatalf("merged =\n%s", merged)
		}
	})
	t.Run("current frontmatter kept byte for byte", func(t *testing.T) {
		current := "---\ntype:    task   # odd spacing\n# the status\nstatus: todo\ndue: 2026-09-20\ntags: [a]\n---\n# Task\n\nBody.\n"
		yours := base[:strings.Index(base, "Body.")] + "Body, edited.\n"
		merged, conflict, err := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if err != nil || conflict != nil {
			t.Fatalf("%v %+v", err, conflict)
		}
		if !strings.HasPrefix(string(merged), "---\ntype:    task   # odd spacing\n") || !strings.HasSuffix(string(merged), "Body, edited.\n") {
			t.Fatalf("merged =\n%s", merged)
		}
	})
	t.Run("same key differently", func(t *testing.T) {
		current := strings.Replace(base, "status: todo", "status: doing", 1)
		yours := strings.Replace(strings.Replace(base, "status: todo", "status: done", 1), "tags: [a]", "tags: [a, b]", 1)
		_, conflict, err := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if err != nil || conflict == nil || len(conflict.Fields) != 1 || len(conflict.Body) != 0 {
			t.Fatalf("%v %+v", err, conflict)
		}
		f := conflict.Fields[0]
		if f.Key != "status" || *f.Base != "todo" || *f.Current != "doing" || *f.Yours != "done" {
			t.Fatalf("field = %+v", f)
		}
	})
	t.Run("deleted against changed", func(t *testing.T) {
		current := strings.Replace(base, "due: 2026-09-20\n", "", 1)
		yours := strings.Replace(base, "due: 2026-09-20", "due: 2026-12-01", 1)
		_, conflict, _ := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if conflict == nil || len(conflict.Fields) != 1 || conflict.Fields[0].Current != nil || *conflict.Fields[0].Yours != "2026-12-01" {
			t.Fatalf("conflict = %+v", conflict)
		}
	})
	t.Run("deleted by yours", func(t *testing.T) {
		current := strings.Replace(base, "Body.", "Body!", 1)
		yours := strings.Replace(base, "tags: [a]\n", "", 1)
		merged, conflict, err := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if err != nil || conflict != nil || strings.Contains(string(merged), "tags") || !strings.Contains(string(merged), "Body!") {
			t.Fatalf("%v %+v\n%s", err, conflict, merged)
		}
	})
	t.Run("added identically", func(t *testing.T) {
		current := strings.Replace(base, "tags: [a]", "tags: [a]\nowner: bob", 1)
		yours := current
		merged, conflict, err := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if err != nil || conflict != nil || string(merged) != current {
			t.Fatalf("%v %+v", err, conflict)
		}
	})
	t.Run("added differently", func(t *testing.T) {
		current := strings.Replace(base, "tags: [a]", "tags: [a]\nowner: bob", 1)
		yours := strings.Replace(base, "tags: [a]", "tags: [a]\nowner: carol", 1)
		_, conflict, _ := merge3([]byte(base), []byte(current), []byte(yours), nil)
		if conflict == nil || len(conflict.Fields) != 1 || conflict.Fields[0].Base != nil {
			t.Fatalf("conflict = %+v", conflict)
		}
	})
	t.Run("every key removed", func(t *testing.T) {
		b := "---\nstatus: todo\n---\nBody\n"
		merged, conflict, err := merge3([]byte(b), []byte("---\nstatus: todo\n---\nBody\nMore\n"), []byte("Body\n"), nil)
		if err != nil || conflict != nil || string(merged) != "Body\nMore\n" {
			t.Fatalf("%v %+v %q", err, conflict, merged)
		}
	})
	t.Run("unreadable frontmatter merges as text", func(t *testing.T) {
		b := "---\nstatus: [todo\n---\nline 1\nline 2\nline 3\n"
		merged, conflict, err := merge3([]byte(b), []byte("---\nstatus: [todo\n---\nline one\nline 2\nline 3\n"), []byte("---\nstatus: [todo\n---\nline 1\nline 2\nline three\n"), nil)
		if err != nil || conflict != nil || string(merged) != "---\nstatus: [todo\n---\nline one\nline 2\nline three\n" {
			t.Fatalf("%v %+v %q", err, conflict, merged)
		}
	})
}

func saveWorkspace(t *testing.T) (string, *Workspace) {
	t.Helper()
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "carol.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "plan.md", "---\nstatus: todo\npermissions:\n  read: carol\n  write: bob\n  admin: alice\n---\n# Plan\n\nFirst.\n\nSecond.\n")
	return d, openTest(t, d)
}

func TestSaveSource(t *testing.T) {
	d, w := saveWorkspace(t)
	base, page, err := w.Source("bob", "plan")
	if err != nil {
		t.Fatal(err)
	}
	baseRev := page.Rev
	yours := strings.Replace(string(base), "First.", "First, by bob.", 1)
	res, err := w.SaveSource("bob", "plan", baseRev, []byte(yours), SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Merged || res.Rev == baseRev || readFile(t, d, "plan.md") != yours {
		t.Fatalf("result %+v, file %q", res, readFile(t, d, "plan.md"))
	}
	// Saving the same edit again against the old revision is a no-op merge.
	if res2, err := w.SaveSource("bob", "plan", baseRev, []byte(yours), SaveOptions{}); err != nil || res2.Rev != res.Rev {
		t.Fatalf("repeat: %+v %v", res2, err)
	}
}

func TestSaveSourceMergesConcurrentEdit(t *testing.T) {
	d, w := saveWorkspace(t)
	base, page, _ := w.Source("bob", "plan")
	// Someone else edits the second paragraph and the status meanwhile.
	theirs := strings.Replace(strings.Replace(string(base), "Second.", "Second, by alice.", 1), "status: todo", "status: doing", 1)
	writeTest(t, d, "plan.md", theirs)
	yours := strings.Replace(string(base), "First.", "First, by bob.", 1)

	// Without the base, nothing can tell whose change is whose.
	_, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Conflict.BaseKnown || len(ce.Conflict.Body) == 0 || len(ce.Conflict.Fields) != 1 {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, d, "plan.md") != theirs {
		t.Fatal("a conflict wrote the file")
	}
	// A wrong base is not trusted.
	if _, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: []byte(theirs)}); !errors.As(err, &ce) || ce.Conflict.BaseKnown {
		t.Fatalf("forged base accepted: %v", err)
	}
	// With the base as read -- even with a form's CRLF line breaks -- the
	// edits merge.
	res, err := w.SaveSource("bob", "plan", page.Rev, []byte(strings.ReplaceAll(yours, "\n", "\r\n")), SaveOptions{Base: []byte(strings.ReplaceAll(string(base), "\n", "\r\n"))})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(theirs, "First.", "First, by bob.", 1)
	if !res.Merged || readFile(t, d, "plan.md") != want {
		t.Fatalf("merged %+v:\n%s", res, readFile(t, d, "plan.md"))
	}
}

func TestSaveSourceReturnsStructuredConflict(t *testing.T) {
	d, w := saveWorkspace(t)
	base, page, _ := w.Source("bob", "plan")
	theirs := strings.Replace(strings.Replace(string(base), "First.", "First, by alice.", 1), "status: todo", "status: doing", 1)
	writeTest(t, d, "plan.md", theirs)
	yours := strings.Replace(strings.Replace(string(base), "First.", "First, by bob.", 1), "status: todo", "status: done", 1)
	_, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: base})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	c := ce.Conflict
	if c.Path != "plan.md" || !c.BaseKnown || c.BaseRev != page.Rev || c.CurrentRev != fingerprint([]byte(theirs)) {
		t.Fatalf("conflict = %+v", c)
	}
	if len(c.Fields) != 1 || c.Fields[0].Key != "status" || *c.Fields[0].Current != "doing" || *c.Fields[0].Yours != "done" {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if len(c.Body) != 1 || c.Body[0].Line != 3 || c.Body[0].Current != "First, by alice.\n" || c.Body[0].Yours != "First, by bob.\n" || c.Body[0].Base != "First.\n" {
		t.Fatalf("body = %+v", c.Body)
	}
	if readFile(t, d, "plan.md") != theirs {
		t.Fatal("a conflict wrote the file")
	}
}

func TestSaveSourceFindsBaseInGitHistory(t *testing.T) {
	d := gitWorkspace(t)
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "notes/doc.md", "# Doc\n\none\n\ntwo\n")
	gitOutput(t, d, "add", ".")
	gitOutput(t, d, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "base")
	w := openTest(t, d)
	base, page, err := w.Source("alice", "doc")
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, d, "notes/doc.md", "# Doc\n\none\n\ntwo, changed\n")
	gitOutput(t, d, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "theirs")
	writeTest(t, d, "notes/doc.md", "# Doc\n\none\n\ntwo, changed\n\nthree\n")
	yours := strings.Replace(string(base), "one", "ONE", 1)
	res, err := w.SaveSource("alice", "doc", page.Rev, []byte(yours), SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Merged || readFile(t, d, "notes/doc.md") != "# Doc\n\nONE\n\ntwo, changed\n\nthree\n" {
		t.Fatalf("%+v %q", res, readFile(t, d, "notes/doc.md"))
	}
}

func TestSaveSourceAuthorization(t *testing.T) {
	d, w := saveWorkspace(t)
	base, page, _ := w.Source("alice", "plan")
	before := snapshotFiles(t, d)
	edit := strings.Replace(string(base), "First.", "Changed.", 1)
	if _, err := w.SaveSource("carol", "plan", page.Rev, []byte(edit), SaveOptions{}); err == nil || !strings.Contains(err.Error(), "lacks write") {
		t.Fatalf("reader saved: %v", err)
	}
	if _, err := w.SaveSource("mallory", "plan", page.Rev, []byte(edit), SaveOptions{}); err == nil {
		t.Fatal("unknown actor saved")
	}
	if _, err := w.SaveSource("", "plan", page.Rev, []byte(edit), SaveOptions{}); err == nil {
		t.Fatal("anonymous caller saved")
	}
	if _, err := w.SaveSource("bob", "plan", "", []byte(edit), SaveOptions{}); err == nil || !strings.Contains(err.Error(), "base revision") {
		t.Fatalf("save without a base revision: %v", err)
	}
	// write does not include changing the access policy.
	widened := strings.Replace(string(base), "read: carol", "read: [carol, bob]", 1)
	if _, err := w.SaveSource("bob", "plan", page.Rev, []byte(widened), SaveOptions{}); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatalf("writer changed the policy: %v", err)
	}
	removed := strings.Replace(string(base), "permissions:\n  read: carol\n  write: bob\n  admin: alice\n", "", 1)
	if _, err := w.SaveSource("bob", "plan", page.Rev, []byte(removed), SaveOptions{}); err == nil {
		t.Fatal("writer removed the policy")
	}
	// An edit that orphans nothing but breaks the workspace is refused.
	broken := strings.Replace(string(base), "First.", "<!--comment-thread:c9\n@bob: orphan\n-->", 1)
	if _, err := w.SaveSource("bob", "plan", page.Rev, []byte(broken), SaveOptions{}); err == nil || !strings.Contains(err.Error(), "workspace problem") {
		t.Fatalf("broken edit saved: %v", err)
	}
	sameFiles(t, before, snapshotFiles(t, d))
	writeTest(t, d, "hidden.md", "---\npermissions:\n  admin: alice\n---\nHidden\n")
	w = openTest(t, d)
	if _, err := w.SaveSource("bob", "hidden", "x", []byte("x"), SaveOptions{}); err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("unreadable page: %v", err)
	}
	if _, _, err := w.Source("bob", "hidden"); err == nil {
		t.Fatal("unreadable source disclosed")
	}
	// An administrator may change the policy.
	if _, err := w.SaveSource("alice", "plan", page.Rev, []byte(widened), SaveOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveSourceKeepsLineEndings(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "crlf.md", "---\r\nstatus: todo\r\n---\r\nOne\r\nTwo\r\n")
	w := openTest(t, d)
	_, page, _ := w.Source("alice", "crlf")
	if _, err := w.SaveSource("alice", "crlf", page.Rev, []byte("---\nstatus: todo\n---\nOne\nThree\n"), SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, "crlf.md"))
	if string(b) != "---\r\nstatus: todo\r\n---\r\nOne\r\nThree\r\n" {
		t.Fatalf("crlf.md = %q", b)
	}
}

func TestSaveSourceResolvesConflicts(t *testing.T) {
	d, w := saveWorkspace(t)
	base, page, _ := w.Source("bob", "plan")
	theirs := strings.Replace(strings.Replace(strings.Replace(string(base), "First.", "First, by alice.", 1), "Second.", "Second, by alice.", 1), "status: todo", "status: doing", 1)
	writeTest(t, d, "plan.md", theirs)
	yours := strings.Replace(strings.Replace(strings.Replace(string(base), "First.", "First, by bob.", 1), "Second.", "Second, by bob.", 1), "status: todo", "status: done", 1)
	_, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: base})
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Conflict.Body) != 2 || ce.Conflict.Body[1].ID != "body:1" || ce.Conflict.Fields[0].ID != "field:status" {
		t.Fatalf("err = %v %+v", err, ce)
	}
	choices := map[string]string{"body:0": TakeYours, "body:1": TakeCurrent, "field:status": TakeYours}
	// Resolutions for an older state of the page are not applied.
	if _, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: base, Resolve: choices, ResolveRev: page.Rev}); !errors.As(err, &ce) {
		t.Fatalf("stale resolutions applied: %v", err)
	}
	// A partial resolution leaves the rest in conflict.
	_, err = w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: base, Resolve: map[string]string{"body:0": TakeYours}, ResolveRev: ce.Conflict.CurrentRev})
	if !errors.As(err, &ce) || len(ce.Conflict.Body) != 1 || ce.Conflict.Body[0].ID != "body:1" || len(ce.Conflict.Fields) != 1 {
		t.Fatalf("partial resolution: %v", err)
	}
	res, err := w.SaveSource("bob", "plan", page.Rev, []byte(yours), SaveOptions{Base: base, Resolve: choices, ResolveRev: ce.Conflict.CurrentRev})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(theirs, "First, by alice.", "First, by bob.", 1), "status: doing", "status: done", 1)
	if !res.Merged || readFile(t, d, "plan.md") != want {
		t.Fatalf("resolved:\n%s", readFile(t, d, "plan.md"))
	}
}
