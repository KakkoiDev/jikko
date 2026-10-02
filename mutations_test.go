package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorizationBearingGroupMutationRequiresAdmin(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "engineering.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  write: engineering\n  admin: bob\n---\n# Secret\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CanChangeMembers("alice", "engineering"); err == nil {
		t.Fatal("writer must not mutate authorization-bearing group")
	}
	if err := w.CanChangeMembers("bob", "engineering"); err != nil {
		t.Fatalf("admin should be allowed: %v", err)
	}
}

// The rule the specification states is about effect: a caller may not hand out
// a capability it cannot administer. Adding a member does that; removing one
// does not, and used to be refused anyway.
func TestMembershipChangeJudgedByEffect(t *testing.T) {
	setup := func(t *testing.T, members string) (string, *Workspace) {
		d := t.TempDir()
		writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "mallory.md", "---\ntype: identity\n---\n")
		writeTest(t, d, "engineering.md", "---\ntype: identity\nmembers: "+members+"\n---\n")
		writeTest(t, d, "secret.md", "---\npermissions:\n  write: engineering\n  admin: bob\n---\n# Secret\n")
		w, err := Open(d)
		if err != nil {
			t.Fatal(err)
		}
		return d, w
	}

	t.Run("granting requires admin on the affected file", func(t *testing.T) {
		_, w := setup(t, "[alice]")
		err := w.SetMetadata("alice", "engineering", "members", "alice,mallory")
		if err == nil {
			t.Fatal("alice must not widen access she cannot administer")
		}
		if !strings.Contains(err.Error(), "grant") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("an administrator may grant", func(t *testing.T) {
		_, w := setup(t, "[alice]")
		if err := w.SetMetadata("bob", "engineering", "members", "alice,mallory"); err != nil {
			t.Fatalf("admin refused: %v", err)
		}
		secret, _ := w.Resolve("secret")
		if !w.Allowed("mallory", secret, Write) {
			t.Fatal("grant not applied")
		}
	})

	t.Run("narrowing access needs no admin", func(t *testing.T) {
		_, w := setup(t, "[alice, mallory]")
		if err := w.SetMetadata("alice", "engineering", "members", "alice"); err != nil {
			t.Fatalf("removing a member widens nothing and must be allowed: %v", err)
		}
		secret, _ := w.Resolve("secret")
		if w.Allowed("mallory", secret, Write) {
			t.Fatal("mallory should have lost write")
		}
	})
}

// Demoting an ACL-bearing identity is an authorization change. It used to be an
// ordinary write, and it silently stripped every grant naming that identity.
func TestIdentityDemotionRejected(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "mallory.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "root.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "admins.md", "---\ntype: identity\nmembers: [root]\n---\n# Admins\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  read: admins\n  admin: admins\n---\n# Secret\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("mallory", "admins", "type", "document"); err == nil {
		t.Fatal("demoting an identity named by a policy must be rejected")
	}
	secret, _ := w.Resolve("secret")
	if !w.Allowed("root", secret, Admin) {
		t.Fatal("rollback failed: root lost administration")
	}
	if b, _ := os.ReadFile(filepath.Join(d, "admins.md")); !strings.Contains(string(b), "type: identity") {
		t.Fatalf("file not rolled back: %s", b)
	}
}

func TestLastAdministratorProtected(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  admin: alice\n  read: bob\n---\n# Secret\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetPermission("alice", "secret", "admin"); err == nil {
		t.Fatal("removing the last administrator must be rejected")
	}
	if err := w.SetPermission("alice", "secret", "admin", "bob"); err != nil {
		t.Fatalf("handing over admin: %v", err)
	}
}

// permissions is a policy, not a scalar. Assigning a plain value flattened the
// mapping and reopened the file to everyone.
func TestSetCannotFlattenPolicy(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "s.md", "---\npermissions:\n  admin: bob\n  read: bob\n---\n# S\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("bob", "s", "permissions", "read: bob"); err == nil {
		t.Fatal("permissions must not be settable as a plain value")
	}
	p, _ := w.Resolve("s")
	if w.Allowed("nobody", p, Read) {
		t.Fatal("policy was destroyed")
	}
	if err := w.SetMetadata("bob", "s", "nested", "x"); err != nil {
		t.Fatalf("ordinary property: %v", err)
	}
}

func TestSetPermissionEditsPolicySurgically(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"alice", "bob", "carol"} {
		writeTest(t, d, n+".md", "---\ntype: identity\n---\n# "+n+"\n")
	}
	writeTest(t, d, "s.md", "---\n# who may touch this\ntype: task\nstatus: todo\ndue: 2026-09-20\ncount: 3\npermissions:\n  admin: alice\n---\n# S\n\nBody stays.\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetPermission("alice", "s", "read", "bob", "carol"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, "s.md"))
	got := string(b)
	for _, want := range []string{"# who may touch this", "due: 2026-09-20", "count: 3", "admin: alice", "- bob", "- carol", "Body stays."} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q from:\n%s", want, got)
		}
	}
	if strings.Index(got, "type: task") > strings.Index(got, "status: todo") {
		t.Fatalf("key order changed:\n%s", got)
	}
	p, _ := w.Resolve("s")
	if p.Metadata["count"] != 3 {
		t.Fatalf("count retyped: %#v", p.Metadata["count"])
	}
	if !w.Allowed("bob", p, Read) || w.Allowed("bob", p, Write) {
		t.Fatal("read grant not applied cleanly")
	}
}

func TestSetMetadataInfersScalarTypes(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\nstatus: todo\n---\n# A\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "a", "count", "7"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "a", "note", "null"); err != nil {
		t.Fatal(err)
	}
	p, _ := w.Resolve("a")
	if p.Metadata["count"] != 7 {
		t.Fatalf("count = %#v, want a number", p.Metadata["count"])
	}
	if p.Metadata["note"] != "null" {
		t.Fatalf("note = %#v, want the string", p.Metadata["note"])
	}
}

// The workspace must reflect what it just wrote.
func TestMutationRefreshesWorkspace(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\nstatus: todo\n---\n# A\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "a", "status", "done"); err != nil {
		t.Fatal(err)
	}
	p, _ := w.Resolve("a")
	if p.Metadata["status"] != "done" {
		t.Fatalf("in-memory status = %v", p.Metadata["status"])
	}
}

func TestMutationDetectsConcurrentEdit(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\nstatus: todo\n---\n# A\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, d, "a.md", "---\nstatus: doing\nowner: someone-else\n---\n# A\n")
	err = w.SetMetadata("", "a", "status", "done")
	if err == nil {
		t.Fatal("a concurrent edit must not be overwritten")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(d, "a.md"))
	if !strings.Contains(string(b), "owner: someone-else") {
		t.Fatal("the other writer's edit was lost")
	}
}

func TestMutationNeverWritesThroughSymlink(t *testing.T) {
	d, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "target.md")
	if err := os.WriteFile(target, []byte("---\nstatus: original\n---\n# Outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(d, "link.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "link", "status", "pwned"); err == nil {
		t.Fatal("wrote through a symlink")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "status: original") {
		t.Fatalf("file outside the workspace was modified: %s", b)
	}
}

func TestMutationPreservesFileMode(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\nstatus: todo\n---\n# A\n")
	path := filepath.Join(d, "a.md")
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "a", "status", "done"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestMutationPreservesCRLF(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\r\nstatus: todo\r\n---\r\n# A\r\nBody\r\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "a", "status", "done"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, "a.md"))
	if !strings.Contains(string(b), "status: done\r\n") {
		t.Fatalf("frontmatter line endings changed: %q", b)
	}
	if !strings.Contains(string(b), "# A\r\nBody\r\n") {
		t.Fatalf("body was rewritten: %q", b)
	}
}

// Clearing the only grant would leave a deny-all policy. That is almost never
// what the caller meant, so it is refused with the recovery spelled out.
func TestClearingTheOnlyGrantIsRefused(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "s.md", "---\npermissions:\n  admin: alice\n---\n# S\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	err = w.SetPermission("alice", "s", "admin")
	if err == nil {
		t.Fatal("emptying a policy must be refused")
	}
	if !strings.Contains(err.Error(), "empty policy") {
		t.Fatalf("unexpected error: %v", err)
	}
	p, _ := w.Resolve("s")
	if !w.Allowed("alice", p, Admin) {
		t.Fatal("policy was damaged")
	}
}
