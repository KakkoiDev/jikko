package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorizedMetadataMutation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "task.md", "---\ntype: task\nstatus: todo\npermissions:\n  write: alice\n---\n# Work\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("bob", "task", "status", "done"); err == nil {
		t.Fatal("bob should be denied")
	}
	if err := w.SetMetadata("alice", "task", "status", "done"); err != nil {
		t.Fatal(err)
	}
	w, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["task.md"].Metadata["status"] != "done" {
		t.Fatal("mutation not persisted")
	}
}

// Changing permissions requires admin and goes through SetPermission;
// SetMetadata refuses the property for every actor.
func TestPermissionsRequireAdmin(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "task.md", "---\ntype: task\npermissions:\n  write: alice\n  admin: bob\n---\n# Work\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("bob", "task", "permissions", "bob"); err == nil {
		t.Fatal("SetMetadata must not mutate ACL")
	}
	if err := w.SetPermission("alice", "task", "admin", "alice"); err == nil {
		t.Fatal("write must not mutate ACL")
	}
	if err := w.SetPermission("bob", "task", "read", "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestTokenMutation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\nold\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBodyWithToken(token, "note", "# Note\nnew\n"); err != nil {
		t.Fatal(err)
	}
	w, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Pages["note.md"].Body != "# Note\nnew\n" {
		t.Fatalf("body = %q", w.Pages["note.md"].Body)
	}
	if err := w.ReplaceBodyWithToken("jk_invalid", "note", "# Note\nx\n"); err == nil {
		t.Fatal("invalid token accepted")
	}
}

func TestReplaceBodyKeepsFrontmatterBytes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	front := "---\ntype: task # kept\nstatus: todo\npermissions:\n  write: alice\n---\n"
	write(t, root, "task.md", front+"# Work\nold\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBody("alice", "task", "# Work\nnew\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "task.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != front+"# Work\nnew\n" {
		t.Fatalf("task.md = %q", got)
	}
}

func TestReplaceBodyRefusesFrontmatterFence(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\nold\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	err = w.ReplaceBody("alice", "note", "---\npermissions:\n  read: alice\n---\n# Note\n")
	if err == nil || !strings.Contains(err.Error(), "use set or perm") {
		t.Fatalf("frontmatter injected through body: err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Note\nold\n" {
		t.Fatalf("note.md = %q, want unchanged", got)
	}
}

// A mutation is judged by its own effect on the workspace as it is now. A
// defect that someone else introduced elsewhere is not blamed on it; a defect
// the mutation itself would introduce is rejected and never reaches disk.
func TestReplaceBodyFailsClosed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\nold\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "other.md", "---\nstatus: [\n---\n# Other\n")
	if err := w.ReplaceBody("alice", "note", "# Note\nnew\n"); err != nil {
		t.Fatalf("an unrelated defect was blamed on the mutation: %v", err)
	}
	if err := w.SetMetadata("alice", "note", "type", "bogus"); err == nil {
		t.Fatal("mutation introducing a workspace problem accepted")
	}
	got, err := os.ReadFile(filepath.Join(root, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Note\nnew\n" {
		t.Fatalf("note.md = %q, want the rejected edit absent", got)
	}
}

// A byte order mark does not smuggle a frontmatter block in through a body.
func TestReplaceBodyRejectsFenceBehindByteOrderMark(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBody("alice", "note", "\ufeff---\ntype: task\n---\n# Note\n"); err == nil {
		t.Fatal("frontmatter injected through a body behind a byte order mark")
	}
}

// A file whose closing fence is the last line, with no trailing newline, used
// to have the new body glued onto the fence, and the mutation was rejected.
func TestReplaceBodyAfterFenceAtEndOfFile(t *testing.T) {
	for name, c := range map[string]struct{ src, want string }{
		"LF":   {"---\nstatus: x\n---", "---\nstatus: x\n---\n# Hi\n"},
		"CRLF": {"---\r\nstatus: x\r\n---", "---\r\nstatus: x\r\n---\r\n# Hi\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
			write(t, root, "n.md", c.src)
			w, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.ReplaceBody("alice", "n", "# Hi\n"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(filepath.Join(root, "n.md"))
			if string(got) != c.want {
				t.Fatalf("n.md = %q, want %q", got, c.want)
			}
			if p := w.Pages["n.md"]; p.Metadata["status"] != "x" || p.Title != "Hi" {
				t.Fatalf("page = %#v", p)
			}
		})
	}
}

func TestReplaceBodyAuthorization(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "v.md", "---\ntype: view\n---\n")
	write(t, root, "s.md", "---\npermissions:\n  comment: bob\n  admin: alice\n---\n# S\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBody("alice", "v", "# no\n"); err == nil {
		t.Fatal("view body accepted")
	}
	if err := w.ReplaceBody("bob", "s", "# mine\n"); err == nil {
		t.Fatal("comment capability allowed a content edit")
	}
	if err := w.ReplaceBody("alice", "missing", "x"); err == nil {
		t.Fatal("missing reference accepted")
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBodyWithToken("jk_bad", "s", "x"); err == nil {
		t.Fatal("bad token accepted")
	}
	if err := w.ReplaceBodyWithToken(token, "s", "# Updated\n"); err != nil {
		t.Fatal(err)
	}
	if w.Pages["s.md"].Title != "Updated" {
		t.Fatal("workspace not refreshed")
	}
	if _, err := w.AuthorizeToken(token, w.Pages["s.md"], Admin); err != nil {
		t.Fatal(err)
	}
	bobToken, _ := w.CreateCredential("bob")
	if _, err := w.AuthorizeToken(bobToken, w.Pages["s.md"], Write); err == nil {
		t.Fatal("commenter authorized to write")
	}
	if _, err := w.AuthorizeToken("jk_bad", w.Pages["s.md"], Read); err == nil {
		t.Fatal("bad token authorized")
	}
}
