package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
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
		if _, ok := synopses[name]; !ok {
			t.Errorf("command %q has no synopsis", name)
		}
	}
	for name := range synopses {
		if _, ok := commands[name]; !ok {
			t.Errorf("synopsis %q names no command", name)
		}
	}
}

// help, --help, and -h exit 0 with a synopsis; a wrong invocation or an
// unknown command exits 2; a failed command exits 1.
func TestHelpAndExitCodes(t *testing.T) {
	t.Setenv("JIKKO_TOKEN", "")
	dir := workspace(t, map[string]string{"a.md": "# A\n"})
	for name := range commands {
		if name == "help" {
			continue
		}
		out, errOut, err := cli(t, commands[name], "--help")
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%s --help: %v", name, err)
		}
		if !strings.HasPrefix(out, "usage: jikko "+name) || !strings.Contains(out, "\nFlags:\n") || !strings.Contains(out, "-dir string") || errOut != "" {
			t.Errorf("%s --help output:\n%s%s", name, out, errOut)
		}
		if viaHelp := mustCLI(t, help, name); viaHelp != out {
			t.Errorf("help %s differs from %s --help:\n%s\n%s", name, name, viaHelp, out)
		}
	}
	for _, c := range []struct {
		args []string
		code int
	}{
		{nil, 0}, {[]string{"help"}, 0}, {[]string{"--help"}, 0}, {[]string{"-h"}, 0}, {[]string{"help", "list"}, 0},
		{[]string{"version"}, 0}, {[]string{"list", "-h", "--dir", dir}, 0},
		{[]string{"bogus"}, 2}, {[]string{"help", "bogus"}, 2}, {[]string{"list", "--bogus", "--dir", dir}, 2},
		{[]string{"set", "a", "--dir", dir}, 2}, {[]string{"show", "a", "b", "--raw", "--dir", dir}, 2},
		{[]string{"show", "nothing", "--dir", dir}, 1}, {[]string{"check", "--dir", filepath.Join(dir, "missing")}, 1},
	} {
		var code int
		out, errOut, _ := cli(t, func([]string) error { code = run(c.args); return nil })
		if code != c.code {
			t.Errorf("jikko %v exited %d, want %d:\n%s%s", c.args, code, c.code, out, errOut)
		}
		if code == 2 && !strings.Contains(errOut, "jikko") {
			t.Errorf("jikko %v gave no diagnostic on stderr", c.args)
		}
	}
	if out := mustCLI(t, printVersion); out != "jikko dev\n" {
		t.Fatalf("version = %q", out)
	}
	if _, _, err := cli(t, printVersion, "extra"); err == nil {
		t.Fatal("version accepted an argument")
	}
	_, errOut, _ := cli(t, func([]string) error { run([]string{"frobnicate"}); return nil })
	if !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("unknown command stderr = %q", errOut)
	}
	// Commands that act as nobody do not accept a token they would ignore.
	for _, fn := range []func([]string) error{auth, check, exportWorkspace, serve, initWorkspace} {
		if _, _, err := cli(t, fn, "--token", "x", "--dir", dir); err == nil || !strings.Contains(err.Error(), "-token") {
			t.Errorf("a token was accepted: %v", err)
		}
	}
}

func TestWorkspaceDirectoryErrorsAreFriendly(t *testing.T) {
	t.Setenv("JIKKO_TOKEN", "")
	missing := filepath.Join(t.TempDir(), "nope")
	_, _, err := cli(t, list, "--dir", missing)
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "jikko init") {
		t.Fatalf("missing dir: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(file, []byte("# F\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(t, list, "--dir", file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file as dir: %v", err)
	}
	dir, aliceToken, _ := team(t)
	_, _, err = cli(t, create, "x", "--file", filepath.Join(dir, "absent.md"), "--dir", dir, "--token", aliceToken)
	if err == nil || !strings.HasPrefix(err.Error(), "cannot read ") || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("unreadable body: %v", err)
	}
	if _, _, err := cli(t, upload, filepath.Join(dir, "absent.png"), "--dir", dir, "--token", aliceToken); err == nil || !strings.HasPrefix(err.Error(), "cannot read ") {
		t.Fatalf("unreadable upload: %v", err)
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
	if out := mustCLI(t, mentions, "--dir", dir, "--token", bobToken); out != "open.md:3: See [[tasks/ship]]. @crew please look.\n" {
		t.Fatalf("mentions = %q", out)
	}
	var pages []struct {
		Path    string
		Matches []struct {
			Line    int
			Text    string
			Comment string
		}
	}
	if err := json.Unmarshal([]byte(mustCLI(t, mentions, "--dir", dir, "--token", bobToken, "--json")), &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Path != "open.md" || len(pages[0].Matches) != 1 || pages[0].Matches[0].Line != 3 || pages[0].Matches[0].Comment != "" {
		t.Fatalf("mentions JSON = %+v", pages)
	}
	// A mention made in a comment names its thread.
	mustCLI(t, comment, "add", "tasks/ship", "Ship it", "@bob can you?", "--dir", dir, "--token", bobToken)
	if err := json.Unmarshal([]byte(mustCLI(t, mentions, "--dir", dir, "--token", bobToken, "--json")), &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[1].Path != "tasks/ship.md" || pages[1].Matches[0].Comment != "c1" || pages[1].Matches[0].Line != 8 {
		t.Fatalf("comment mention = %+v", pages)
	}
	// An identity nobody mentions gets an empty array, not null.
	if out := mustCLI(t, mentions, "--dir", dir, "--token", strings.TrimSpace(mustCLI(t, auth, "create", "alice", "--dir", dir)), "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty mentions = %q", out)
	}
}

func TestMentionsSince(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, aliceToken, bobToken := team(t)
	// Without Git --since cannot be honoured: every mention is listed, with a warning.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	out, errOut, err := cli(t, mentions, "--since", "HEAD", "--dir", dir, "--token", bobToken)
	if err != nil || !strings.Contains(out, "open.md:3") || !strings.Contains(errOut, "not Git-backed") {
		t.Fatalf("no-git fallback: %v %q %q", err, out, errOut)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", "")
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	mustCLI(t, commit, "Import", "--dir", dir, "--token", aliceToken)
	if out := mustCLI(t, mentions, "--since", "HEAD", "--dir", dir, "--token", bobToken); out != "" {
		t.Fatalf("nothing changed, yet mentions = %q", out)
	}
	mustCLI(t, create, "notes/new", "--body", "# New\n\n@bob look.\n", "--dir", dir, "--token", aliceToken)
	if out := mustCLI(t, mentions, "--since", "HEAD", "--dir", dir, "--token", bobToken); out != "notes/new.md:3: @bob look.\n" {
		t.Fatalf("mentions since HEAD = %q", out)
	}
	if _, _, err := cli(t, mentions, "--since", "nosuchrev", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("an unknown revision was accepted")
	}
}

func TestListAssignee(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("tasks/mine.md", "---\ntype: task\nstatus: todo\nassignee: bob\n---\n# Mine\n")
	write("tasks/ours.md", "---\ntype: task\nstatus: doing\nassignee: [crew]\npermissions:\n  read: alice\n  admin: alice\n---\n# Ours\n")
	if out := mustCLI(t, list, "--assignee", "bob", "--dir", dir); out != "task      tasks/mine.md\n" {
		t.Fatalf("anonymous list by assignee:\n%s", out)
	}
	// bob is in crew, but may not read tasks/ours.md.
	if out := mustCLI(t, list, "--assignee", "me", "--dir", dir, "--token", bobToken); out != "task      tasks/mine.md\n" {
		t.Fatalf("bob's tasks:\n%s", out)
	}
	if out := mustCLI(t, list, "--assignee", "crew", "--dir", dir, "--token", aliceToken); out != "task      tasks/ours.md\n" {
		t.Fatalf("crew's tasks:\n%s", out)
	}
	if out := mustCLI(t, list, "--assignee", "me", "--status", "done", "--json", "--dir", dir, "--token", bobToken); strings.TrimSpace(out) != "[]" {
		t.Fatalf("filtered list = %q", out)
	}
	if _, _, err := cli(t, list, "--assignee", "me", "--dir", dir); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("anonymous --assignee me: %v", err)
	}
	if _, _, err := cli(t, list, "--assignee", "ghost", "--dir", dir); err == nil {
		t.Fatal("unknown assignee accepted")
	}
}

func TestCreateDocumentsAndTasks(t *testing.T) {
	dir, aliceToken, _ := team(t)

	if _, _, err := cli(t, create, "notes/x", "--dir", dir, "--body", "# X\n"); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("anonymous create: %v", err)
	}
	if _, _, err := cli(t, create, "notes/x", "--dir", dir, "--type", "bogus", "--token", aliceToken); err == nil {
		t.Fatal("unknown type accepted")
	}
	if _, _, err := cli(t, create, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("create without a path accepted")
	}
	if _, _, err := cli(t, create, "notes/x", "--body", "a", "--file", "b", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("--body and --file together accepted")
	}
	if out := mustCLI(t, create, "notes/x", "--dir", dir, "--body", "# X\n", "--token", aliceToken); out != "created notes/x.md\n" {
		t.Fatalf("create output = %q", out)
	}
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
	// A task whose body carries its own frontmatter keeps it.
	if _, _, err := cli(t, create, "tasks/next", "--dir", dir, "--type", "task", "--body", "# Again\n", "--token", aliceToken); err == nil {
		t.Fatal("an existing page was overwritten")
	}
}

func TestCreateIdentitiesViewsAndFiles(t *testing.T) {
	dir, aliceToken, _ := team(t)
	var created struct {
		Path, Type, Rev string
	}
	out := mustCLI(t, create, "people/carol", "--type", "identity", "--body", "# Carol\n", "--json", "--dir", dir, "--token", aliceToken)
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Path != "people/carol.md" || created.Type != "identity" || created.Rev == "" {
		t.Fatalf("create --json = %q (%v)", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "people/carol.md")); string(b) != "---\ntype: identity\n---\n# Carol\n" {
		t.Fatalf("identity source = %q", b)
	}
	if out := mustCLI(t, auth, "create", "carol", "--dir", dir); !strings.HasPrefix(out, "jk_") {
		t.Fatalf("new identity cannot get a token: %q", out)
	}
	mustCLI(t, create, "views/todo", "--type", "view", "--body", "filter:\n  type: task\n  status: todo\n", "--dir", dir, "--token", aliceToken)
	if out := mustCLI(t, view, "views/todo", "--dir", dir); !strings.Contains(out, "tasks/ship.md") {
		t.Fatalf("created view:\n%s", out)
	}
	// A view with a body is a workspace problem, so creating one is refused.
	if _, _, err := cli(t, create, "views/bad", "--type", "view", "--body", "---\ntype: view\n---\n# body\n", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("a view with a body was created")
	}
	if _, _, err := cli(t, create, "tasks/odd", "--type", "task", "--body", "---\ntype: view\n---\n", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("a body contradicting --type was accepted")
	}

	src := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(src, []byte("---\nstatus: doing\nassignee: alice\n---\n# From a file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, create, "tasks/filed", "--type", "task", "--file", src, "--dir", dir, "--token", aliceToken)
	if b, _ := os.ReadFile(filepath.Join(dir, "tasks/filed.md")); string(b) != "---\nstatus: doing\nassignee: alice\ntype: task\n---\n# From a file\n" {
		t.Fatalf("task from file = %q", b)
	}
	// --file - reads standard input.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("# Piped\n\nTwo lines.\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	mustCLI(t, create, "notes/piped", "--file", "-", "--dir", dir, "--token", aliceToken)
	if b, _ := os.ReadFile(filepath.Join(dir, "notes/piped.md")); string(b) != "# Piped\n\nTwo lines.\n" {
		t.Fatalf("piped page = %q", b)
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
	if out := mustCLI(t, commit, "Import", "workspace", "--operation", "import", "--dir", dir, "--token", aliceToken); !strings.HasPrefix(out, "committed ") {
		t.Fatalf("commit output = %q", out)
	}
	if out := mustCLI(t, commit, "Nothing", "--dir", dir, "--token", aliceToken); out != "nothing to commit\n" {
		t.Fatalf("empty commit output = %q", out)
	}
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
	var res struct {
		Commit, Actor, Operation string
		Empty                    bool
	}
	if err := os.WriteFile(filepath.Join(dir, "more.md"), []byte("# More\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out = []byte(mustCLI(t, commit, "More", "--json", "--dir", dir, "--token", aliceToken))
	if err := json.Unmarshal(out, &res); err != nil || len(res.Commit) != 40 || res.Actor != "alice" || res.Operation != "workspace-mutation" || res.Empty {
		t.Fatalf("commit --json = %s (%v)", out, err)
	}
	// A tracked credential file is never staged and is warned about.
	if out, err := exec.Command("git", "-C", dir, "add", "-f", ".auth.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "-c", "user.name=x", "-c", "user.email=x@y", "commit", "-q", "-m", "leak").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	mustCLI(t, auth, "create", "alice", "--dir", dir) // modifies the tracked .auth.md
	_, errOut, err := cli(t, commit, "After", "--dir", dir, "--token", aliceToken)
	if err != nil || !strings.Contains(errOut, "tracked by Git") {
		t.Fatalf("commit with tracked credentials: %v %q", err, errOut)
	}
	status, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if !strings.Contains(string(status), " M .auth.md") {
		t.Fatalf("credential change was staged or committed: %q", status)
	}
}

// --commit records one attributed commit per mutating command, limited to
// the files it touched; jikko log reads them back.
func TestCommitFlagRecordsEachMutation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, aliceToken, bobToken := team(t)
	if _, _, err := cli(t, set, "ship", "status", "doing", "--commit", "--dir", dir, "--token", bobToken); err == nil || !strings.Contains(err.Error(), "not Git-backed") {
		t.Fatalf("--commit outside Git: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tasks/ship.md")); strings.Contains(string(b), "doing") {
		t.Fatal("the page was changed although it could not be committed")
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	_, errOut, err := cli(t, set, "ship", "status", "doing", "--commit", "--dir", dir, "--token", bobToken)
	if err != nil || !strings.Contains(errOut, "committed ") || !strings.Contains(errOut, "(set-metadata)") {
		t.Fatalf("set --commit: %v %q", err, errOut)
	}
	var added map[string]string
	out := mustCLI(t, comment, "add", "open", "please look", "On it.", "--commit", "--json", "--dir", dir, "--token", bobToken)
	if err := json.Unmarshal([]byte(out), &added); err != nil || added["id"] != "c1" || added["path"] != "open.md" || len(added["commit"]) != 40 {
		t.Fatalf("comment add --commit --json = %q", out)
	}
	out = mustCLI(t, comment, "resolve", "open", "c1", "--commit", "--json", "--dir", dir, "--token", bobToken)
	if err := json.Unmarshal([]byte(out), &added); err != nil || added["id"] != "c1" || len(added["commit"]) != 40 {
		t.Fatalf("comment resolve --json = %q", out)
	}
	var created struct{ Path, Commit string }
	out = mustCLI(t, create, "tasks/plan", "--type", "task", "--body", "# Plan\n", "--commit", "--json", "--dir", dir, "--token", aliceToken)
	if err := json.Unmarshal([]byte(out), &created); err != nil || len(created.Commit) != 40 {
		t.Fatalf("create --commit --json = %q", out)
	}
	mustCLI(t, perm, "tasks/plan", "admin", "alice", "--commit", "--dir", dir, "--token", aliceToken)
	mustCLI(t, rename, "tasks/plan", "tasks/roadmap", "--commit", "--dir", dir, "--token", aliceToken)
	// A no-op is not a commit.
	mustCLI(t, set, "ship", "status", "doing", "--commit", "--dir", dir, "--token", bobToken)
	mustCLI(t, deletePage, "tasks/roadmap", "--commit", "--dir", dir, "--token", aliceToken)

	// Only the touched files were recorded: the rest of the workspace is
	// still untracked.
	status, _ := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if !strings.Contains(string(status), "?? alice.md") || strings.Contains(string(status), "ship.md") || strings.Contains(string(status), ".auth.md") {
		t.Fatalf("status = %q", status)
	}
	var entries []struct {
		Commit, Actor, Operation string
		Paths                    []string
	}
	if err := json.Unmarshal([]byte(mustCLI(t, logHistory, "--json", "--dir", dir)), &entries); err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, e := range entries {
		ops = append(ops, e.Operation+"@"+e.Actor)
	}
	if got := strings.Join(ops, " "); got != "delete@alice rename@alice set-permission@alice create@alice comment-resolve@bob comment-add@bob set-metadata@bob" {
		t.Fatalf("log = %s", got)
	}
	// A rename records both paths; a metadata change only its page.
	if strings.Join(entries[1].Paths, ",") != "tasks/plan.md,tasks/roadmap.md" || strings.Join(entries[6].Paths, ",") != "tasks/ship.md" {
		t.Fatalf("paths = %v / %v", entries[1].Paths, entries[6].Paths)
	}
	text := mustCLI(t, logHistory, "--limit", "1", "--dir", dir)
	if lines := strings.Split(strings.TrimSpace(text), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "  alice  delete  ") {
		t.Fatalf("log text = %q", text)
	}
	if out := mustCLI(t, logHistory, "tasks/ship", "--dir", dir); strings.Count(out, "\n") != 1 {
		t.Fatalf("page log = %q", out)
	}
	if _, _, err := cli(t, logHistory, "nothing", "--dir", dir); err == nil {
		t.Fatal("log of a missing page")
	}
	if _, _, err := cli(t, logHistory, "a", "b", "--dir", dir); err == nil {
		t.Fatal("log accepted two pages")
	}
	plain := workspace(t, map[string]string{"a.md": "# A\n"})
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	if _, _, err := cli(t, logHistory, "--dir", plain); err == nil || !strings.Contains(err.Error(), "not Git-backed") {
		t.Fatalf("log outside Git: %v", err)
	}
}

func TestInitCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("JIKKO_TOKEN", "")
	dir := filepath.Join(t.TempDir(), "work")
	out, errOut, err := cli(t, initWorkspace, "--dir", dir, "--identity", "alice")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out)
	if !strings.HasPrefix(token, "jk_") || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout must be the token alone: %q", out)
	}
	for _, want := range []string{"initialized", "as a Git repository", "created alice.md, open-work.md, welcome.md", "token for alice"} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if out := mustCLI(t, check, "--dir", dir); out != "ok: 3 pages\n" {
		t.Fatalf("check after init = %q", out)
	}
	if out := mustCLI(t, mentions, "--dir", dir, "--token", token); !strings.HasPrefix(out, "welcome.md:") {
		t.Fatalf("mentions after init = %q", out)
	}
	mustCLI(t, create, "tasks/first", "--type", "task", "--body", "# First\n", "--commit", "--dir", dir, "--token", token)
	if out := mustCLI(t, view, "open-work", "--dir", dir); !strings.Contains(out, "tasks/first.md") {
		t.Fatalf("open-work view = %q", out)
	}
	// Running it again is harmless and issues no second token.
	out, errOut, err = cli(t, initWorkspace, "--dir", dir, "--identity", "alice")
	if err != nil || out != "" || !strings.Contains(errOut, "already has a credential") || strings.Contains(errOut, "created") {
		t.Fatalf("second init: %v %q %q", err, out, errOut)
	}
	var res struct {
		Identity, Token string
		Created         []string
		Git             bool `json:"git"`
	}
	out = mustCLI(t, initWorkspace, "--dir", dir, "--identity", "bob", "--json")
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Identity != "bob" || res.Token == "" || len(res.Created) != 1 || !res.Git {
		t.Fatalf("init --json = %q (%v)", out, err)
	}
	for _, args := range [][]string{{"extra"}, {"--identity", "Not Valid"}, {"--identity", "open-work"}} {
		if _, _, err := cli(t, initWorkspace, append(args, "--dir", dir)...); err == nil {
			t.Fatalf("init %v accepted", args)
		}
	}
	plain := filepath.Join(t.TempDir(), "plain")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	_, errOut, err = cli(t, initWorkspace, "--dir", plain, "--identity", "carol", "--no-git")
	if err != nil || !strings.Contains(errOut, "not Git-backed") {
		t.Fatalf("init --no-git: %v %q", err, errOut)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); !os.IsNotExist(err) {
		t.Fatal("--no-git made a repository")
	}
	if _, errOut, err := cli(t, check, "--dir", plain); err != nil || !strings.Contains(errOut, "not Git-backed") {
		t.Fatalf("check without Git: %v %q", err, errOut)
	}
	if name := defaultIdentity(); name == "" || strings.ToLower(name) != name {
		t.Fatalf("defaultIdentity = %q", name)
	}
}

func TestAuthListAndRevokeOne(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	second := strings.TrimSpace(mustCLI(t, auth, "create", "alice", "--dir", dir))
	out := mustCLI(t, auth, "list", "--dir", dir)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "alice") || !strings.HasPrefix(lines[1], "bob") {
		t.Fatalf("auth list:\n%s", out)
	}
	var listed []struct{ Identity, ID, Created string }
	if err := json.Unmarshal([]byte(mustCLI(t, auth, "list", "--json", "--dir", dir)), &listed); err != nil || len(listed) != 3 || listed[2].Identity != "alice" || listed[2].Created == "" {
		t.Fatalf("auth list --json: %v %+v", err, listed)
	}
	if strings.Contains(out, aliceToken) || strings.Contains(out, second) {
		t.Fatal("auth list prints tokens")
	}
	if _, _, err := cli(t, auth, "revoke", "alice", "--id", "nope", "--dir", dir); err == nil {
		t.Fatal("a bad id was accepted")
	}
	mustCLI(t, auth, "revoke", "alice", "--id", listed[0].ID, "--dir", dir)
	if _, _, err := cli(t, list, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("revoked token still works")
	}
	for _, token := range []string{second, bobToken} {
		mustCLI(t, list, "--dir", dir, "--token", token)
	}
	for _, args := range [][]string{{"list", "x"}, {"revoke"}, {"create", "alice", "extra"}} {
		if _, _, err := cli(t, auth, append(args, "--dir", dir)...); err == nil {
			t.Fatalf("auth %v accepted", args)
		}
	}
}

func TestServeFailsBeforeAnnouncingABadAddress(t *testing.T) {
	dir := workspace(t, map[string]string{"a.md": "# A\n"})
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	_, errOut, err := cli(t, serve, "--addr", "127.0.0.1:99999", "--dir", dir)
	if err == nil || strings.Contains(errOut, "serving") {
		t.Fatalf("serve on a bad port: %v %q", err, errOut)
	}
	if !strings.Contains(errOut, "not Git-backed") {
		t.Fatalf("serve did not warn about a workspace without Git: %q", errOut)
	}
	if _, _, err := cli(t, serve, "--auto-commit", "--dir", dir); err == nil || !strings.Contains(err.Error(), "not Git-backed") {
		t.Fatalf("--auto-commit outside Git: %v", err)
	}
	if _, _, err := cli(t, serve, "--dir", filepath.Join(dir, "missing")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("serve on a missing dir: %v", err)
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

func TestRenameCommand(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	if _, _, err := cli(t, rename, "tasks/ship", "tasks/launch", "--dir", dir); err == nil {
		t.Fatal("anonymous rename accepted")
	}
	if _, _, err := cli(t, rename, "tasks/ship", "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("rename without a destination accepted")
	}
	// bob may read secret.md but not write it.
	if _, _, err := cli(t, rename, "secret", "public", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader renamed a page")
	}
	out := mustCLI(t, rename, "tasks/ship", "tasks/launch", "--dir", dir, "--token", aliceToken)
	if !strings.Contains(out, "renamed tasks/ship.md -> tasks/launch.md") || !strings.Contains(out, "rewrote open.md (body): tasks/ship -> tasks/launch") {
		t.Fatalf("rename output:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "open.md"))
	if err != nil || !strings.Contains(string(b), "[[tasks/launch]]") {
		t.Fatalf("open.md = %q (%v)", b, err)
	}

	var res struct {
		From, To string
		Broken   []struct{ Path, From string }
	}
	out = mustCLI(t, rename, "tasks/launch", "tasks/ship", "--no-rewrite", "--json", "--dir", dir, "--token", aliceToken)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.To != "tasks/ship.md" || len(res.Broken) != 1 || res.Broken[0].Path != "open.md" {
		t.Fatalf("no-rewrite result = %+v", res)
	}
}

func TestDeleteCommand(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	if _, _, err := cli(t, deletePage, "open", "--dir", dir); err == nil {
		t.Fatal("anonymous delete accepted")
	}
	if _, _, err := cli(t, deletePage, "secret", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader deleted a page")
	}
	// crew is named by secret.md's policy: refused without --prune.
	_, errOut, err := cli(t, deletePage, "crew", "--dir", dir, "--token", aliceToken)
	if err == nil || !strings.Contains(err.Error(), "--prune") {
		t.Fatalf("delete of a policy identity: %v %s", err, errOut)
	}
	_, errOut, err = cli(t, deletePage, "tasks/ship", "--dir", dir, "--token", aliceToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, `open.md (body): "tasks/ship" no longer resolves`) {
		t.Fatalf("stderr = %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks/ship.md")); !os.IsNotExist(err) {
		t.Fatal("page not deleted")
	}
}

func TestViewCommand(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("tasks/review.md", "---\ntype: task\nstatus: doing\nassignee: bob\npermissions:\n  read: alice\n  admin: alice\n---\n# Review\n")
	write("tasks/plan.md", "---\ntype: task\nstatus: todo\n---\n# Plan\n")
	write("by-status.md", "---\ntype: view\nfilter:\n  type: task\n  status: [todo, doing]\nview:\n  group: status\n  sort: title\n---\n")
	write("broken.md", "---\ntype: view\nfilter: everything\n---\n")

	out := mustCLI(t, view, "by-status", "--dir", dir, "--token", aliceToken)
	want := "status: todo (2)\n  task      tasks/plan.md                  Plan\n  task      tasks/ship.md                  Ship it\nstatus: doing (1)\n  task      tasks/review.md                Review\n"
	if out != want {
		t.Fatalf("view output:\n%s\nwant:\n%s", out, want)
	}
	var res struct {
		Pages  []struct{ Path string }
		Groups []struct {
			Key   string
			Pages []struct{ Path string }
		}
	}
	out = mustCLI(t, view, "by-status", "--json", "--dir", dir, "--token", bobToken)
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Pages) != 2 || len(res.Groups) != 2 || len(res.Groups[1].Pages) != 0 {
		t.Fatalf("bob's view = %+v", res)
	}
	if out := mustCLI(t, view, "board", "--dir", dir); !strings.Contains(out, "tasks/ship.md") {
		t.Fatalf("ungrouped view:\n%s", out)
	}
	if _, _, err := cli(t, view, "broken", "--dir", dir); err == nil {
		t.Fatal("a broken view was evaluated")
	}
	if _, _, err := cli(t, view, "--dir", dir); err == nil {
		t.Fatal("view without a reference accepted")
	}
	out, _, err := cli(t, check, "--dir", dir)
	if err == nil || !strings.Contains(out, "broken.md: [view] filter must be a mapping") {
		t.Fatalf("check:\n%s", out)
	}
}

func TestCheckReportsWorkModel(t *testing.T) {
	dir := workspace(t, map[string]string{
		"alice.md": "---\ntype: identity\n---\n",
		"a.md":     "---\ntype: task\nstatus: done\nrequires: [proof]\n---\n# A\n",
		"b.md":     "---\ntype: task\nstatus: todo\nassignee: ghost\n---\n# B\n",
	})
	out, _, err := cli(t, check, "--dir", dir)
	if err == nil || !strings.Contains(out, `b.md: [reference] assignee "ghost" is not an identity`) || !strings.Contains(out, "a.md: warning: [work] task is done but requires proof") {
		t.Fatalf("check:\n%s", out)
	}
}

func TestShowRawAndSave(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	src, errOut, err := cli(t, show, "open", "--raw", "--dir", dir)
	if err != nil || src != "# Open\n\nSee [[tasks/ship]]. @crew please look.\n" {
		t.Fatalf("raw = %q (%v)", src, err)
	}
	rev := strings.TrimSpace(errOut[strings.LastIndex(errOut, " ")+1:])
	var page struct{ Rev string }
	if err := json.Unmarshal([]byte(mustCLI(t, show, "open", "--json", "--dir", dir)), &page); err != nil || page.Rev != rev {
		t.Fatalf("show --json rev %q, raw rev %q", page.Rev, rev)
	}
	if _, _, err := cli(t, show, "open", "secret", "--raw", "--dir", dir); err == nil {
		t.Fatal("--raw accepted two pages")
	}
	if _, _, err := cli(t, show, "secret", "--raw", "--dir", dir); err == nil {
		t.Fatal("anonymous caller read restricted source")
	}

	scratch := t.TempDir()
	put := func(name, content string) string {
		p := filepath.Join(scratch, name)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := put("base.md", src)
	yours := put("yours.md", strings.Replace(src, "# Open", "# Open work", 1))
	if _, _, err := cli(t, save, "open", "--rev", rev, "--file", yours, "--dir", dir); err == nil {
		t.Fatal("anonymous save accepted")
	}
	// Someone else changes the last line meanwhile.
	theirs := strings.Replace(src, "please look.", "please look today.", 1)
	if err := os.WriteFile(filepath.Join(dir, "open.md"), []byte(theirs), 0644); err != nil {
		t.Fatal(err)
	}
	// Without a base the change cannot be merged: a structured conflict.
	out, _, err := cli(t, save, "open", "--rev", rev, "--file", yours, "--json", "--dir", dir, "--token", bobToken)
	var conflict struct {
		Conflict struct {
			Path      string `json:"path"`
			BaseKnown bool   `json:"base_known"`
			Body      []struct{ Line int }
		}
	}
	if err == nil || json.Unmarshal([]byte(out), &conflict) != nil || conflict.Conflict.Path != "open.md" || conflict.Conflict.BaseKnown {
		t.Fatalf("conflict: %v %s", err, out)
	}
	out = mustCLI(t, save, "open", "--rev", rev, "--file", yours, "--base", base, "--dir", dir, "--token", bobToken)
	if !strings.HasPrefix(out, "merged open.md rev ") {
		t.Fatalf("save output %q", out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "open.md"))
	if string(b) != "# Open work\n\nSee [[tasks/ship]]. @crew please look today.\n" {
		t.Fatalf("open.md = %q", b)
	}
	// A conflicting edit prints the regions and fails.
	newRev := strings.TrimSpace(strings.TrimPrefix(out, "merged open.md rev "))
	if err := os.WriteFile(filepath.Join(dir, "open.md"), []byte("# Open work, theirs\n\nSee [[tasks/ship]]. @crew please look today.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mine := put("mine.md", "# Open work, mine\n\nSee [[tasks/ship]]. @crew please look today.\n")
	out, _, err = cli(t, save, "open", "--rev", newRev, "--file", mine, "--base", put("b2.md", string(b)), "--dir", dir, "--token", aliceToken)
	if err == nil || !strings.Contains(out, "[body:0] body at line 1:") || !strings.Contains(out, "current:\n      # Open work, theirs") {
		t.Fatalf("conflict output %v:\n%s", err, out)
	}
	current := out[strings.Index(out, "--against ")+len("--against "):]
	current = current[:strings.Index(current, " ")]
	if _, _, err := cli(t, save, "open", "--rev", newRev, "--file", mine, "--resolve", "body:0=mine", "--against", current, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("a bad resolution side was accepted")
	}
	out = mustCLI(t, save, "open", "--rev", newRev, "--file", mine, "--base", filepath.Join(scratch, "b2.md"), "--resolve", "body:0=yours", "--against", current, "--dir", dir, "--token", aliceToken)
	if b, _ := os.ReadFile(filepath.Join(dir, "open.md")); !strings.HasPrefix(out, "merged") || !strings.HasPrefix(string(b), "# Open work, mine\n") {
		t.Fatalf("resolution: %s %q", out, b)
	}
	if _, _, err := cli(t, save, "secret", "--rev", rev, "--file", mine, "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader saved")
	}
}

func TestUploadCommand(t *testing.T) {
	dir, aliceToken, bobToken := team(t)
	t.Setenv("JIKKO_MAX_UPLOAD", "")
	src := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(src, []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(t, upload, src, "--dir", dir); err == nil {
		t.Fatal("anonymous upload accepted")
	}
	if _, _, err := cli(t, upload, src, "--into", "secret", "--dir", dir, "--token", bobToken); err == nil {
		t.Fatal("a reader embedded into a page")
	}
	if _, _, err := cli(t, upload, src, "--max-upload", "9", "--dir", dir, "--token", aliceToken); err == nil || !strings.Contains(err.Error(), "maximum upload size is 9") {
		t.Fatalf("limit: %v", err)
	}
	t.Setenv("JIKKO_MAX_UPLOAD", "5")
	if _, _, err := cli(t, upload, src, "--dir", dir, "--token", aliceToken); err == nil {
		t.Fatal("JIKKO_MAX_UPLOAD ignored")
	}
	t.Setenv("JIKKO_MAX_UPLOAD", "1K")
	var res struct{ Path, Into, Embed string }
	out := mustCLI(t, upload, src, "--into", "open", "--json", "--dir", dir, "--token", aliceToken)
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Path != "chart.png" || res.Embed != "chart.png" || res.Into != "open.md" {
		t.Fatalf("upload = %q (%v)", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "open.md")); !strings.HasSuffix(string(b), "\n\n![[chart.png]]\n") {
		t.Fatalf("open.md = %q", b)
	}
	for in, want := range map[string]int64{"": 25 << 20, "100": 100, "2k": 2048, "25M": 25 << 20, "1GiB": 1 << 30, "3MB": 3 << 20} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"x", "-1", "0", "1T"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
}

// TestQuickstart runs the README's Quickstart section, line by line, against
// a temporary directory, so the documented commands cannot drift from the
// program. It understands the little shell the section uses: jikko
// commands, cd, and export NAME="$(jikko ...)".
func TestQuickstart(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := quickstartLines(string(readme))
	if len(lines) < 15 {
		t.Fatalf("the Quickstart section has only %d commands:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	t.Chdir(t.TempDir())
	t.Setenv("JIKKO_TOKEN", "")
	t.Setenv("JIKKO_MAX_UPLOAD", "")
	ran := map[string]bool{}
	for _, line := range lines {
		args, err := splitShell(line)
		if err != nil || len(args) == 0 {
			t.Fatalf("%q: %v", line, err)
		}
		switch args[0] {
		case "go": // the install instruction
		case "cd":
			t.Chdir(args[1])
		case "export":
			name, value, ok := strings.Cut(args[1], "=")
			if !ok {
				t.Fatalf("%q: not an assignment", line)
			}
			if inner, isSubst := strings.CutPrefix(value, "$("); isSubst {
				inner = strings.TrimSuffix(inner, ")")
				innerArgs, err := splitShell(inner)
				if err != nil || len(innerArgs) < 2 || innerArgs[0] != "jikko" {
					t.Fatalf("%q: unsupported substitution", line)
				}
				value = strings.TrimSpace(quickstartRun(t, line, innerArgs[1:]))
				ran[innerArgs[1]] = true
			}
			t.Setenv(name, value)
		case "jikko":
			quickstartRun(t, line, args[1:])
			ran[args[1]] = true
		default:
			t.Fatalf("%q: the quickstart runner does not know this command", line)
		}
	}
	for _, want := range []string{"init", "create", "set", "view", "log", "serve", "auth", "mentions", "list", "show", "comment"} {
		if !ran[want] {
			t.Errorf("the Quickstart never runs jikko %s", want)
		}
	}
	// The agent finished the task and every step is attributed history.
	b, err := os.ReadFile("tasks/first.md")
	if err != nil || !strings.Contains(string(b), "status: done") {
		t.Fatalf("tasks/first.md = %q (%v)", b, err)
	}
	out, err := exec.Command("git", "log", "--format=%(trailers:key=Jikko-Actor,valueonly)").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	actors := strings.Fields(string(out))
	if len(actors) < 7 || !slices.Contains(actors, "alice") || !slices.Contains(actors, "codex") {
		t.Fatalf("actors in history = %v", actors)
	}
	status, _ := exec.Command("git", "status", "--porcelain").CombinedOutput()
	if strings.Contains(string(status), ".auth.md") {
		t.Fatalf("credentials visible to git: %q", status)
	}
	if out := mustCLI(t, check); !strings.HasPrefix(out, "ok:") {
		t.Fatalf("check after the quickstart = %q", out)
	}
}

// quickstartRun runs one documented jikko command. serve blocks, so it is
// started on an ephemeral port and probed over HTTP instead.
func quickstartRun(t *testing.T, line string, args []string) string {
	t.Helper()
	if args[0] == "serve" {
		quickstartServe(t, append(args[1:], "--addr", "127.0.0.1:0"))
		return ""
	}
	fn, ok := commands[args[0]]
	if !ok {
		t.Fatalf("%q: unknown command", line)
	}
	out, errOut, err := cli(t, fn, args[1:]...)
	if err != nil {
		t.Fatalf("%q: %v\n%s", line, err, errOut)
	}
	return out
}

func quickstartServe(t *testing.T, args []string) {
	t.Helper()
	var logs lockedBuffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	go func() {
		if err := serve(args); err != nil {
			logs.WriteString("serve failed: " + err.Error())
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if m := regexp.MustCompile(`serving .* at (http://\S+)`).FindStringSubmatch(logs.String()); m != nil {
			res, err := http.Get(m[1] + "/")
			if err != nil {
				t.Fatalf("GET %s: %v", m[1], err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: %s", m[1], res.Status)
			}
			return
		}
		if time.Now().After(deadline) || strings.Contains(logs.String(), "serve failed") {
			t.Fatalf("serve did not come up: %q", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) WriteString(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(s)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// quickstartLines returns the shell lines of the README's Quickstart
// section: every line inside a ```sh block before the next top-level
// heading.
func quickstartLines(readme string) []string {
	var out []string
	inSection, inBlock := false, false
	for _, line := range strings.Split(readme, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			inSection = strings.HasPrefix(line, "## Quickstart")
		case !inSection:
		case strings.HasPrefix(line, "```"):
			inBlock = !inBlock && strings.TrimPrefix(line, "```") == "sh"
		case inBlock && strings.TrimSpace(line) != "":
			out = append(out, line)
		}
	}
	return out
}

// splitShell splits a command line the way a POSIX shell would for the
// subset the README uses: words, single and double quotes, and a trailing
// # comment. Nothing is expanded.
func splitShell(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	quote := byte(0)
	word := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, word = c, true
		case c == '#' && !word:
			i = len(line)
		case c == ' ' || c == '\t':
			if word {
				args = append(args, cur.String())
				cur.Reset()
				word = false
			}
		default:
			cur.WriteByte(c)
			word = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if word {
		args = append(args, cur.String())
	}
	return args, nil
}
