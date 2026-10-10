package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitBootstrapsAWorkspaceOnce(t *testing.T) {
	gitWorkspace(t) // skips without git
	dir := filepath.Join(t.TempDir(), "nested", "work")
	res, err := Init(dir, InitOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Git || !res.GitInitialized || !strings.HasPrefix(res.Token, "jk_") || res.Identity != "alice" {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(res.Created, ","); got != "alice.md,open-work.md,welcome.md" {
		t.Fatalf("created = %s", got)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(ignore) != ".auth.md\n.data/\n" {
		t.Fatalf(".gitignore = %q", ignore)
	}
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Problems) != 0 {
		t.Fatalf("problems = %v", w.Problems)
	}
	if p, err := w.Authenticate(res.Token); err != nil || p.Path != "alice.md" {
		t.Fatalf("token does not authenticate alice: %v", err)
	}
	if _, err := w.EvaluateView("", "open-work"); err != nil {
		t.Fatal(err)
	}
	if hits := w.Mentions("alice"); len(hits) != 1 || hits[0].Path != "welcome.md" {
		t.Fatalf("welcome page does not address alice: %v", hits)
	}
	if status := gitOutput(t, dir, "status", "--porcelain"); strings.Contains(status, ".auth.md") {
		t.Fatalf("credentials visible to git: %q", status)
	}

	// A second run changes nothing and issues no second token.
	if err := os.WriteFile(filepath.Join(dir, "welcome.md"), []byte("# Mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	again, err := Init(dir, InitOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Token != "" || len(again.Created) != 0 || again.GitInitialized || !again.Git {
		t.Fatalf("second run = %+v", again)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "welcome.md")); string(b) != "# Mine\n" {
		t.Fatalf("welcome.md overwritten: %q", b)
	}
	if n := strings.Count(gitOutput(t, dir, "rev-parse", "--git-dir"), "\n"); n != 1 {
		t.Fatal("git dir lost")
	}
	// Another identity in the same workspace gets its own token.
	bob, err := Init(dir, InitOptions{Identity: "bob"})
	if err != nil || bob.Token == "" || strings.Join(bob.Created, ",") != "bob.md" {
		t.Fatalf("bob = %+v, %v", bob, err)
	}
}

func TestInitRefusals(t *testing.T) {
	for _, name := range []string{"", "Alice", "a b", "people/alice", "../x", "-x"} {
		if _, err := Init(t.TempDir(), InitOptions{Identity: name, NoGit: true}); err == nil {
			t.Errorf("identity %q accepted", name)
		}
	}
	dir := t.TempDir()
	write(t, dir, "alice.md", "# Not an identity\n")
	if _, err := Init(dir, InitOptions{Identity: "alice", NoGit: true}); err == nil {
		t.Fatal("a document was taken for the identity")
	}
	write(t, dir, "crew.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	if _, err := Init(dir, InitOptions{Identity: "crew", NoGit: true}); err == nil {
		t.Fatal("a group was taken for the identity")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(file, InitOptions{Identity: "alice", NoGit: true}); err == nil {
		t.Fatal("a file was taken for the directory")
	}
}

func TestInitWithoutGitAndWithExistingIgnore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "node_modules\n/.auth.md")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	res, err := Init(dir, InitOptions{Identity: "alice", NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Git || res.GitInitialized {
		t.Fatalf("git initialized despite NoGit: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git created")
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(ignore) != "node_modules\n/.auth.md\n.data/\n" {
		t.Fatalf(".gitignore = %q", ignore)
	}
}
