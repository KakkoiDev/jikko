package jikko

import "testing"

func TestAuthorizationBearingGroupMutationRequiresAdmin(t *testing.T) {
	d := t.TempDir()
	writeTest(t,d,"alice.md","---\ntype: identity\n---\n")
	writeTest(t,d,"bob.md","---\ntype: identity\n---\n")
	writeTest(t,d,"engineering.md","---\ntype: identity\nmembers: [alice]\n---\n")
	writeTest(t,d,"secret.md","---\npermissions:\n  write: engineering\n  admin: bob\n---\n# Secret\n")
	w, err := Open(d); if err != nil { t.Fatal(err) }
	if err := w.CanChangeMembers("alice","engineering"); err == nil { t.Fatal("writer must not mutate authorization-bearing group") }
	if err := w.CanChangeMembers("bob","engineering"); err != nil { t.Fatalf("admin should be allowed: %v", err) }
}
