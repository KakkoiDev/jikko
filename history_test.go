package jikko

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestCommitChangesIsLimitedToItsPaths(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "a.md", "# A\n")
	write(t, root, "b.md", "# B\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.CommitChanges("alice", "Add a", "create", []string{"a.md", ".auth.md", "../outside.md", "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Empty || res.Commit == "" || len(res.Paths) != 1 || res.Paths[0] != "a.md" {
		t.Fatalf("result = %+v", res)
	}
	if files := gitOutput(t, root, "ls-files"); files != "a.md\n" {
		t.Fatalf("committed files = %q", files)
	}
	if trailers := gitOutput(t, root, "log", "-1", "--format=%(trailers:only,unfold)"); !strings.Contains(trailers, "Jikko-Operation: create") {
		t.Fatalf("trailers = %q", trailers)
	}
	// Something the caller had staged is left for a later commit.
	gitOutput(t, root, "add", "b.md")
	write(t, root, "a.md", "# A changed\n")
	if res, err := w.CommitChanges("alice", "Change a", "save", []string{"a.md"}); err != nil || res.Empty {
		t.Fatalf("second commit: %+v %v", res, err)
	}
	if files := gitOutput(t, root, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != "a.md" {
		t.Fatalf("second commit touched %q", files)
	}
	if staged := gitOutput(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "b.md" {
		t.Fatalf("staged after commit = %q", staged)
	}
	// Nothing changed: no commit.
	if res, err := w.CommitChanges("alice", "Again", "save", []string{"a.md"}); err != nil || !res.Empty || res.Commit != "" {
		t.Fatalf("empty commit: %+v %v", res, err)
	}
}

func TestCommitNeverStagesTrackedCredentials(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, ".auth.md", "---\ncredentials: []\n---\n")
	gitOutput(t, root, "add", "-f", ".auth.md")
	gitOutput(t, root, "-c", "user.name=x", "-c", "user.email=x@y", "commit", "-q", "-m", "leak")
	write(t, root, ".auth.md", "---\ncredentials:\n  - identity: alice\n    token_hash: abc\n---\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit("alice", "Pages", ""); err != nil {
		t.Fatal(err)
	}
	if files := gitOutput(t, root, "show", "--name-only", "--format=", "HEAD"); strings.Contains(files, ".auth.md") {
		t.Fatalf("credential file committed: %q", files)
	}
	if status := gitOutput(t, root, "status", "--porcelain"); !strings.Contains(status, " M .auth.md") {
		t.Fatalf("status = %q", status)
	}
}

func TestCommitKeepsTheConfiguredGitAuthor(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	gitOutput(t, root, "config", "user.name", "Pat Example")
	gitOutput(t, root, "config", "user.email", "pat@example.com")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit("alice", "Import", ""); err != nil {
		t.Fatal(err)
	}
	if author := gitOutput(t, root, "log", "-1", "--format=%an <%ae>"); strings.TrimSpace(author) != "Pat Example <pat@example.com>" {
		t.Fatalf("author = %q", author)
	}
	// Without any configured identity the harness supplies one rather than
	// failing the commit.
	other := gitWorkspace(t)
	write(t, other, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	w, err = Open(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit("alice", "Import", ""); err != nil {
		t.Fatal(err)
	}
	if author := gitOutput(t, other, "log", "-1", "--format=%an <%ae>"); strings.TrimSpace(author) != "Jikko <jikko@local>" {
		t.Fatalf("fallback author = %q", author)
	}
}

func TestLogReadsTrailersAndFollowsRenames(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "secret.md", "---\npermissions:\n  read: alice\n  admin: alice\n---\n# Secret\n")
	write(t, root, "plan.md", "# Plan\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := w.Log("", "", 0); err != nil || len(entries) != 0 {
		t.Fatalf("log of a repository without commits = %v, %v", entries, err)
	}
	if err := w.Commit("alice", "Import", "import"); err != nil {
		t.Fatal(err)
	}
	w.AutoCommit = true
	if _, err := w.Rename("alice", "plan", "plans/q4", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("alice", "secret", "status", "draft"); err != nil {
		t.Fatal(err)
	}
	entries, err := w.Log("bob", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Operation != "set-metadata" || entries[0].Actor != "alice" || len(entries[0].Paths) != 0 {
		t.Fatalf("latest entry disclosed a restricted path or lost its trailers: %+v", entries[0])
	}
	if entries[1].Operation != "rename" || strings.Join(entries[1].Paths, ",") != "plan.md,plans/q4.md" {
		t.Fatalf("rename entry = %+v", entries[1])
	}
	if entries[2].Subject != "Import" || entries[2].Operation != "import" || entries[2].Date == "" || entries[2].Commit == "" {
		t.Fatalf("import entry = %+v", entries[2])
	}
	alice, err := w.Log("alice", "", 1)
	if err != nil || len(alice) != 1 || strings.Join(alice[0].Paths, ",") != "secret.md" {
		t.Fatalf("admin log = %+v, %v", alice, err)
	}
	followed, err := w.Log("bob", "plans/q4", 0)
	if err != nil || len(followed) != 2 {
		t.Fatalf("followed log = %+v, %v", followed, err)
	}
	if _, err := w.Log("bob", "secret", 0); err == nil {
		t.Fatal("log of a forbidden page")
	}
	if _, err := w.Log("bob", "nothing", 0); err == nil {
		t.Fatal("log of a missing page")
	}
}

func TestChangedSince(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "a.md", "# A\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit("alice", "Import", ""); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if changed, err := w.ChangedSince(base); err != nil || len(changed) != 0 {
		t.Fatalf("changed = %v, %v", changed, err)
	}
	write(t, root, "a.md", "# A edited\n")
	write(t, root, "new/b.md", "# B\n")
	changed, err := w.ChangedSince(base)
	if err != nil || strings.Join(changed, ",") != "a.md,new/b.md" {
		t.Fatalf("changed = %v, %v", changed, err)
	}
	for _, bad := range []string{"", "-x", "nosuchrev"} {
		if _, err := w.ChangedSince(bad); err == nil {
			t.Fatalf("revision %q accepted", bad)
		}
	}
	plain, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.ChangedSince("HEAD"); !errors.Is(err, ErrNoGit) {
		t.Fatalf("outside Git: %v", err)
	}
}

func TestAutoCommitRecordsEachMutation(t *testing.T) {
	root := gitWorkspace(t)
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "plan.md", "# Plan\n\nDraft.\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var seen []*CommitResult
	w.AutoCommit = true
	w.OnCommit = func(r *CommitResult) { seen = append(seen, r) }
	if _, err := w.AddComment("alice", "plan", "Draft", "Really?"); err != nil {
		t.Fatal(err)
	}
	if err := w.CreatePage("alice", "tasks/t", "---\ntype: task\nstatus: todo\n---\n# T\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Upload("alice", "chart.png", []byte("png"), UploadOptions{Into: "plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Upload("alice", "loose.png", []byte("png"), UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Delete("alice", "tasks/t", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	// A no-op mutation makes no commit.
	if _, err := w.Upload("alice", "x.png", []byte("png"), UploadOptions{Path: "assets/x.png"}); err != nil {
		t.Fatal(err)
	}
	ops := []string{"comment-add", "create", "upload", "upload", "delete", "upload"}
	if len(seen) != len(ops) {
		t.Fatalf("commits = %d, want %d", len(seen), len(ops))
	}
	for i, r := range seen {
		if r.Operation != ops[i] || r.Actor != "alice" || r.Commit == "" {
			t.Fatalf("commit %d = %+v", i, r)
		}
	}
	if paths := seen[2].Paths; strings.Join(paths, ",") != "chart.png,plan.md" {
		t.Fatalf("upload commit paths = %v", paths)
	}
	if status := gitOutput(t, root, "status", "--porcelain"); !strings.Contains(status, "?? alice.md") || strings.Contains(status, "plan.md") {
		t.Fatalf("status = %q", status)
	}
	if n := strings.Count(gitOutput(t, root, "log", "--format=%H"), "\n"); n != len(ops) {
		t.Fatalf("commits in log = %d", n)
	}
}

func TestAutoCommitRefusesBeforeWritingOutsideGit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "plan.md", "# Plan\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	w.AutoCommit = true
	if err := w.SetMetadata("alice", "plan", "status", "x"); !errors.Is(err, ErrNoGit) {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.Upload("alice", "a.png", []byte("x"), UploadOptions{}); !errors.Is(err, ErrNoGit) {
		t.Fatalf("upload err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "plan.md")); string(b) != "# Plan\n" {
		t.Fatalf("plan.md written: %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "a.png")); !os.IsNotExist(err) {
		t.Fatal("asset written")
	}
}
