package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// A byte order mark stays at the start of the file. Before frontmatter behind
// a BOM was recognised, a mutation prepended a second frontmatter block and
// left the original one stranded in the body.
func TestMutationPreservesByteOrderMark(t *testing.T) {
	for name, src := range map[string]string{
		"with frontmatter":    "\ufeff---\nstatus: todo\n---\n# A\n",
		"without frontmatter": "\ufeff# A\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			writeTest(t, d, "a.md", src)
			w, err := Open(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.SetMetadata("", "a", "status", "done"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(d, "a.md"))
			if got, want := string(b), "\ufeff---\nstatus: done\n---\n# A\n"; got != want {
				t.Fatalf("a.md = %q, want %q", got, want)
			}
		})
	}
}

// Values and keys are YAML-encoded, never spliced: a line break followed by a
// fence or a policy cannot escape the property it was given to.
func TestSetMetadataCannotInjectFrontmatter(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "s.md", "---\npermissions:\n  write: alice\n  admin: bob\n---\n# S\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"note":                          "x\n---\npermissions:\n  admin: alice\n---\n",
		"note2":                         "x\npermissions:\n  admin: alice",
		"a\npermissions":                "{admin: alice}",
		"b\n---\npermissions:\n  admin": "alice",
	} {
		if err := w.SetMetadata("alice", "s", key, value); err != nil {
			continue // refusing is fine too
		}
		p := w.Pages["s.md"]
		if w.Allowed("alice", p, Admin) {
			t.Fatalf("set %q=%q escalated alice to admin", key, value)
		}
		if p.Body != "# S\n" {
			t.Fatalf("body changed: %q", p.Body)
		}
		if got, ok := p.Metadata[key].(string); !ok || got != value {
			t.Fatalf("metadata[%q] = %#v, want the literal value", key, p.Metadata[key])
		}
	}
	if len(w.Problems) != 0 {
		t.Fatalf("problems = %#v", w.Problems)
	}
}

func TestSetMetadataRefusals(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "s.md", "---\npermissions:\n  read: bob\n  admin: alice\n---\n# S\n")
	writeTest(t, d, "open.md", "---\nunclosed: true\n# Open\n")
	writeTest(t, d, "seq.md", "---\n- a\n---\n# Seq\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ actor, ref, key, want string }{
		{"alice", "missing", "status", "not found"},
		{"alice", "s", " ", "property name required"},
		{"alice", "s", "permissions", "use `jikko perm"},
		{"bob", "s", "status", "lacks write"},
		{"alice", "open", "status", "no closing"},
		{"alice", "seq", "status", "not a YAML mapping"},
	}
	for _, c := range cases {
		err := w.SetMetadata(c.actor, c.ref, c.key, "done")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("SetMetadata(%s, %s, %q) = %v, want %q", c.actor, c.ref, c.key, err, c.want)
		}
	}
	if err := w.SetMetadataWithToken("jk_bad", "s", "status", "x"); err == nil {
		t.Fatal("bad token accepted")
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadataWithToken(token, "s", "status", "done"); err != nil {
		t.Fatal(err)
	}
	if w.Pages["s.md"].Metadata["status"] != "done" {
		t.Fatal("token mutation not applied")
	}
}

// A comma-separated value creates a list; an existing list stays a list.
func TestSetMetadataLists(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "t.md", "---\ntags: [a]\n---\n# T\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "t", "tags", "x, y"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "t", "owners", "alice, bob"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "t", "single", "solo"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMetadata("", "t", "tags", ""); err != nil {
		t.Fatal(err)
	}
	m := w.Pages["t.md"].Metadata
	if got, ok := m["owners"].([]any); !ok || len(got) != 2 || got[1] != "bob" {
		t.Fatalf("owners = %#v", m["owners"])
	}
	if got, ok := m["tags"].([]any); !ok || len(got) != 0 {
		t.Fatalf("tags = %#v", m["tags"])
	}
	if m["single"] != "solo" {
		t.Fatalf("single = %#v", m["single"])
	}
}

func TestSetPermissionEdits(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "carol.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "doc.md", "# Doc\n")
	writeTest(t, d, "broken.md", "---\npermissions: alice\n---\n# Broken\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetPermission("alice", "doc", "owner", "alice"); err == nil {
		t.Fatal("unknown capability accepted")
	}
	if err := w.SetPermission("alice", "doc", "read", "ghost"); err == nil {
		t.Fatal("unknown identity accepted")
	}
	if err := w.SetPermission("alice", "missing", "read", "alice"); err == nil {
		t.Fatal("missing page accepted")
	}
	// Clearing a grant on an open file is a no-op, not an empty policy.
	// It used to write an empty "{}" frontmatter block, which every later
	// edit then inherited as a flow-style mapping.
	if err := w.SetPermission("alice", "doc", "read"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "doc.md")); string(b) != "# Doc\n" {
		t.Fatalf("no-op edit rewrote the file: %q", b)
	}
	// Restricting an open file: the policy must keep an administrator.
	if err := w.SetPermission("alice", "doc", "read", "bob"); err == nil {
		t.Fatal("policy with no administrator accepted")
	}
	if err := w.SetPermission("alice", "doc", "admin", "alice.md", " ", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPermission("alice", "doc", "read", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPermission("bob", "doc", "admin", "bob"); err == nil {
		t.Fatal("reader granted itself admin")
	}
	if err := w.SetPermission("carol", "doc", "read"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, "doc.md"))
	if got, want := string(b), "---\npermissions:\n  admin:\n    - alice\n    - carol\n---\n# Doc\n"; got != want {
		t.Fatalf("doc.md = %q, want %q", got, want)
	}
	// A policy that cannot be evaluated denies everyone, admin included, so it
	// cannot be repaired through Jikko: that is host-local recovery.
	if err := w.SetPermission("alice", "broken", "admin", "alice"); err == nil {
		t.Fatal("unusable policy edited")
	}
}

// A rejected mutation is judged in memory and never written: the file keeps
// its bytes and its modification time, and no temporary file is left behind.
func TestRejectedEscalationNeverTouchesDisk(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "mallory.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "engineering.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  write: engineering\n  admin: bob\n---\n# Secret\n")
	path := filepath.Join(d, "engineering.md")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	attempts := map[string]func() error{
		// alice may write the group but not administer what it grants.
		"add member": func() error { return w.SetMetadata("alice", "engineering", "members", "alice,mallory") },
		// The group file is open, but demoting it strips a policy's grants.
		"demote group": func() error { return w.SetMetadata("mallory", "engineering", "type", "document") },
	}
	for name, attempt := range attempts {
		if err := attempt(); err == nil {
			t.Fatalf("%s: escalation accepted", name)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s: file changed: %q", name, after)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Fatalf("%s: file was written (mtime %v, want %v)", name, info.ModTime(), old)
		}
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jikko-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	secret, _ := w.Resolve("secret")
	if w.Allowed("mallory", secret, Write) {
		t.Fatal("in-memory workspace adopted a rejected proposal")
	}
}

// Concurrent mutations of one Workspace serialize: each one sees the state
// the previous one left, so every update lands and none is lost.
func TestConcurrentMutationsSerialize(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "task.md", "---\ntype: task\n---\n# Task\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- w.SetMetadata("alice", "task", fmt.Sprintf("k%02d", i), fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("serialized mutation failed: %v", err)
		}
	}
	b, err := os.ReadFile(filepath.Join(d, "task.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if !strings.Contains(string(b), fmt.Sprintf("k%02d: %d\n", i, i)) {
			t.Fatalf("update %d lost:\n%s", i, b)
		}
	}
	if p, _ := w.Resolve("task"); len(p.Metadata) != n+1 {
		t.Fatalf("in-memory page has %d properties, want %d", len(p.Metadata), n+1)
	}
}

// Separate Workspace values on one directory serialize too. A value that read
// the page before another one changed it is refused by the revision check
// rather than overwriting the newer content.
func TestConcurrentWorkspacesNeverLoseUpdates(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "task.md", "---\ntype: task\n---\n# Task\n")
	const n = 8
	spaces := make([]*Workspace, n)
	for i := range spaces {
		w, err := Open(d)
		if err != nil {
			t.Fatal(err)
		}
		spaces[i] = w
	}
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := range spaces {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = spaces[i].SetMetadata("alice", "task", fmt.Sprintf("k%d", i), "x")
		}(i)
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(d, "task.md"))
	if err != nil {
		t.Fatal(err)
	}
	won := 0
	for i, err := range results {
		landed := strings.Contains(string(b), fmt.Sprintf("k%d: x", i))
		switch {
		case err == nil && !landed:
			t.Fatalf("mutation %d reported success but was lost:\n%s", i, b)
		case err != nil && landed:
			t.Fatalf("mutation %d reported failure but landed", i)
		case err != nil && !strings.Contains(err.Error(), "changed on disk"):
			t.Fatalf("mutation %d: unexpected error %v", i, err)
		case err == nil:
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d stale workspaces wrote, want exactly 1", won)
	}
}
