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

// Deleting an identity file used to make its credentials unrevocable, and they
// authenticated again as soon as an identity of that name reappeared.
func TestRevokeCredentialsOfDeletedIdentity(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	aliceToken, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	bobToken, err := w.CreateCredential("bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d, "alice.md")); err != nil {
		t.Fatal(err)
	}
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Authenticate(aliceToken); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("credential of a deleted identity: %v", err)
	}
	if err := w.RevokeCredentials("alice"); err != nil {
		t.Fatalf("orphaned credential not revocable: %v", err)
	}
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# New Alice\n")
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Authenticate(aliceToken); err == nil {
		t.Fatal("revoked token authenticated the recreated identity")
	}
	if p, err := w.Authenticate(bobToken); err != nil || p.Path != "bob.md" {
		t.Fatalf("unrelated credential lost: %v", err)
	}
	if err := w.RevokeCredentials("ghost"); err == nil {
		t.Fatal("revoking an unknown name with no credentials must be reported")
	}
	if err := w.RevokeCredentials("bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Authenticate(bobToken); err == nil {
		t.Fatal("revoked token still authenticates")
	}
}

// A credential is stored under the identity's path, so a page that later makes
// the bare name ambiguous does not redirect it to someone else.
func TestCredentialBoundToIdentityPath(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "people/alice.md", "---\ntype: identity\n---\n# Alice\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadAuth(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Credentials) != 1 || a.Credentials[0].Identity != "people/alice" {
		t.Fatalf("credentials = %#v", a.Credentials)
	}
	writeTest(t, d, "bots/alice.md", "---\ntype: identity\n---\n# Alice bot\n")
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	actor, err := w.ActorForToken(token)
	if err != nil || actor != "people/alice" {
		t.Fatalf("actor = %q, %v", actor, err)
	}
}

// A credential whose identity became a group no longer authenticates.
func TestCredentialOfIdentityTurnedGroup(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	token, err := w.CreateCredential("alice")
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, d, "alice.md", "---\ntype: identity\nmembers: [bob]\n---\n# Alice\n")
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Authenticate(token); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("group authenticated: %v", err)
	}
	if _, err := w.CreateCredential("missing"); err == nil {
		t.Fatal("credential for a missing identity")
	}
}

func TestLoadAuthRejectsIncompleteCredential(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, ".auth.md"), []byte("---\ncredentials:\n  - identity: alice\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuth(d); err == nil || !strings.Contains(err.Error(), "missing identity or token_hash") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, ".auth.md"), []byte("---\ncredentials: [\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuth(d); err == nil {
		t.Fatal("invalid YAML accepted")
	}
}

func TestAuthTrackedByGit(t *testing.T) {
	root := gitWorkspace(t)
	if AuthTrackedByGit(root) {
		t.Fatal("untracked file reported as tracked")
	}
	write(t, root, ".auth.md", "---\ncredentials: []\n---\n")
	gitOutput(t, root, "add", "-f", ".auth.md")
	if !AuthTrackedByGit(root) {
		t.Fatal("tracked credential file not detected")
	}
}

func TestParseCapability(t *testing.T) {
	for in, want := range map[string]Capability{"read": Read, "COMMENT": Comment, "Write": Write, "admin": Admin} {
		if got, ok := ParseCapability(in); !ok || got != want {
			t.Fatalf("ParseCapability(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := ParseCapability("owner"); ok {
		t.Fatal("unknown capability accepted")
	}
}

// Every capability below a grant is included, and grants through several
// groups are additive.
func TestCapabilityHierarchyAndAdditiveGrants(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "readers.md", "---\ntype: identity\nmembers: [alice]\n---\n")
	writeTest(t, d, "writers.md", "---\ntype: identity\nmembers: alice\n---\n")
	writeTest(t, d, "s.md", "---\npermissions:\n  read: readers\n  write: [ghost, writers]\n  bogus: alice\n---\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	p := w.Pages["s.md"]
	for c, want := range map[Capability]bool{Read: true, Comment: true, Write: true, Admin: false} {
		if got := w.Allowed("alice", p, c); got != want {
			t.Fatalf("alice %v = %v, want %v", c, got, want)
		}
	}
	if w.Allowed("readers", p, Read) {
		t.Fatal("a group is never the acting identity")
	}
	if w.Allowed("alice", nil, Read) {
		t.Fatal("nil page allowed")
	}
	if !problem(w, "s.md", `unknown permission "bogus"`) || !problem(w, "s.md", `unresolved permission identity "ghost"`) {
		t.Fatalf("problems = %#v", w.Problems)
	}
	if got := w.Administrators(p); len(got) != 0 {
		t.Fatalf("administrators = %v", got)
	}
	if err := w.CanChangeMembers("alice", "alice"); err == nil {
		t.Fatal("an individual is not a group")
	}
	if err := w.CanChangeMembers("alice", "missing"); err == nil {
		t.Fatal("missing group accepted")
	}
}

// A credential id names one credential: it re-checks while the credential is
// stored, fails once it is revoked, and is not itself a bearer token.
func TestCredentialIdentityFollowsRevocation(t *testing.T) {
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
	p, id, err := w.AuthenticateCredential(token)
	if err != nil || p.Path != "alice.md" || id == "" || strings.Contains(id, token) {
		t.Fatalf("AuthenticateCredential = %v %q %v", p, id, err)
	}
	if p, err := w.CredentialIdentity(id); err != nil || p.Path != "alice.md" {
		t.Fatalf("CredentialIdentity = %v %v", p, err)
	}
	if _, err := w.Authenticate(id); !errors.Is(err, ErrAuthentication) {
		t.Fatal("a credential id authenticated as a token")
	}
	if _, err := w.CredentialIdentity(""); !errors.Is(err, ErrAuthentication) {
		t.Fatal("empty credential id accepted")
	}
	if err := w.RevokeCredentials("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.CredentialIdentity(id); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("revoked credential re-checked as valid: %v", err)
	}
}
