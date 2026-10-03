package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotFiles returns every file of a workspace with its content, so a
// rejected operation can be shown to have touched nothing.
func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameFiles(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("file set changed: %d -> %d files", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed:\n%s\n---\n%s", k, v, after[k])
		}
	}
}

func openTest(t *testing.T, d string) *Workspace {
	t.Helper()
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestRenameRewritesLinksAndEmbeds(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "notes/design.md", "# Design\n\nSelf: [[design]].\n")
	writeTest(t, d, "index.md", "# Index\n\nSee [[design]], [[notes/design|the design]], [[design.md]] and\n\n![[notes/design]]\n\n```\n[[design]] stays in code\n```\n\nInline `[[design]]` too.\n")
	writeTest(t, d, "task.md", "---\ntype: task\nstatus: todo\nblocked_by: design\nproof:\n  - \"[[design]]\"\n  - https://example.com/design\n---\n# Task\n")
	w := openTest(t, d)
	res, err := w.Rename("alice", "design", "archive/old-design", RenameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "notes/design.md" || res.To != "archive/old-design.md" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(d, "notes/design.md")); !os.IsNotExist(err) {
		t.Fatal("old file still exists")
	}
	index := readFile(t, d, "index.md")
	want := "# Index\n\nSee [[old-design]], [[archive/old-design|the design]], [[old-design.md]] and\n\n![[archive/old-design]]\n\n```\n[[design]] stays in code\n```\n\nInline `[[design]]` too.\n"
	if index != want {
		t.Fatalf("index.md =\n%s", index)
	}
	if got := readFile(t, d, "archive/old-design.md"); got != "# Design\n\nSelf: [[old-design]].\n" {
		t.Fatalf("moved page = %q", got)
	}
	task := readFile(t, d, "task.md")
	if !strings.Contains(task, "blocked_by: old-design") || !strings.Contains(task, `"[[old-design]]"`) || !strings.Contains(task, "https://example.com/design") {
		t.Fatalf("task.md =\n%s", task)
	}
	if len(res.Rewritten) != 7 {
		t.Fatalf("rewritten = %+v", res.Rewritten)
	}
	moved, ok := w.Resolve("old-design")
	if !ok || len(moved.Backlinks) != 2 {
		t.Fatalf("workspace not refreshed: %+v", moved)
	}
	if unresolved, _ := w.UnresolvedReferences(); len(unresolved) != 0 {
		t.Fatalf("unresolved after rename: %+v", unresolved)
	}
}

// Moving a page can make a bare name ambiguous for an unrelated page. Those
// references are rewritten to the full path so they keep their meaning.
func TestRenameKeepsOtherReferencesResolving(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "a/note.md", "# A note\n")
	writeTest(t, d, "x.md", "# X\n")
	writeTest(t, d, "index.md", "[[note]] and [[x]]\n")
	w := openTest(t, d)
	if _, err := w.Rename("alice", "x", "b/note", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "index.md"); got != "[[a/note]] and [[b/note]]\n" {
		t.Fatalf("index.md = %q", got)
	}
}

func TestRenameRefusesUnaddressableResult(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "y.md", "# Y\n")
	writeTest(t, d, "x.md", "# X\n")
	writeTest(t, d, "index.md", "[[y]] [[x]]\n")
	before := snapshotFiles(t, d)
	w := openTest(t, d)
	// y.md is addressed only as "y", which archive/y.md would also answer to.
	if _, err := w.Rename("alice", "x", "archive/y", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "unambiguously") {
		t.Fatalf("err = %v", err)
	}
	sameFiles(t, before, snapshotFiles(t, d))
}

func TestRenameRewritesRelativeAssetEmbeds(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "docs/page.md", "![[img/diagram.png]]\n")
	if err := os.MkdirAll(filepath.Join(d, "docs/img"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "docs/img/diagram.png"), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	w := openTest(t, d)
	if _, err := w.Rename("alice", "page", "elsewhere/page", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "elsewhere/page.md"); got != "![[docs/img/diagram.png]]\n" {
		t.Fatalf("moved page = %q", got)
	}
}

func TestRenameIdentityRewritesAuthorizationReferences(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [alice, bob]\n---\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  read: crew\n  admin:\n    - alice\n---\n# Secret\n")
	writeTest(t, d, "task.md", "---\ntype: task\nassignee: alice\n---\n# Task\n\nPing @alice. Mail bob@alice.example.\n\nA <!--comment:c1-->point<!--/comment:c1-->.\n\n<!--comment-thread:c1\n@alice: Why?\n-->\n")
	w := openTest(t, d)
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.Rename("alice", "alice", "people/alicia", RenameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "crew.md"); !strings.Contains(got, "members: [alicia, bob]") {
		t.Fatalf("crew.md =\n%s", got)
	}
	if got := readFile(t, d, "secret.md"); !strings.Contains(got, "- alicia") {
		t.Fatalf("secret.md =\n%s", got)
	}
	task := readFile(t, d, "task.md")
	for _, want := range []string{"assignee: alicia", "Ping @alicia.", "bob@alice.example", "@alicia: Why?"} {
		if !strings.Contains(task, want) {
			t.Fatalf("task.md lacks %q:\n%s", want, task)
		}
	}
	if len(res.Rewritten) != 5 {
		t.Fatalf("rewritten = %+v", res.Rewritten)
	}
	secret, _ := w.Resolve("secret")
	if !w.Allowed("alicia", secret, Admin) || !w.Allowed("bob", secret, Read) {
		t.Fatal("effective access changed")
	}
	p, err := w.Authenticate(token)
	if err != nil || p.Path != "people/alicia.md" {
		t.Fatalf("credential did not follow the identity: %v %v", p, err)
	}
	// A new Identity given the old name must not inherit the credential.
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	w = openTest(t, d)
	if p, err := w.Authenticate(token); err != nil || p.Path != "people/alicia.md" {
		t.Fatalf("token now authenticates as %v (%v)", p, err)
	}
}

// Renaming an Identity rewrites access policies that name it. Changing a
// policy needs admin on that file, so a mere writer is refused.
func TestRenameIdentityNeedsAdminWhereItsPoliciesChange(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  write: crew\n  admin: bob\n---\n# Secret\n")
	writeTest(t, d, "hidden.md", "---\npermissions:\n  read: crew\n  admin: bob\n---\n# Hidden\n")
	before := snapshotFiles(t, d)
	w := openTest(t, d)
	_, err := w.Rename("alice", "crew", "team", RenameOptions{})
	if err == nil || !strings.Contains(err.Error(), "secret.md (needs admin)") {
		t.Fatalf("err = %v", err)
	}
	sameFiles(t, before, snapshotFiles(t, d))
	if _, err := w.Rename("bob", "crew", "team", RenameOptions{}); err != nil {
		t.Fatalf("admin refused: %v", err)
	}
	if got := readFile(t, d, "secret.md"); !strings.Contains(got, "write: team") {
		t.Fatalf("secret.md =\n%s", got)
	}
}

func TestRenameAuthorization(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "readonly.md", "---\npermissions:\n  read: bob\n  admin: alice\n---\n# Read only\n")
	writeTest(t, d, "private.md", "---\npermissions:\n  admin: alice\n---\n# Private\n\n[[open]]\n")
	writeTest(t, d, "open.md", "# Open\n")
	before := snapshotFiles(t, d)
	w := openTest(t, d)

	if _, err := w.Rename("bob", "readonly", "moved", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "lacks write") {
		t.Fatalf("reader renamed: %v", err)
	}
	if _, err := w.Rename("bob", "private", "moved", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "not found, ambiguous, or not permitted") {
		t.Fatalf("unreadable page disclosed: %v", err)
	}
	// open.md is open, but private.md links to it and bob may not edit
	// private.md. The rename is refused without naming the private page.
	_, err := w.Rename("bob", "open", "moved", RenameOptions{})
	if err == nil || !strings.Contains(err.Error(), "1 page(s) you cannot read") || strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.Rename("ghost", "open", "moved", RenameOptions{}); err == nil {
		t.Fatal("unauthenticated rename accepted")
	}
	for _, bad := range []string{"../escape", "/abs", ".git/x", "readonly", "notes.txt"} {
		if _, err := w.Rename("alice", "open", bad, RenameOptions{}); err == nil {
			t.Fatalf("rename to %q accepted", bad)
		}
	}
	sameFiles(t, before, snapshotFiles(t, d))
}

func TestRenameWithoutRewriteReportsBreakage(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "design.md", "# Design\n")
	writeTest(t, d, "index.md", "[[design]] [[later]]\n")
	w := openTest(t, d)
	res, err := w.Rename("alice", "design", "later", RenameOptions{NoRewrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "index.md"); got != "[[design]] [[later]]\n" {
		t.Fatalf("no-rewrite changed index.md: %q", got)
	}
	if len(res.Broken) != 1 || res.Broken[0].From != "design" || res.Broken[0].Path != "index.md" {
		t.Fatalf("broken = %+v", res.Broken)
	}
	if len(res.Captured) != 1 || res.Captured[0].From != "later" {
		t.Fatalf("captured = %+v", res.Captured)
	}
}

// A no-rewrite rename may break links, but never a membership or a policy:
// that would silently change authorization.
func TestRenameWithoutRewriteCannotBreakAuthorization(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [bob]\n---\n")
	before := snapshotFiles(t, d)
	w := openTest(t, d)
	if _, err := w.Rename("alice", "bob", "robert", RenameOptions{NoRewrite: true}); err == nil || !strings.Contains(err.Error(), "workspace problem") {
		t.Fatalf("err = %v", err)
	}
	sameFiles(t, before, snapshotFiles(t, d))
}

func TestRenameDetectsConcurrentEdit(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "design.md", "# Design\n")
	writeTest(t, d, "index.md", "[[design]]\n")
	w := openTest(t, d)
	// Rename always works from the current workspace, so an edit made before
	// it starts is simply included.
	writeTest(t, d, "index.md", "[[design]] edited\n")
	if _, err := w.Rename("alice", "design", "plan", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "index.md"); got != "[[plan]] edited\n" {
		t.Fatalf("index.md = %q", got)
	}
}

func TestDeletePage(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "draft.md", "---\npermissions:\n  write: bob\n  admin: alice\n---\n# Draft\n")
	writeTest(t, d, "index.md", "[[draft]]\n")
	writeTest(t, d, "discussed.md", "A <!--comment:c1-->point<!--/comment:c1-->.\n\n<!--comment-thread:c1\n@alice: Why?\n-->\n")
	w := openTest(t, d)

	if _, err := w.Delete("alice", "discussed", DeleteOptions{}); err == nil || !strings.Contains(err.Error(), "unresolved comment") {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.Delete("ghost", "index", DeleteOptions{}); err == nil {
		t.Fatal("unauthenticated delete accepted")
	}
	// Write includes deletion; admin is not needed even on a restricted page.
	res, err := w.Delete("bob", "draft", DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "draft.md")); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
	if len(res.Broken) != 1 || res.Broken[0].Path != "index.md" {
		t.Fatalf("broken = %+v", res.Broken)
	}
	if _, ok := w.Resolve("draft"); ok {
		t.Fatal("workspace not refreshed")
	}
	writeTest(t, d, "ro.md", "---\npermissions:\n  read: bob\n  admin: alice\n---\n")
	w = openTest(t, d)
	if _, err := w.Delete("bob", "ro", DeleteOptions{}); err == nil || !strings.Contains(err.Error(), "lacks write") {
		t.Fatalf("reader deleted: %v", err)
	}
}

func TestDeleteIdentityNamedByPolicy(t *testing.T) {
	setup := func(t *testing.T) string {
		d := t.TempDir()
		writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "carol.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [bob, carol]\n---\n")
		writeTest(t, d, "secret.md", "---\npermissions:\n  read: [bob, crew]\n  write: bob\n  admin: alice\n---\n# Secret\n")
		writeTest(t, d, "task.md", "---\ntype: task\nassignee: bob\n---\n@bob please\n")
		return d
	}

	t.Run("refused and reported", func(t *testing.T) {
		d := setup(t)
		before := snapshotFiles(t, d)
		w := openTest(t, d)
		_, err := w.Delete("alice", "bob", DeleteOptions{})
		if err == nil || !strings.Contains(err.Error(), "crew.md (members)") || !strings.Contains(err.Error(), "secret.md (permissions.read)") || !strings.Contains(err.Error(), "--prune") {
			t.Fatalf("err = %v", err)
		}
		sameFiles(t, before, snapshotFiles(t, d))
	})

	t.Run("pruned by an administrator", func(t *testing.T) {
		d := setup(t)
		w := openTest(t, d)
		token, err := w.CreateCredential("bob")
		if err != nil {
			t.Fatal(err)
		}
		res, err := w.Delete("alice", "bob", DeleteOptions{Prune: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, d, "crew.md"); !strings.Contains(got, "members: [carol]") {
			t.Fatalf("crew.md =\n%s", got)
		}
		secret := readFile(t, d, "secret.md")
		if strings.Contains(secret, "bob") || !strings.Contains(secret, "read: [crew]") || strings.Contains(secret, "write") {
			t.Fatalf("secret.md =\n%s", secret)
		}
		if len(res.Pruned) != 3 {
			t.Fatalf("pruned = %+v", res.Pruned)
		}
		// Assignee and mention are authored text: reported, not removed.
		if len(res.Broken) != 2 {
			t.Fatalf("broken = %+v", res.Broken)
		}
		if _, err := w.Authenticate(token); err == nil {
			t.Fatal("credential of a deleted identity still authenticates")
		}
		auth, _ := LoadAuth(d)
		if len(auth.Credentials) != 0 {
			t.Fatalf("credentials = %+v", auth.Credentials)
		}
	})

	t.Run("pruning a policy needs admin on it", func(t *testing.T) {
		d := setup(t)
		writeTest(t, d, "dave.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [bob, carol]\npermissions:\n  write: dave\n---\n")
		before := snapshotFiles(t, d)
		w := openTest(t, d)
		_, err := w.Delete("dave", "bob", DeleteOptions{Prune: true})
		if err == nil || !strings.Contains(err.Error(), "1 page(s) you cannot read") {
			t.Fatalf("err = %v", err)
		}
		sameFiles(t, before, snapshotFiles(t, d))
	})

	t.Run("pruning the last administrator is refused", func(t *testing.T) {
		d := setup(t)
		writeTest(t, d, "solo.md", "---\npermissions:\n  read: carol\n  admin: alice\n---\n")
		before := snapshotFiles(t, d)
		w := openTest(t, d)
		_, err := w.Delete("alice", "alice", DeleteOptions{Prune: true})
		if err == nil || !strings.Contains(err.Error(), "no identity able to administer") {
			t.Fatalf("err = %v", err)
		}
		sameFiles(t, before, snapshotFiles(t, d))
	})
}

// Rename is a write: a writer may move a restricted page, whose policy moves
// with it unchanged.
func TestWriterMayRenameRestrictedPage(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "plan.md", "---\npermissions:\n  write: bob\n  admin: alice\n---\n# Plan\n")
	w := openTest(t, d)
	if _, err := w.Rename("bob", "plan", "archive/plan", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	moved, ok := w.Resolve("archive/plan")
	if !ok || !w.Allowed("alice", moved, Admin) || !w.Allowed("bob", moved, Write) || w.Allowed("bob", moved, Admin) {
		t.Fatal("policy did not move with the page")
	}
}
