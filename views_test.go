package jikko

import (
	"strings"
	"testing"
)

func viewWorkspace(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "people/carol.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "board.md", "---\ntype: view\nfilter:\n  type: task\n  tags: [architecture]\n  status: [todo, doing]\nview:\n  layout: board\n  group: status\n  sort: due\n---\n")
	writeTest(t, d, "t1.md", "---\ntype: task\nstatus: doing\ndue: 2026-09-20\ntags: [architecture, auth]\nassignee: carol\n---\n# Sessions\n")
	writeTest(t, d, "t2.md", "---\ntype: task\nstatus: todo\ndue: 2026-09-18\ntags: [architecture]\n---\n# Storage\n")
	writeTest(t, d, "t3.md", "---\ntype: task\nstatus: todo\ntags: architecture\n---\n# Undated\n")
	writeTest(t, d, "t4.md", "---\ntype: task\nstatus: done\ntags: [architecture]\n---\n# Finished\n")
	writeTest(t, d, "t5.md", "---\ntype: task\nstatus: todo\ntags: [ops]\n---\n# Elsewhere\n")
	writeTest(t, d, "t6.md", "---\ntype: task\nstatus: todo\ndue: 2026-09-01\ntags: [architecture]\npermissions:\n  read: alice\n  admin: alice\n---\n# Private\n")
	writeTest(t, d, "doc.md", "---\nstatus: todo\ntags: [architecture]\n---\n# A document\n")
	return d
}

func paths(items []ViewItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return strings.Join(out, " ")
}

// The specification's own example: tasks tagged architecture that are todo
// or doing, on a board grouped by status and sorted by due date.
func TestEvaluateSpecificationView(t *testing.T) {
	w := openTest(t, viewWorkspace(t))
	if len(w.Problems) != 0 {
		t.Fatalf("problems: %+v", w.Problems)
	}
	res, err := w.EvaluateView("alice", "board")
	if err != nil {
		t.Fatal(err)
	}
	if res.Spec.Layout != "board" || res.Spec.Group != "status" || len(res.Spec.Sort) != 1 {
		t.Fatalf("spec = %+v", res.Spec)
	}
	if got := paths(res.Pages); got != "t6.md t2.md t1.md t3.md" {
		t.Fatalf("pages = %s", got)
	}
	if len(res.Groups) != 2 || res.Groups[0].Key != "todo" || res.Groups[1].Key != "doing" {
		t.Fatalf("groups = %+v", res.Groups)
	}
	if got := paths(res.Groups[0].Pages); got != "t6.md t2.md t3.md" {
		t.Fatalf("todo = %s", got)
	}
	// Bob cannot read t6.md, so the View never shows it to him.
	res, err = w.EvaluateView("bob", "board")
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(res.Pages); got != "t2.md t1.md t3.md" {
		t.Fatalf("bob sees %s", got)
	}
	// Anonymous callers see open pages only.
	res, err = w.EvaluateView("", "board")
	if err != nil || strings.Contains(paths(res.Pages), "t6.md") {
		t.Fatalf("anonymous: %v %v", res, err)
	}
}

func TestViewSortGroupAndMatching(t *testing.T) {
	d := viewWorkspace(t)
	writeTest(t, d, "by-tag.md", "---\ntype: view\nfilter:\n  type: task\nview:\n  group: tags\n  sort: [-due, title]\n---\n")
	writeTest(t, d, "mine.md", "---\ntype: view\nfilter:\n  assignee: people/carol\n---\n")
	writeTest(t, d, "dated.md", "---\ntype: view\nfilter:\n  due: 2026-09-18\n---\n")
	writeTest(t, d, "everything.md", "---\ntype: view\n---\n")
	w := openTest(t, d)

	res, err := w.EvaluateView("bob", "by-tag")
	if err != nil {
		t.Fatal(err)
	}
	// Descending due, undated last, then by title.
	if got := paths(res.Pages); got != "t1.md t2.md t5.md t4.md t3.md" {
		t.Fatalf("pages = %s", got)
	}
	var keys []string
	for _, g := range res.Groups {
		keys = append(keys, g.Key+"="+paths(g.Pages))
	}
	if got := strings.Join(keys, "; "); got != "architecture=t1.md t2.md t4.md t3.md; auth=t1.md; ops=t5.md" {
		t.Fatalf("groups = %s", got)
	}

	res, err = w.EvaluateView("bob", "mine")
	if err != nil || paths(res.Pages) != "t1.md" {
		t.Fatalf("assignee by identity: %v %v", res, err)
	}
	res, err = w.EvaluateView("bob", "dated")
	if err != nil || paths(res.Pages) != "t2.md" {
		t.Fatalf("date filter: %v %v", res, err)
	}
	res, err = w.EvaluateView("bob", "everything")
	if err != nil || strings.Contains(paths(res.Pages), "everything.md") || !strings.Contains(paths(res.Pages), "doc.md") {
		t.Fatalf("unfiltered view: %v %v", res, err)
	}
}

func TestViewDefinitionsAreValidated(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "a.md", "---\ntype: view\nfilter: tasks\n---\n")
	writeTest(t, d, "b.md", "---\ntype: view\nfilter:\n  status:\n    nested: value\n---\n")
	writeTest(t, d, "c.md", "---\ntype: view\nfilter:\n  type: [task, project]\n---\n")
	writeTest(t, d, "e.md", "---\ntype: view\nview: board\n---\n")
	writeTest(t, d, "f.md", "---\ntype: view\nview:\n  group: [a, b]\n  sort: {due: asc}\n---\n")
	writeTest(t, d, "g.md", "---\ntype: view\nfilter:\n  tags: []\n---\n")
	writeTest(t, d, "ok.md", "---\ntype: view\nfilter:\n  type: task\nview:\n  layout: calendar\n  unknown-hint: kept\n---\n")
	w := openTest(t, d)
	for file, want := range map[string]string{
		"a.md": "filter must be a mapping",
		"b.md": `filter "status" must be a value or a list`,
		"c.md": `filter type "project"`,
		"e.md": "view must be a mapping",
		"f.md": "view sort must be",
		"g.md": `filter "tags" names no value`,
	} {
		if !problem(w, file, want) {
			t.Errorf("%s: no problem %q in %+v", file, want, w.Problems)
		}
	}
	if !problem(w, "f.md", "view group must be a property name") {
		t.Error("group list not reported")
	}
	for _, p := range w.Problems {
		if p.Path == "ok.md" {
			t.Errorf("valid view reported: %+v", p)
		}
	}
	if _, err := w.EvaluateView("alice", "a"); err == nil {
		t.Fatal("invalid view evaluated")
	}
	if _, err := w.EvaluateView("alice", "alice"); err == nil || !strings.Contains(err.Error(), "not a view") {
		t.Fatalf("non-view evaluated: %v", err)
	}
	if _, err := w.EvaluateView("alice", "missing"); err == nil {
		t.Fatal("missing view evaluated")
	}
}

func TestUnreadableViewIsNotEvaluated(t *testing.T) {
	d := viewWorkspace(t)
	writeTest(t, d, "secret-board.md", "---\ntype: view\nfilter:\n  type: task\npermissions:\n  read: alice\n  admin: alice\n---\n")
	w := openTest(t, d)
	if _, err := w.EvaluateView("bob", "secret-board"); err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.EvaluateView("alice", "secret-board"); err != nil {
		t.Fatal(err)
	}
}

// A mutation that would make a View unusable is a new workspace problem and
// is rejected like any other.
func TestMutationCannotBreakAView(t *testing.T) {
	d := viewWorkspace(t)
	w := openTest(t, d)
	if err := w.SetMetadata("alice", "board", "view", "board"); err == nil {
		t.Fatal("a broken view definition was accepted")
	}
	if err := w.SetMetadata("alice", "board", "layout", "list"); err != nil {
		t.Fatalf("an unrelated property was refused: %v", err)
	}
}
