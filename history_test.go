package jikko

import (
	"os/exec"
	"strings"
	"testing"
)

func gitWorkspace(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return root
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestCommitRecordsActorTrailers(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "people/alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "team.md", "---\ntype: identity\nmembers: [alice]\n---\n# Team\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit("team", "msg", ""); err == nil {
		t.Fatal("a group committed")
	}
	if err := w.Commit("nobody", "msg", ""); err == nil {
		t.Fatal("unknown identity committed")
	}
	if err := w.Commit("alice", "  ", ""); err == nil {
		t.Fatal("empty message accepted")
	}
	if err := w.Commit("alice", "Initial import", ""); err != nil {
		t.Fatal(err)
	}
	trailers := gitOutput(t, root, "log", "-1", "--format=%(trailers:only,unfold)")
	if !strings.Contains(trailers, "Jikko-Actor: people/alice") || !strings.Contains(trailers, "Jikko-Operation: workspace-mutation") {
		t.Fatalf("trailers = %q", trailers)
	}
	// Nothing changed: no empty audit commit.
	if err := w.Commit("alice", "Again", "noop"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(gitOutput(t, root, "log", "--format=%H"), "\n"); n != 1 {
		t.Fatalf("commits = %d, want 1", n)
	}
}

// The operation lands in the trailer block. A line break in it let the caller
// append a Jikko-Actor trailer naming someone else.
func TestCommitOperationCannotForgeTrailers(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"x\nJikko-Actor: root", "x\rJikko-Actor: root", "two words"} {
		if err := w.Commit("alice", "msg", op); err == nil {
			t.Fatalf("operation %q accepted", op)
		}
	}
	if out, _ := exec.Command("git", "-C", root, "log", "--format=%B").CombinedOutput(); strings.Contains(string(out), "root") {
		t.Fatalf("forged trailer committed: %s", out)
	}
}

func TestCommitOutsideGit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	if err := w.Commit("alice", "msg", ""); err == nil || !strings.Contains(err.Error(), "not Git-backed") {
		t.Fatalf("err = %v", err)
	}
}
