package jikko

import "testing"

func TestAuthorizedMetadataMutation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "task.md", "---\ntype: task\nstatus: todo\npermissions:\n  write: alice\n---\n# Work\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if err := w.SetMetadata("bob", "task", "status", "done"); err == nil { t.Fatal("bob should be denied") }
	if err := w.SetMetadata("alice", "task", "status", "done"); err != nil { t.Fatal(err) }
	w, err = Open(root); if err != nil { t.Fatal(err) }
	if w.Pages["task.md"].Metadata["status"] != "done" { t.Fatal("mutation not persisted") }
}

func TestPermissionsRequireAdmin(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	write(t, root, "task.md", "---\ntype: task\npermissions:\n  write: alice\n  admin: bob\n---\n# Work\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	if err := w.SetMetadata("alice", "task", "permissions", map[string]any{"admin":"alice"}); err == nil { t.Fatal("write must not mutate ACL") }
	if err := w.SetMetadata("bob", "task", "permissions", map[string]any{"admin":"bob", "write":"alice"}); err != nil { t.Fatal(err) }
}

func TestTokenMutation(t *testing.T) {
	root := t.TempDir()
	write(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	write(t, root, "note.md", "# Note\nold\n")
	w, err := Open(root); if err != nil { t.Fatal(err) }
	token, err := w.CreateCredential("alice"); if err != nil { t.Fatal(err) }
	if err := w.ReplaceBodyWithToken(token, "note", "# Note\nnew\n"); err != nil { t.Fatal(err) }
	w, err = Open(root); if err != nil { t.Fatal(err) }
	if w.Pages["note.md"].Body != "# Note\nnew\n" { t.Fatalf("body = %q", w.Pages["note.md"].Body) }
}
