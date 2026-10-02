package jikko

import (
	"os"
	"path/filepath"
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

// A body that would leave the workspace with an unparseable source is
// rejected and the file is restored.
func TestReplaceBodyFailsClosed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\nold\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReplaceBody("alice", "note", "---\nstatus: [\n---\n# Note\n"); err == nil {
		t.Fatal("body introducing a workspace problem accepted")
	}
	got, err := os.ReadFile(filepath.Join(root, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Note\nold\n" {
		t.Fatalf("note.md = %q, want original restored", got)
	}
}
