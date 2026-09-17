package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTest(t *testing.T, root, name, content string) {
	t.Helper(); path := filepath.Join(root, name); if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }; if err := os.WriteFile(path, []byte(content), 0644); err != nil { t.Fatal(err) }
}

func TestIdentityMembershipAndPermissions(t *testing.T) {
	d := t.TempDir()
	writeTest(t,d,"alice.md","---\ntype: identity\n---\n# Alice\n")
	writeTest(t,d,"bob.md","---\ntype: identity\n---\n# Bob\n")
	writeTest(t,d,"engineering.md","---\ntype: identity\nmembers: [alice]\n---\n# Engineering\n")
	writeTest(t,d,"staff.md","---\ntype: identity\nmembers: [engineering]\n---\n# Staff\n")
	writeTest(t,d,"secret.md","---\npermissions:\n  read: staff\n  write: engineering\n  admin: bob\n---\n# Secret\n")
	w, err := Open(d); if err != nil { t.Fatal(err) }; if err := w.ValidatePermissions(); err != nil { t.Fatal(err) }
	if !w.MemberOf("alice", "staff") { t.Fatal("expected transitive membership") }
	p, _ := w.Resolve("secret")
	if !w.Allowed("alice", p, Read) || !w.Allowed("alice", p, Write) { t.Fatal("alice should inherit engineering write/read") }
	if w.Allowed("alice", p, Admin) { t.Fatal("write must not imply admin") }
	if !w.Allowed("bob", p, Admin) || !w.Allowed("bob", p, Read) { t.Fatal("admin must imply read") }
}

func TestOpenByDefault(t *testing.T) {
	d := t.TempDir(); writeTest(t,d,"doc.md","# Open\n"); w, err := Open(d); if err != nil { t.Fatal(err) }; p,_ := w.Resolve("doc")
	if !w.Allowed("nobody", p, Write) { t.Fatal("file without permissions should be open by Jikko") }
}

func TestIdentityCycleRejected(t *testing.T) {
	d := t.TempDir(); writeTest(t,d,"a.md","---\ntype: identity\nmembers: [b]\n---\n"); writeTest(t,d,"b.md","---\ntype: identity\nmembers: [a]\n---\n")
	if _, err := Open(d); err == nil { t.Fatal("expected membership cycle error") }
}

func TestDanglingPermissionRejected(t *testing.T) {
	d := t.TempDir(); writeTest(t,d,"doc.md","---\npermissions:\n  read: missing\n---\n# Doc\n"); w, err := Open(d); if err != nil { t.Fatal(err) }
	if err := w.ValidatePermissions(); err == nil { t.Fatal("expected dangling permission error") }
}

func TestTokenAuthentication(t *testing.T) {
	d := t.TempDir(); writeTest(t,d,"alice.md","---\ntype: identity\n---\n# Alice\n"); w, err := Open(d); if err != nil { t.Fatal(err) }
	token, err := w.CreateCredential("alice"); if err != nil { t.Fatal(err) }
	if !strings.HasPrefix(token,"jk_") { t.Fatal("unexpected token format") }
	b, err := os.ReadFile(filepath.Join(d,".auth.md")); if err != nil { t.Fatal(err) }
	if strings.Contains(string(b), token) { t.Fatal("plaintext token persisted") }
	if p, ok := w.Authenticate(token); !ok || p.Title != "Alice" { t.Fatal("token did not authenticate") }
	if _, ok := w.Authenticate("wrong"); ok { t.Fatal("wrong token authenticated") }
	ignore, _ := os.ReadFile(filepath.Join(d,".gitignore")); if !strings.Contains(string(ignore), ".auth.md") { t.Fatal("auth file not gitignored") }
	w2, err := Open(d); if err != nil { t.Fatal(err) }; if _, ok := w2.Resolve(".auth"); ok { t.Fatal("auth file must not be indexed") }
}

func TestGroupCannotAuthenticate(t *testing.T) {
	d := t.TempDir(); writeTest(t,d,"alice.md","---\ntype: identity\n---\n"); writeTest(t,d,"team.md","---\ntype: identity\nmembers: [alice]\n---\n"); w, err := Open(d); if err != nil { t.Fatal(err) }
	if _, err := w.CreateCredential("team"); err == nil { t.Fatal("group credential should be rejected") }
}
