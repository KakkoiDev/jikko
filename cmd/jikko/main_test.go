package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// cli runs one command with stdout and stderr captured. Commands write to the
// process streams, so these tests never run in parallel.
func cli(t *testing.T, fn func([]string) error, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	capture := func(target **os.File) func() string {
		r, w, perr := os.Pipe()
		if perr != nil {
			t.Fatal(perr)
		}
		old := *target
		*target = w
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			w.Close()
			*target = old
			return <-done
		}
	}
	restoreOut := capture(&os.Stdout)
	restoreErr := capture(&os.Stderr)
	err = fn(args)
	return restoreOut(), restoreErr(), err
}

// mustCLI runs a command that is expected to succeed and returns its stdout.
func mustCLI(t *testing.T, fn func([]string) error, args ...string) string {
	t.Helper()
	out, errOut, err := cli(t, fn, args...)
	if err != nil {
		t.Fatalf("%v: %v (stderr %q)", args, err, errOut)
	}
	return out
}

// team builds a workspace with an administrator, a reader, and a restricted
// page, and returns it with a token for each identity.
func team(t *testing.T) (dir, aliceToken, bobToken string) {
	t.Helper()
	t.Setenv("JIKKO_TOKEN", "")
	dir = workspace(t, map[string]string{
		"alice.md":      "---\ntype: identity\n---\n# Alice\n",
		"bob.md":        "---\ntype: identity\n---\n# Bob\n",
		"crew.md":       "---\ntype: identity\nmembers: [bob]\n---\n# Crew\n",
		"open.md":       "# Open\n\nSee [[tasks/ship]]. @crew please look.\n",
		"secret.md":     "---\npermissions:\n  read: crew\n  admin: alice\n---\n# Secret\n\nClassified.\n",
		"tasks/ship.md": "---\ntype: task\nstatus: todo\n---\n# Ship it\n",
		"board.md":      "---\ntype: view\nfilter:\n  type: task\n---\n",
	})
	aliceToken = strings.TrimSpace(mustCLI(t, auth, "create", "alice", "--dir", dir))
	bobToken = strings.TrimSpace(mustCLI(t, auth, "create", "bob", "--dir", dir))
	if !strings.HasPrefix(aliceToken, "jk_") || !strings.HasPrefix(bobToken, "jk_") {
		t.Fatalf("tokens = %q %q", aliceToken, bobToken)
	}
	return dir, aliceToken, bobToken
}

func TestUsageListsEveryCommand(t *testing.T) {
	out, _, _ := cli(t, func([]string) error { usage(); return nil })
	if strings.Contains(out, `\n`) {
		t.Fatalf("usage prints a literal escape sequence:\n%s", out)
	}
	for name := range commands {
		found := false
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
				found = true
			}
		}
		if !found {
			t.Errorf("usage does not list %q:\n%s", name, out)
		}
	}
}

func TestListFiltersByTypeStatusAndPermission(t *testing.T) {
	dir, _, bobToken := team(t)

	out := mustCLI(t, list, "--dir", dir)
	if strings.Contains(out, "secret.md") || !strings.Contains(out, "task      tasks/ship.md") {
		t.Fatalf("anonymous list:\n%s", out)
	}
	if out := mustCLI(t, list, "--dir", dir, "--token", bobToken); !strings.Contains(out, "secret.md") {
		t.Fatalf("reader list:\n%s", out)
	}

	var pages []map[string]any
	out = mustCLI(t, list, "--dir", dir, "--type", "task", "--status", "todo", "--json")
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0]["path"] != "tasks/ship.md" {
		t.Fatalf("filtered list = %s", out)
	}
	if out := mustCLI(t, list, "--dir", dir, "--status", "done", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list must be a JSON array, got %q", out)
	}

	if _, _, err := cli(t, list, "--dir", dir, "--type", "bogus"); err == nil {
		t.Fatal("unknown type accepted")
	}
	if _, _, err := cli(t, list, "--dir", dir, "extra"); err == nil {
		t.Fatal("stray argument accepted")
	}
	if _, _, err := cli(t, list, "--dir", dir, "--token", "jk_wrong"); err == nil {
		t.Fatal("bad token accepted")
	}
}

func TestShowReadsOneOrManyPages(t *testing.T) {
	dir, _, bobToken := team(t)

	out := mustCLI(t, show, "open", "--dir", dir)
	if !strings.HasPrefix(out, "Open\n\n# Open") {
		t.Fatalf("show text = %q", out)
	}
	out = mustCLI(t, show, "open", "tasks/ship", "--dir", dir)
	if !strings.Contains(out, "\n---\nShip it") {
		t.Fatalf("batch text = %q", out)
	}

	var one map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, show, "--json", "ship", "--dir", dir)), &one); err != nil {
		t.Fatal(err)
	}
	if one["type"] != "task" || one["path"] != "tasks/ship.md" {
		t.Fatalf("single JSON = %v", one)
	}
	var many []map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, show, "--json", "ship", "open", "--dir", dir)), &many); err != nil {
		t.Fatal(err)
	}
	if len(many) != 2 || many[0]["path"] != "tasks/ship.md" {
		t.Fatalf("batch JSON = %v", many)
	}

	// A forbidden page fails exactly like a missing one.
	_, _, forbidden := cli(t, show, "secret", "--dir", dir)
	_, _, missing := cli(t, show, "nothing", "--dir", dir)
	if forbidden == nil || missing == nil {
		t.Fatal("unreadable reference shown")
	}
	if strings.Replace(forbidden.Error(), "secret", "X", 1) != strings.Replace(missing.Error(), "nothing", "X", 1) {
		t.Fatalf("errors disclose existence: %q vs %q", forbidden, missing)
	}
	if out := mustCLI(t, show, "secret", "--dir", dir, "--token", bobToken); !strings.Contains(out, "Classified.") {
		t.Fatalf("reader show = %q", out)
	}
	if _, _, err := cli(t, show, "--dir", dir); err == nil {
		t.Fatal("show without a reference accepted")
	}
}

func TestSetAndPermRequireAuthenticationAndAuthority(t *testing.T) {
	dir, aliceToken, bobToken := team(t)

	if _, _, err := cli(t, set, "ship", "status", "done", "--dir", dir); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("anonymous set: %v", err)
	}
	if _, _, err := cli(t, set, "ship", "status", "--dir", dir); err == nil {
		t.Fatal("set with two arguments accepted")
	}
	// Flags may follow positional arguments.
	mustCLI(t, set, "ship", "status", "done", "--dir", dir, "--token", bobToken)
	if out := mustCLI(t, list, "--dir", dir, "--status", "done"); !strings.Contains(out, "tasks/ship.md") {
		t.Fatalf("status not updated:\n%s", out)
	}
	if _, _, err := cli(t, set, "secret", "status", "x", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("reader wrote a restricted page")
	}

	if _, _, err := cli(t, perm, "secret", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("perm without a capability accepted")
	}
	if _, _, err := cli(t, perm, "secret", "read", "bob", "--dir", dir); err == nil {
		t.Fatal("anonymous perm accepted")
	}
	if _, _, err := cli(t, perm, "secret", "admin", "bob", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("reader granted itself admin")
	}
	// JIKKO_TOKEN is the default credential.
	t.Setenv("JIKKO_TOKEN", aliceToken)
	mustCLI(t, perm, "secret", "write", "bob", "--dir", dir)
	t.Setenv("JIKKO_TOKEN", bobToken)
	mustCLI(t, set, "secret", "status", "edited", "--dir", dir)
	t.Setenv("JIKKO_TOKEN", "")

	mustCLI(t, auth, "revoke", "bob", "--dir", dir)
	if _, _, err := cli(t, set, "ship", "status", "todo", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("revoked token still mutates")
	}
}

func TestAuthUsageAndRefusals(t *testing.T) {
	dir, _, _ := team(t)
	for _, args := range [][]string{
		{"create"},
		{"rotate", "alice"},
		{"create", "crew"},
		{"create", "ghost"},
	} {
		if _, _, err := cli(t, auth, append(args, "--dir", dir)...); err == nil {
			t.Fatalf("auth %v accepted", args)
		}
	}
}

func TestAuthCreateWarnsWhenCredentialsAreTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("JIKKO_TOKEN", "")
	dir := workspace(t, map[string]string{
		"alice.md": "---\ntype: identity\n---\n# Alice\n",
		".auth.md": "---\ncredentials: []\n---\n",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "-f", ".auth.md"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	_, errOut, err := cli(t, auth, "create", "alice", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "tracked by Git") {
		t.Fatalf("no warning: %q", errOut)
	}
}

func TestCheckReportsProblemsAndUnresolvedReferences(t *testing.T) {
	t.Setenv("JIKKO_TOKEN", "")
	clean := workspace(t, map[string]string{"a.md": "# A\n[[b]]\n", "b.md": "# B\n"})
	if out := mustCLI(t, check, "--dir", clean); out != "ok: 2 pages\n" {
		t.Fatalf("clean check = %q", out)
	}

	dir := workspace(t, map[string]string{
		"a.md": "# A\n[[missing]] ![[gone]]\n",
		"v.md": "---\ntype: view\n---\n# body\n",
	})
	out, _, err := cli(t, check, "--dir", dir)
	if err == nil || !strings.Contains(err.Error(), "3 problem(s) in 2 pages") {
		t.Fatalf("check err = %v", err)
	}
	for _, want := range []string{`a.md: [reference] unresolved "missing"`, `a.md: [reference] unresolved "gone"`, "v.md: [structure] views must not contain"} {
		if !strings.Contains(out, want) {
			t.Fatalf("check output lacks %q:\n%s", want, out)
		}
	}

	out, _, err = cli(t, check, "--dir", dir, "--json")
	if err == nil {
		t.Fatal("problems must fail check in JSON mode too")
	}
	var report struct {
		Pages    int `json:"pages"`
		Problems []struct {
			Path, Kind, Message string
		} `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if report.Pages != 2 || len(report.Problems) != 3 {
		t.Fatalf("report = %+v", report)
	}
	if _, _, err := cli(t, check, "--dir", dir, "extra"); err == nil {
		t.Fatal("stray argument accepted")
	}
	if _, _, err := cli(t, check, "--dir", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing workspace accepted")
	}
}

func TestTreeAndMentionsArePermissionFiltered(t *testing.T) {
	dir, _, bobToken := team(t)

	var entries []map[string]string
	if err := json.Unmarshal([]byte(mustCLI(t, tree, "--dir", dir, "--json")), &entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e["path"] == "secret.md" {
			t.Fatal("tree disclosed a forbidden path")
		}
	}
	if out := mustCLI(t, tree, "--dir", dir, "--token", bobToken); !strings.Contains(out, "secret.md") || !strings.Contains(out, "view      board.md") {
		t.Fatalf("reader tree:\n%s", out)
	}
	if _, _, err := cli(t, tree, "--dir", dir, "x"); err == nil {
		t.Fatal("stray argument accepted")
	}

	if _, _, err := cli(t, mentions, "--dir", dir); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("anonymous mentions: %v", err)
	}
	if out := mustCLI(t, mentions, "--dir", dir, "--token", bobToken); out != "open.md\n" {
		t.Fatalf("mentions = %q", out)
	}
	var pages []map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, mentions, "--dir", dir, "--token", bobToken, "--json")), &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0]["path"] != "open.md" {
		t.Fatalf("mentions JSON = %v", pages)
	}
}

func TestCreateDocumentsAndTasks(t *testing.T) {
	dir, aliceToken, _ := team(t)

	if _, _, err := cli(t, create, "notes/x", "--dir", dir, "--body", "# X\n"); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("anonymous create: %v", err)
	}
	if _, _, err := cli(t, create, "notes/x", "--dir", dir, "--type", "view", "--token", aliceToken); err == nil {
		t.Fatal("view creation accepted")
	}
	if _, _, err := cli(t, create, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("create without a path accepted")
	}
	mustCLI(t, create, "notes/x", "--dir", dir, "--body", "# X\n", "--token", aliceToken)
	mustCLI(t, create, "tasks/next", "--dir", dir, "--type", "task", "--body", "# Next\n", "--token", aliceToken)

	b, err := os.ReadFile(filepath.Join(dir, "tasks", "next.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "---\ntype: task\nstatus: todo\n---\n# Next\n" {
		t.Fatalf("task source = %q", b)
	}
	if out := mustCLI(t, list, "--dir", dir, "--type", "task", "--status", "todo"); !strings.Contains(out, "tasks/next.md") {
		t.Fatalf("created task not listed:\n%s", out)
	}
	if _, _, err := cli(t, create, "../escape", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestExportWritesPortableZIP(t *testing.T) {
	dir, _, _ := team(t)
	out := filepath.Join(t.TempDir(), "w.zip")
	mustCLI(t, exportWorkspace, "--dir", dir, "--output", out)
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	got := strings.Join(names, ",")
	if strings.Contains(got, ".auth.md") || !strings.Contains(got, "secret.md") || !strings.Contains(got, ".gitignore") {
		t.Fatalf("entries = %s", got)
	}
	if _, _, err := cli(t, exportWorkspace, "--dir", dir, "extra"); err == nil {
		t.Fatal("stray argument accepted")
	}
}

func TestCommitAttributesTheAuthenticatedActor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, aliceToken, _ := team(t)
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, _, err := cli(t, commit, "--dir", dir, "message"); err == nil {
		t.Fatal("anonymous commit accepted")
	}
	if _, _, err := cli(t, commit, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("commit without a message accepted")
	}
	mustCLI(t, commit, "Import", "workspace", "--operation", "import", "--dir", dir, "--token", aliceToken)
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%s%n%(trailers:only,unfold)").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Import workspace", "Jikko-Actor: alice", "Jikko-Operation: import"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("commit lacks %q:\n%s", want, out)
		}
	}
	files, err := exec.Command("git", "-C", dir, "ls-files").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files), ".auth.md") {
		t.Fatal("credential file committed")
	}
}

func TestFlagErrors(t *testing.T) {
	t.Setenv("JIKKO_TOKEN", "")
	for name, fn := range commands {
		if name == "serve" {
			continue
		}
		if _, _, err := cli(t, fn, "--no-such-flag"); err == nil {
			t.Errorf("%s accepted an unknown flag", name)
		}
	}
	if _, _, err := cli(t, serve, "extra"); err == nil {
		t.Fatal("serve accepted a stray argument")
	}
}

func TestCommentCommands(t *testing.T) {
	dir, aliceToken, bobToken := team(t)

	id := strings.TrimSpace(mustCLI(t, comment, "add", "open", "please look", "Looking now.", "--dir", dir, "--token", bobToken))
	if id != "c1" {
		t.Fatalf("id = %q", id)
	}
	b, err := os.ReadFile(filepath.Join(dir, "open.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<!--comment:c1-->please look<!--/comment:c1-->") || !strings.Contains(string(b), "<!--comment-thread:c1\n@bob: Looking now.\n-->") {
		t.Fatalf("open.md = %q", b)
	}
	mustCLI(t, comment, "reply", "open", "c1", "Thanks.", "--dir", dir, "--token", aliceToken)

	// bob may read secret.md but not comment on it.
	if _, _, err := cli(t, comment, "add", "secret", "Classified", "Why?", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader commented")
	}
	if _, _, err := cli(t, comment, "add", "open", "See", "x", "--dir", dir); err == nil {
		t.Fatal("an anonymous caller commented")
	}
	var added map[string]string
	out := mustCLI(t, comment, "add", "secret", "Classified", "Why?", "--dir", dir, "--token", aliceToken, "--json")
	if err := json.Unmarshal([]byte(out), &added); err != nil || added["id"] != "c1" {
		t.Fatalf("add --json = %q", out)
	}

	// Listing is filtered by read permission.
	anonymous := mustCLI(t, comment, "list", "--dir", dir)
	if !strings.Contains(anonymous, "open.md#c1") || !strings.Contains(anonymous, "@alice: Thanks.") || strings.Contains(anonymous, "secret.md") {
		t.Fatalf("anonymous list:\n%s", anonymous)
	}
	var listed []struct {
		ID, Path string
		Entries  []struct{ Author, Text string }
	}
	out = mustCLI(t, comment, "list", "secret", "--dir", dir, "--token", bobToken, "--json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != "secret.md" || listed[0].Entries[0].Author != "alice" {
		t.Fatalf("filtered list = %s", out)
	}
	if _, _, err := cli(t, comment, "list", "secret", "--dir", dir); err == nil {
		t.Fatal("anonymous listing of a restricted page")
	}

	if _, _, err := cli(t, comment, "resolve", "secret", "c1", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader resolved")
	}
	mustCLI(t, comment, "resolve", "open", "c1", "--dir", dir, "--token", bobToken)
	if b, _ := os.ReadFile(filepath.Join(dir, "open.md")); string(b) != "# Open\n\nSee [[tasks/ship]]. @crew please look.\n" {
		t.Fatalf("resolved open.md = %q", b)
	}

	for _, args := range [][]string{{}, {"bogus"}, {"add", "open"}, {"resolve", "open"}, {"list", "a", "b"}} {
		if _, _, err := cli(t, comment, append(args, "--dir", dir, "--token", aliceToken)...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

// A Task marked done with unresolved comments warns on set and in check, but
// neither refuses it.
func TestDoneTaskWithCommentsWarns(t *testing.T) {
	dir, aliceToken, _ := team(t)
	mustCLI(t, comment, "add", "tasks/ship", "Ship it", "Tests first?", "--dir", dir, "--token", aliceToken)
	_, errOut, err := cli(t, set, "tasks/ship", "status", "done", "--dir", dir, "--token", aliceToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "warning: tasks/ship.md: task is done but has 1 unresolved comment(s)") {
		t.Fatalf("set stderr = %q", errOut)
	}
	out, _, err := cli(t, check, "--dir", dir)
	if err != nil {
		t.Fatalf("a warning failed check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tasks/ship.md: warning: [review]") {
		t.Fatalf("check output:\n%s", out)
	}
	var report struct {
		Warnings []struct{ Path string } `json:"warnings"`
	}
	out = mustCLI(t, check, "--dir", dir, "--json")
	if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Warnings) != 1 {
		t.Fatalf("check --json = %q", out)
	}
	if out := mustCLI(t, show, "tasks/ship", "--dir", dir); !strings.HasPrefix(out, "Ship it\n") {
		t.Fatalf("title kept the markers: %q", out)
	}
}
