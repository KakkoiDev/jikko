package jikko

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commentWorkspace(t *testing.T) (string, *Workspace) {
	t.Helper()
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n# Bob\n")
	writeTest(t, d, "carol.md", "---\ntype: identity\n---\n# Carol\n")
	writeTest(t, d, "crew.md", "---\ntype: identity\nmembers: [bob]\n---\n# Crew\n")
	writeTest(t, d, "design.md", "---\npermissions:\n  admin: alice\n  comment: crew\n  read: carol\n---\n# Design\n\nThe harness should automatically update references\nwhen a file is renamed.\n\nSee [[alice]] for detail.\n")
	w, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	return d, w
}

func readFile(t *testing.T, d, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The specification's provisional example parses into one comment with two
// attributed messages.
func TestParseSpecificationExample(t *testing.T) {
	body := "The harness should <!--comment:c17-->automatically update references<!--/comment:c17-->\nwhen a file is renamed.\n\n<!--comment-thread:c17\n@alice: Should normal links update too?\n\n@codex: Yes, links and embeds.\n-->\n"
	comments, problems := parseComments("x.md", body)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(comments) != 1 {
		t.Fatalf("comments = %#v", comments)
	}
	c := comments[0]
	if c.ID != "c17" || c.Anchor != "automatically update references" || len(c.Entries) != 2 {
		t.Fatalf("comment = %#v", c)
	}
	if c.Entries[0] != (CommentEntry{Author: "alice", Text: "Should normal links update too?"}) ||
		c.Entries[1] != (CommentEntry{Author: "codex", Text: "Yes, links and embeds."}) {
		t.Fatalf("entries = %#v", c.Entries)
	}
}

func TestMalformedCommentsAreReported(t *testing.T) {
	cases := map[string]string{
		"duplicate anchor": "<!--comment:c1-->a<!--/comment:c1--> <!--comment:c1-->b<!--/comment:c1-->\n\n<!--comment-thread:c1\n@a: x\n-->\n",
		"duplicate thread": "<!--comment:c1-->a<!--/comment:c1-->\n\n<!--comment-thread:c1\n@a: x\n-->\n\n<!--comment-thread:c1\n@a: y\n-->\n",
		"orphaned thread":  "plain\n\n<!--comment-thread:c1\n@a: x\n-->\n",
		"no thread":        "<!--comment:c1-->a<!--/comment:c1-->\n",
		"never ends":       "<!--comment:c1-->a\n\n<!--comment-thread:c1\n@a: x\n-->\n",
		"never opens":      "a<!--/comment:c1-->\n\n<!--comment-thread:c1\n@a: x\n-->\n",
		"reversed":         "<!--/comment:c1-->a<!--comment:c1-->\n\n<!--comment-thread:c1\n@a: x\n-->\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, problems := parseComments("x.md", body); len(problems) == 0 {
				t.Fatal("not reported")
			}
		})
	}
	// Syntax inside code is documentation.
	fenced := "```md\n<!--comment:c1-->a<!--/comment:c1-->\n<!--comment-thread:c1\n@a: x\n-->\n```\n\nUse `<!--comment-thread:c2` to start a thread.\n"
	if comments, problems := parseComments("x.md", fenced); len(comments) != 0 || len(problems) != 0 {
		t.Fatalf("code parsed as comments: %v %v", comments, problems)
	}
}

// Add, reply, and resolve change only comment markup, and resolving restores
// the page exactly as it was.
func TestCommentLifecycle(t *testing.T) {
	d, w := commentWorkspace(t)
	original := readFile(t, d, "design.md")

	id, err := w.AddComment("bob", "design", "automatically update references", "Should normal links update too?")
	if err != nil {
		t.Fatal(err)
	}
	if id != "c1" {
		t.Fatalf("id = %q", id)
	}
	want := "---\npermissions:\n  admin: alice\n  comment: crew\n  read: carol\n---\n# Design\n\nThe harness should <!--comment:c1-->automatically update references<!--/comment:c1-->\nwhen a file is renamed.\n\n<!--comment-thread:c1\n@bob: Should normal links update too?\n-->\n\nSee [[alice]] for detail.\n"
	if got := readFile(t, d, "design.md"); got != want {
		t.Fatalf("after add:\n%s\nwant:\n%s", got, want)
	}
	if err := w.ReplyComment("alice", "design", "c1", "Yes, links and embeds.\nAsk @carol."); err != nil {
		t.Fatal(err)
	}
	p, _ := w.Resolve("design")
	if len(p.Comments) != 1 || len(p.Comments[0].Entries) != 2 || p.Comments[0].Entries[1].Author != "alice" {
		t.Fatalf("comments = %#v", p.Comments)
	}
	if p.Title != "Design" {
		t.Fatalf("title = %q", p.Title)
	}
	id2, err := w.AddComment("bob", "design", "for detail", "Which detail?")
	if err != nil || id2 != "c2" {
		t.Fatalf("second comment = %q %v", id2, err)
	}
	if got := w.Comments("carol"); len(got) != 2 {
		t.Fatalf("reader sees %d comments", len(got))
	}
	if err := w.ResolveComment("bob", "design", "c2"); err != nil {
		t.Fatal(err)
	}
	if err := w.ResolveComment("bob", "design", "c1"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "design.md"); got != original {
		t.Fatalf("after resolve:\n%q\nwant:\n%q", got, original)
	}
	if err := w.ResolveComment("bob", "design", "c1"); err == nil {
		t.Fatal("resolved a comment twice")
	}
	if len(w.Problems) != 0 {
		t.Fatalf("problems = %v", w.Problems)
	}
}

// comment grants comment, reply, and resolve, and nothing else; read grants
// none of them.
func TestCommentAuthorization(t *testing.T) {
	d, w := commentWorkspace(t)
	if _, err := w.AddComment("carol", "design", "automatically", "hi"); err == nil {
		t.Fatal("a reader commented")
	}
	if _, err := w.AddComment("crew", "design", "automatically", "hi"); err == nil {
		t.Fatal("a group commented")
	}
	if _, err := w.AddComment("", "design", "automatically", "hi"); err == nil {
		t.Fatal("an anonymous caller commented")
	}
	if _, err := w.AddComment("alice", "design", "automatically", "Why?"); err != nil {
		t.Fatalf("an admin could not comment: %v", err)
	}
	before := readFile(t, d, "design.md")
	if err := w.ReplyComment("carol", "design", "c1", "me too"); err == nil {
		t.Fatal("a reader replied")
	}
	if err := w.ResolveComment("carol", "design", "c1"); err == nil {
		t.Fatal("a reader resolved")
	}
	if got := readFile(t, d, "design.md"); got != before {
		t.Fatal("a denied comment operation changed the file")
	}
	if err := w.ReplyComment("bob", "design", "c1", "Because."); err != nil {
		t.Fatalf("a commenter could not reply: %v", err)
	}
	if err := w.SetMetadata("bob", "design", "status", "done"); err == nil {
		t.Fatal("comment granted a metadata write")
	}
	if err := w.ReplaceBody("bob", "design", "# Design\n"); err == nil {
		t.Fatal("comment granted a body write")
	}
	if err := w.ResolveComment("bob", "design", "c1"); err != nil {
		t.Fatalf("a commenter could not resolve: %v", err)
	}
}

func TestCommentAnchorAndTextValidation(t *testing.T) {
	d, w := commentWorkspace(t)
	writeTest(t, d, "code.md", "# Code\n\nthe word here and the word there\n\n```\nunique phrase\n```\n\nUse `inline phrase` here.\n")
	writeTest(t, d, "board.md", "---\ntype: view\n---\n")
	var err error
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ ref, anchor, text string }{
		"missing anchor":        {"design", "nowhere to be found", "x"},
		"ambiguous anchor":      {"code", "the word", "x"},
		"anchor in fence":       {"code", "unique phrase", "x"},
		"anchor in inline code": {"code", "inline phrase", "x"},
		"anchor splits link":    {"design", "[[ali", "x"},
		"multi-line anchor":     {"design", "references\nwhen", "x"},
		"empty anchor":          {"design", "", "x"},
		"anchor with marker":    {"design", "-->", "x"},
		"empty text":            {"design", "automatically", "  "},
		"text closes comment":   {"design", "automatically", "a --> b"},
		"text opens comment":    {"design", "automatically", "a <!-- b"},
		"text with blank line":  {"design", "automatically", "a\n\nb"},
		"text with fence":       {"design", "automatically", "a\n```\nb"},
		"view":                  {"board", "x", "x"},
		"unknown page":          {"nope", "x", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := w.AddComment("alice", c.ref, c.anchor, c.text); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := w.AddComment("alice", "design", "[[alice]]", "the whole link is fine"); err != nil {
		t.Fatalf("anchor around a whole link: %v", err)
	}
	if err := w.ReplyComment("alice", "design", "c9", "x"); err == nil {
		t.Fatal("replied to a missing comment")
	}
}

// Deleting commented text without dealing with its thread would orphan the
// discussion, so it is rejected like any other new workspace problem.
func TestBodyEditCannotOrphanAComment(t *testing.T) {
	d, w := commentWorkspace(t)
	if _, err := w.AddComment("alice", "design", "automatically update references", "Why?"); err != nil {
		t.Fatal(err)
	}
	body := strings.SplitN(readFile(t, d, "design.md"), "---\n", 3)[2]
	orphaned := strings.Replace(body, "<!--comment:c1-->automatically update references<!--/comment:c1-->", "rewrite links", 1)
	if err := w.ReplaceBody("alice", "design", orphaned); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphaning edit = %v", err)
	}
}

// The "@name:" that attributes a message is not a mention of that identity;
// a mention inside the message is.
func TestCommentAuthorIsNotAMention(t *testing.T) {
	_, w := commentWorkspace(t)
	if _, err := w.AddComment("bob", "design", "automatically", "Ask @carol."); err != nil {
		t.Fatal(err)
	}
	for _, p := range w.Mentions("bob") {
		if p.Path == "design.md" {
			t.Fatal("writing a comment mentioned its author")
		}
	}
	found := false
	for _, p := range w.Mentions("carol") {
		found = found || p.Path == "design.md"
	}
	if !found {
		t.Fatal("a mention inside a comment was missed")
	}
}

func TestCommentKeepsCRLFAndFrontmatterlessPages(t *testing.T) {
	d, w := commentWorkspace(t)
	writeTest(t, d, "crlf.md", "---\r\ntype: task\r\n---\r\n# Task\r\n\r\nShip the thing.\r\n")
	writeTest(t, d, "plain.md", "\xef\xbb\xbf# Plain\n\nShip it.")
	var err error
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddComment("alice", "crlf", "the thing", "When?"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, d, "crlf.md")
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("line endings mixed: %q", got)
	}
	if _, err := w.AddComment("alice", "plain", "Ship it", "Now?"); err != nil {
		t.Fatal(err)
	}
	want := "\xef\xbb\xbf# Plain\n\n<!--comment:c1-->Ship it<!--/comment:c1-->.\n\n<!--comment-thread:c1\n@alice: Now?\n-->\n"
	if got := readFile(t, d, "plain.md"); got != want {
		t.Fatalf("plain.md = %q", got)
	}
	if err := w.ResolveComment("alice", "plain", "c1"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, d, "plain.md"); got != "\xef\xbb\xbf# Plain\n\nShip it.\n" {
		t.Fatalf("resolved plain.md = %q", got)
	}
}

// A Task marked done with unresolved comments is a warning, not a problem.
func TestDoneTaskWithUnresolvedCommentsWarns(t *testing.T) {
	d, w := commentWorkspace(t)
	writeTest(t, d, "task.md", "---\ntype: task\nstatus: doing\n---\n# Task\n\nShip it.\n")
	var err error
	if w, err = Open(d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddComment("alice", "task", "Ship it", "Tests?"); err != nil {
		t.Fatal(err)
	}
	if got := w.UnresolvedCommentWarnings(); len(got) != 0 {
		t.Fatalf("warned before done: %v", got)
	}
	if err := w.SetMetadata("alice", "task", "status", "done"); err != nil {
		t.Fatalf("completing with open comments must not be refused: %v", err)
	}
	got := w.UnresolvedCommentWarnings()
	if len(got) != 1 || got[0].Path != "task.md" {
		t.Fatalf("warnings = %v", got)
	}
	if err := w.ResolveComment("alice", "task", "c1"); err != nil {
		t.Fatal(err)
	}
	if got := w.UnresolvedCommentWarnings(); len(got) != 0 {
		t.Fatalf("warned after resolve: %v", got)
	}
}
