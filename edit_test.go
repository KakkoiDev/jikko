package jikko

import (
	"strings"
	"testing"
)

func TestSourceFields(t *testing.T) {
	src := []byte("---\ntype: task\ntags: [a, b]\ndue: 2026-09-20\npermissions:\n  read: alice\nnotes: |\n  two\n  lines\nodd: [\"x, y\"]\n---\n# Body\n")
	got := SourceFields(src)
	want := []Field{
		{"type", "task", true}, {"tags", "a, b", true}, {"due", "2026-09-20", true},
		{"permissions", "read: alice", false}, {"notes", "|\n  two\n  lines", false}, {"odd", `["x, y"]`, false},
	}
	if len(got) != len(want) {
		t.Fatalf("fields = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if SourceFields([]byte("# No frontmatter\n")) != nil || SourceFields([]byte("---\n[broken\n---\n")) != nil {
		t.Fatal("fields from a page without readable frontmatter")
	}
}

func TestEditSource(t *testing.T) {
	src := []byte("---\ntype: task\n# keep me\nstatus: todo\ntags: [a]\nowner: bob\n---\n# Old\n")
	body := "# New\n\nText.\n"
	out, err := EditSource(src, SourceEdit{Set: map[string]string{"status": "doing", "tags": "a, b", "due": "2026-10-01"}, Remove: []string{"owner"}, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntype: task\n# keep me\nstatus: doing\ntags: [a, b]\ndue: 2026-10-01\n---\n# New\n\nText.\n"
	if string(out) != want {
		t.Fatalf("out =\n%s", out)
	}
	if out, err := EditSource(src, SourceEdit{Body: &body}); err != nil || !strings.HasPrefix(string(out), string(src[:strings.Index(string(src), "# Old")])) {
		t.Fatalf("body-only edit changed frontmatter: %q %v", out, err)
	}
	for _, bad := range []SourceEdit{
		{Set: map[string]string{"permissions": "alice"}},
		{Remove: []string{"permissions"}},
		{Set: map[string]string{" ": "x"}},
	} {
		if _, err := EditSource(src, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	fence := "---\npermissions: {read: x}\n---\n"
	if _, err := EditSource([]byte("plain\n"), SourceEdit{Body: &fence}); err == nil {
		t.Fatal("a body injected frontmatter")
	}
}
