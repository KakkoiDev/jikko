package jikko

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hasProblem(problems []Problem, pagePath, substr string) bool {
	for _, p := range problems {
		if p.Path == pagePath && strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}

func TestWorkReferencesAreValidated(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "notes.md", "# Notes\n")
	writeTest(t, d, "dep.md", "---\ntype: task\nstatus: done\n---\n")
	if err := os.WriteFile(filepath.Join(d, "shot.png"), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	writeTest(t, d, "good.md", "---\ntype: task\nassignee: [alice]\ndepends_on: dep\nblocked_by: \"[[notes]]\"\nproof:\n  - [[notes]]\n  - shot.png\n  - https://ci.example/run/1\n  - commit:abc123\nrequires: [proof, review]\n---\n")
	writeTest(t, d, "bad.md", "---\ntype: task\nassignee: notes\ndepends_on: [notes, ghost]\nblocked_by: ghost\nproof: [missing.png, \"[[gone]]\", \"commit:zz\"]\nrequires: [magic]\n---\n")
	writeTest(t, d, "malformed.md", "---\ntype: task\nproof:\n  url: https://x\nrequires: {a: b}\n---\n")
	writeTest(t, d, "doc.md", "---\nassignee: nobody\n---\n# Not a task\n")
	w := openTest(t, d)
	problems, err := w.UnresolvedReferences()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		if p.Path == "good.md" || p.Path == "doc.md" {
			t.Errorf("unexpected problem: %+v", p)
		}
	}
	for _, want := range []string{
		`assignee "notes" is not an identity`,
		`depends_on "notes" is not a task`,
		`depends_on "ghost" is not a task`,
		`unresolved blocked_by "ghost"`,
		`unresolved proof "missing.png"`,
		`unresolved proof "gone"`,
		`proof "commit:zz" is not a commit id`,
		`unknown completion requirement "magic"`,
	} {
		if !hasProblem(problems, "bad.md", want) {
			t.Errorf("missing %q in %+v", want, problems)
		}
	}
	for _, want := range []string{"proof must be a reference", "requires must be a list"} {
		if !hasProblem(problems, "malformed.md", want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCommitProofIsCheckedAgainstGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	d := gitWorkspace(t)
	writeTest(t, d, "readme.md", "# Readme\n")
	gitOutput(t, d, "add", ".")
	gitOutput(t, d, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	head := strings.TrimSpace(gitOutput(t, d, "rev-parse", "HEAD"))
	writeTest(t, d, "known.md", "---\ntype: task\nproof: commit:"+head[:10]+"\n---\n")
	writeTest(t, d, "unknown.md", "---\ntype: task\nproof: commit:deadbeefdeadbeef\n---\n")
	w := openTest(t, d)
	problems, err := w.UnresolvedReferences()
	if err != nil {
		t.Fatal(err)
	}
	if hasProblem(problems, "known.md", "commit") {
		t.Fatalf("a real commit was rejected: %+v", problems)
	}
	if !hasProblem(problems, "unknown.md", "names no commit") {
		t.Fatalf("an unknown commit was accepted: %+v", problems)
	}
}

func TestWorkWarnings(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "open-dep.md", "---\ntype: task\nstatus: doing\nassignee: alice\n---\n")
	writeTest(t, d, "unowned.md", "---\ntype: task\nstatus: doing\n---\n")
	writeTest(t, d, "early.md", "---\ntype: task\nstatus: done\ndepends_on: open-dep\nblocked_by: [open-dep]\n---\n")
	writeTest(t, d, "unproven.md", "---\ntype: task\nstatus: done\nrequires: [proof, assignee, review]\n---\nA <!--comment:c1-->claim<!--/comment:c1-->.\n\n<!--comment-thread:c1\n@alice: Evidence?\n-->\n")
	writeTest(t, d, "proven.md", "---\ntype: task\nstatus: done\nassignee: alice\nproof: https://ci.example/1\nrequires: [proof, assignee]\n---\n")
	writeTest(t, d, "pending.md", "---\ntype: task\nstatus: todo\nrequires: [proof]\n---\n")
	w := openTest(t, d)
	warnings := w.Warnings()
	for _, want := range []struct{ path, msg string }{
		{"unowned.md", "in progress but has no assignee"},
		{"early.md", "depends_on open-dep.md is not"},
		{"early.md", "blocked_by open-dep.md is not"},
		{"unproven.md", "requires proof"},
		{"unproven.md", "requires an assignee"},
		{"unproven.md", "requires no unresolved comments"},
		{"unproven.md", "1 unresolved comment"},
	} {
		if !hasProblem(warnings, want.path, want.msg) {
			t.Errorf("missing warning %s: %q in %+v", want.path, want.msg, warnings)
		}
	}
	for _, p := range warnings {
		if p.Path == "proven.md" || p.Path == "pending.md" || p.Path == "open-dep.md" {
			t.Errorf("unexpected warning: %+v", p)
		}
	}
}
