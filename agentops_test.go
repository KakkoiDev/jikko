package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestPage(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestTreeFiltersForbiddenPages(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "malrec.md", "---\ntype: identity\n---\n# Malrec\n")
	writeTestPage(t, root, "public.md", "# Public\n")
	writeTestPage(t, root, "secret.md", "---\npermissions:\n  read: malrec\n  admin: malrec\n---\n# Secret\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got := w.AccessiblePaths("cassian")
	for _, p := range got {
		if p == "secret.md" {
			t.Fatal("tree leaked forbidden page")
		}
	}
	if len(got) != 3 {
		t.Fatalf("got paths %v", got)
	}
}

func TestReadManyPreservesOrderAndPermissions(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "a.md", "# A\n")
	writeTestPage(t, root, "b.md", "# B\n")
	w, _ := Open(root)
	got, err := w.ReadMany("cassian", "b", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != "b.md" || got[1].Path != "a.md" {
		t.Fatalf("wrong order: %v %v", got[0].Path, got[1].Path)
	}
}

func TestMentionsIncludesTransitiveGroup(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	writeTestPage(t, root, "north.md", "---\ntype: identity\nmembers: [cassian]\n---\n# North\n")
	writeTestPage(t, root, "alliance.md", "---\ntype: identity\nmembers: [north]\n---\n# Alliance\n")
	writeTestPage(t, root, "message.md", "# Message\n@alliance defend Sol.\n")
	writeTestPage(t, root, "other.md", "# Other\n@malrec hello.\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got := w.Mentions("cassian")
	found := false
	for _, p := range got {
		if p.Path == "message.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("transitive group mention missing: %#v", got)
	}
}

func TestCreatePageValidatesAndRefreshesWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "cassian.md", "---\ntype: identity\n---\n# Cassian\n")
	w, _ := Open(root)
	if err := w.CreatePage("cassian", "memory/talos", "# Talos\nNever forget.\n"); err != nil {
		t.Fatal(err)
	}
	p, ok := w.Resolve("memory/talos")
	if !ok || p.Title != "Talos" {
		t.Fatalf("created page not indexed: %#v %v", p, ok)
	}
	if err := w.CreatePage("cassian", "../escape.md", "# no\n"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

// Creating a page is a mutation like any other and is judged by its effect. A
// grant naming an identity that does not exist yet is skipped, but creating
// that identity -- with the creator as a member -- used to activate it.
func TestCreatePageCannotActivateDanglingGrant(t *testing.T) {
	cases := map[string]string{
		"dangling grant subject": "---\npermissions:\n  admin: [bob, ghost]\n---\n# Secret\n",
		"dangling group member":  "---\npermissions:\n  admin: admins\n---\n# Secret\n",
	}
	for name, secret := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTestPage(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
			writeTestPage(t, root, "mallory.md", "---\ntype: identity\n---\n# Mallory\n")
			writeTestPage(t, root, "admins.md", "---\ntype: identity\nmembers: [bob, ghost]\n---\n# Admins\n")
			writeTestPage(t, root, "secret.md", secret)
			w, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			err = w.CreatePage("mallory", "ghost", "---\ntype: identity\nmembers: [mallory]\n---\n# Ghost\n")
			if err == nil || !strings.Contains(err.Error(), "may not administer") {
				t.Fatalf("escalation through creation accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "ghost.md")); !os.IsNotExist(err) {
				t.Fatal("rejected page was left on disk")
			}
			if w.Allowed("mallory", w.Pages["secret.md"], Read) {
				t.Fatal("mallory gained access")
			}
			// An administrator of the affected file may create the same identity.
			if err := w.CreatePage("bob", "ghost", "---\ntype: identity\nmembers: [mallory]\n---\n# Ghost\n"); err != nil {
				t.Fatalf("administrator refused: %v", err)
			}
		})
	}
}

// The creator administers what it creates, including a restricted page that
// names other identities, and creating ordinary identities is not escalation.
func TestCreatePageGrantsOnTheNewPageAreAllowed(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTestPage(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	writeTestPage(t, root, "private.md", "---\npermissions:\n  admin: bob\n---\n# Private\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreatePage("alice", "shared", "---\npermissions:\n  read: bob\n  admin: alice\n---\n# Shared\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.CreatePage("alice", "people/carol", "---\ntype: identity\n---\n# Carol\n"); err != nil {
		t.Fatal(err)
	}
	if w.Allowed("people/carol", w.Pages["private.md"], Read) {
		t.Fatal("new identity reached a restricted file")
	}
	if err := w.CreatePage("alice", "shared", "# again\n"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite accepted: %v", err)
	}
	if err := w.CreatePage("alice", "bad", "---\ntype: view\n---\n# body\n"); err == nil {
		t.Fatal("page introducing a problem accepted")
	}
	if err := w.CreatePage("nobody", "x", "# x\n"); err == nil {
		t.Fatal("unauthenticated creation accepted")
	}
}

// A symbolic link to a directory outside the workspace used to let creation
// write straight through it.
func TestCreatePageStaysInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestPage(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"link/evil", "link/deeper/evil", "/etc/evil", "a/../../evil", "notes.txt"} {
		if err := w.CreatePage("alice", p, "# evil\n"); err == nil {
			t.Fatalf("CreatePage(%q) accepted", p)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("created outside the workspace: %v", entries)
	}
}

// Harness state is not source: a page there would be invisible to Open, and
// writing .auth.md would mint credentials.
func TestCreatePageRefusesHarnessState(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".auth.md", "sub/.AUTH.md", ".git/hooks/x", ".data/cache", "a/.Git/x"} {
		if err := w.CreatePage("alice", p, "---\ncredentials: []\n---\n"); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("CreatePage(%q) = %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".auth.md")); !os.IsNotExist(err) {
		t.Fatal("credential file written")
	}
}

func mentionPaths(w *Workspace, actor string) []string {
	var out []string
	for _, p := range w.Mentions(actor) {
		out = append(out, p.Path)
	}
	return out
}

// "Thanks @alice." is the ordinary way to end a sentence; the full stop used
// to become part of the name, so the mention reached nobody.
func TestMentionsIgnoreTrailingPunctuation(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTestPage(t, root, "people/bob.md", "---\ntype: identity\n---\n# Bob\n")
	writeTestPage(t, root, "end.md", "# End\nThanks @alice.\n")
	writeTestPage(t, root, "path.md", "# Path\nAsk @people/bob.\n")
	writeTestPage(t, root, "comma.md", "# Comma\n(@alice, @people/bob)\n")
	writeTestPage(t, root, "email.md", "# Email\nmail alice@alice.example or x@alice\n")
	writeTestPage(t, root, "code.md", "# Code\n`@alice` and\n```\n@alice\n```\n")
	writeTestPage(t, root, "other.md", "# Other\n@alicex @alice_b\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(mentionPaths(w, "alice"), ","); got != "comma.md,end.md" {
		t.Fatalf("alice mentions = %s", got)
	}
	if got := strings.Join(mentionPaths(w, "people/bob"), ","); got != "comma.md,path.md" {
		t.Fatalf("bob mentions = %s", got)
	}
	if got := w.Mentions("nobody"); got != nil {
		t.Fatalf("unknown identity has mentions: %v", got)
	}
}

// A bare name shared by two identities resolves to neither, so it must not
// count as a mention of both.
func TestAmbiguousMentionAddressesNobody(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "people/sam.md", "---\ntype: identity\n---\n# Sam\n")
	writeTestPage(t, root, "bots/sam.md", "---\ntype: identity\n---\n# Sam bot\n")
	writeTestPage(t, root, "bare.md", "# Bare\n@sam hi\n")
	writeTestPage(t, root, "full.md", "# Full\n@people/sam hi\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(mentionPaths(w, "people/sam"), ","); got != "full.md" {
		t.Fatalf("people/sam mentions = %s", got)
	}
	if got := mentionPaths(w, "bots/sam"); len(got) != 0 {
		t.Fatalf("bots/sam mentions = %v", got)
	}
}

// Mentions are permission-filtered: a forbidden page never surfaces, even when
// it addresses the caller.
func TestMentionsRespectReadPermission(t *testing.T) {
	root := t.TempDir()
	writeTestPage(t, root, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTestPage(t, root, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	writeTestPage(t, root, "s.md", "---\npermissions:\n  admin: bob\n---\n# S\n@alice see this\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := mentionPaths(w, "alice"); len(got) != 0 {
		t.Fatalf("forbidden page surfaced: %v", got)
	}
}
