package jikko

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTest(t *testing.T, root, name, content string) { t.Helper(); write(t, root, name, content) }

func TestIdentityMembershipAndPermissions(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	writeTest(t, d, "engineering.md", "---\ntype: identity\nmembers: [alice]\n---\n# Engineering\n")
	writeTest(t, d, "staff.md", "---\ntype: identity\nmembers: [engineering]\n---\n# Staff\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  read: staff\n  write: engineering\n  admin: bob\n---\n# Secret\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ValidatePermissions(); err != nil {
		t.Fatal(err)
	}
	if !w.MemberOf("alice", "staff") {
		t.Fatal("expected transitive membership")
	}
	p, _ := w.Resolve("secret")
	if !w.Allowed("alice", p, Read) || !w.Allowed("alice", p, Write) {
		t.Fatal("alice should inherit engineering write/read")
	}
	if w.Allowed("alice", p, Admin) {
		t.Fatal("write must not imply admin")
	}
	if !w.Allowed("bob", p, Admin) || !w.Allowed("bob", p, Read) {
		t.Fatal("admin must imply read")
	}
}

func TestOpenByDefault(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "doc.md", "# Open\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := w.Resolve("doc")
	if !w.Allowed("nobody", p, Write) {
		t.Fatal("file without permissions should be open by Jikko")
	}
}

// A permissions mapping that cannot be read is not the absence of a policy.
// Treating it as absent silently published the file.
func TestUnusablePolicyDeniesEveryone(t *testing.T) {
	for name, frontmatter := range map[string]string{
		"scalar":   "permissions: alice\n",
		"sequence": "permissions:\n  - alice\n",
		"empty":    "permissions: {}\n",
		"null":     "permissions:\n",
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
			writeTest(t, d, "s.md", "---\n"+frontmatter+"---\n# S\n")
			w, err := Open(d)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := w.Resolve("s")
			if w.Allowed("alice", p, Read) {
				t.Fatal("unusable policy read as open")
			}
			if w.Allowed("nobody", p, Read) {
				t.Fatal("unusable policy read as open")
			}
			if err := w.ValidatePermissions(); err == nil {
				t.Fatal("unusable policy not reported")
			}
		})
	}
}

func TestIdentityCycleReported(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "a.md", "---\ntype: identity\nmembers: [b]\n---\n")
	writeTest(t, d, "b.md", "---\ntype: identity\nmembers: [a]\n---\n")
	w, err := Open(d)
	if err != nil {
		t.Fatalf("a membership cycle must not fail the scan: %v", err)
	}
	if err := w.ValidateIdentities(); err == nil {
		t.Fatal("expected membership cycle to be reported")
	}
	// Membership resolution must still terminate.
	if w.MemberOf("a", "b") != true && w.MemberOf("a", "b") != false {
		t.Fatal("unreachable")
	}
}

func TestDanglingPermissionReported(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "doc.md", "---\npermissions:\n  read: missing\n---\n# Doc\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ValidatePermissions(); err == nil {
		t.Fatal("expected dangling permission error")
	}
}

// An identity name that matches two files resolves to neither. That silently
// voided every grant naming it; it must now be reported.
func TestAmbiguousGrantReported(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "root.md", "---\ntype: identity\n---\n# Root\n")
	writeTest(t, d, "admins.md", "---\ntype: identity\nmembers: [root]\n---\n# Admins\n")
	writeTest(t, d, "team/admins.md", "---\ntype: identity\n---\n# Other\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  admin: admins\n---\n# Secret\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ValidatePermissions(); err == nil {
		t.Fatal("ambiguous grant subject not reported")
	}
	p, _ := w.Resolve("secret")
	if w.Allowed("root", p, Admin) {
		t.Fatal("an unresolvable grant must not be honoured")
	}
}

func TestTokenAuthentication(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "jk_") {
		t.Fatal("unexpected token format")
	}
	b, err := os.ReadFile(filepath.Join(d, ".auth.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatal("plaintext token persisted")
	}
	if info, err := os.Stat(filepath.Join(d, ".auth.md")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0600 {
		t.Fatalf("auth file mode = %v", info.Mode().Perm())
	}
	p, err := w.Authenticate(token)
	if err != nil || p.Title != "Alice" {
		t.Fatalf("token did not authenticate: %v", err)
	}
	if _, err := w.Authenticate("wrong"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := w.Authenticate(""); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("empty token: %v", err)
	}
	ignore, _ := os.ReadFile(filepath.Join(d, ".gitignore"))
	if !strings.Contains(string(ignore), ".auth.md") {
		t.Fatal("auth file not gitignored")
	}
	w2, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w2.Resolve(".auth"); ok {
		t.Fatal("auth file must not be indexed")
	}
}

func TestGitignoreNotDuplicated(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	if err := os.WriteFile(filepath.Join(d, ".gitignore"), []byte("/.auth.md\n"), 0644); err != nil {
		t.Fatal(err)
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateCredential("alice"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(d, ".gitignore"))
	if strings.Count(string(b), ".auth.md") != 1 {
		t.Fatalf("gitignore = %q", b)
	}
}

// A credential file that exists but cannot be read is an error. Reporting it as
// "no credentials" turned a corrupted file into a silent lockout.
func TestMalformedAuthFileIsAnError(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	if err := os.WriteFile(filepath.Join(d, ".auth.md"), []byte("credentials: nonsense\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuth(d); err == nil {
		t.Fatal("malformed auth file read as empty")
	}
	if _, err := w.Authenticate("jk_whatever"); err == nil || errors.Is(err, ErrAuthentication) {
		t.Fatalf("a broken credential store must surface, not look like a bad token: %v", err)
	}
}

func TestGroupCannotAuthenticate(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "team.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateCredential("team"); err == nil {
		t.Fatal("group credential should be rejected")
	}
}

func TestCapabilityStringIsTotal(t *testing.T) {
	if Admin.String() != "admin" || Read.String() != "read" {
		t.Fatal("known capabilities misnamed")
	}
	if Capability(99).String() == "" {
		t.Fatal("out-of-range capability must not panic or vanish")
	}
	if Capability(0).String() == "" {
		t.Fatal("zero capability must not panic or vanish")
	}
}
