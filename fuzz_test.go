package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fuzzWorkspace is a small workspace with an identity, a page, a view and a
// file, so every kind of reference the renderer resolves is exercised.
func fuzzWorkspace(tb testing.TB) *Workspace {
	tb.Helper()
	d := tb.TempDir()
	for name, content := range map[string]string{
		"alice.md": "---\ntype: identity\n---\n# Alice\n",
		"b.md":     "# B\n\nInside b. ![[page]]\n",
		"board.md": "---\ntype: view\nfilter:\n  type: identity\n---\n",
		"page.md":  "# Page\n",
	} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0644); err != nil {
			tb.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "pic.png"), []byte("x"), 0644); err != nil {
		tb.Fatal(err)
	}
	w, err := Open(d)
	if err != nil {
		tb.Fatal(err)
	}
	return w
}

var (
	tagStart       = regexp.MustCompile(`<(/?)([a-zA-Z0-9]+)([^>]*)>`)
	eventAttribute = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	unsafeURL      = regexp.MustCompile(`(?i)(href|src)="\s*(javascript|data|vbscript):`)
)

// checkRendered asserts the renderer's safety contract on one output: every
// "<" starts an element the renderer writes, no element carries an event
// handler or a script URL, and the private placeholders never leak.
func checkRendered(tb testing.TB, in, out string) {
	tb.Helper()
	for i := 0; i < len(out); i++ {
		if out[i] != '<' {
			continue
		}
		m := tagStart.FindStringSubmatch(out[i:])
		if m == nil || !allowedTags[strings.ToLower(m[2])] {
			tb.Fatalf("stray or unexpected element at %d in output of %q:\n%s", i, in, out)
		}
		if eventAttribute.MatchString(m[3]) || unsafeURL.MatchString(m[3]) || strings.Contains(strings.ToLower(m[3]), "style=") {
			tb.Fatalf("unsafe attribute in output of %q:\n%s", in, out)
		}
	}
	if strings.ContainsAny(out, anchorOpen+anchorID+anchorClose) {
		tb.Fatalf("anchor placeholder leaked in output of %q:\n%s", in, out)
	}
}

func FuzzRender(f *testing.F) {
	w := fuzzWorkspace(f)
	for _, seed := range []string{
		"# T\n\n**b** *i* ~~s~~ `c` [[alice|A]] ![[b]] ![[pic.png]] ![x](pic.png) [l](https://x.example) @alice\n",
		"| a | b |\n|---|:-:|\n| 1 | **2** |\n",
		"- [ ] a\n- [x] b\n  - c\n\n> q\n\n```\n<script>\n```\n",
		"x <!--comment:c1-->y<!--/comment:c1--> z\n\n<!--comment-thread:c1\n@alice: hi\n-->\n",
		"<!--comment:c1-->a\n\n```\n<!--/comment:c1-->\n```\n",
		"<img src=x onerror=alert(1)> <a href=javascript:x>y</a> \ue000c1\ue001\ue002",
		"![[board]] ![[nowhere]] [[a]][[b]][[alice]]@alice@alice `a``b` ***x***",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		r := w.Renderer("alice", DefaultLinks)
		out := string(r.Page(&Page{Path: "page.md", Body: body}))
		checkRendered(t, body, out)
		checkRendered(t, body, string(r.Inline(body)))
	})
}

func FuzzScanComments(f *testing.F) {
	for _, seed := range []string{
		"x <!--comment:c1-->y<!--/comment:c1--> z\n\n<!--comment-thread:c1\n@alice: hi\n-->\n",
		"<!--comment:c1--><!--comment:c1-->a<!--/comment:c1-->",
		"<!--/comment:c1-->a<!--comment:c1-->",
		"```\n<!--comment:c1-->\n```\n`<!--comment:c2-->`<!--/comment:c2-->",
		"<!--comment-thread:c1\n-->\n<!--comment-thread:c1\n-->\n",
	} {
		f.Add(seed, "y")
	}
	f.Fuzz(func(t *testing.T, body, anchor string) {
		spans, _ := scanComments(body)
		within := func(at int) bool { return at == -1 || (at >= 0 && at <= len(body)) }
		for _, s := range spans {
			for _, at := range []int{s.openStart, s.openEnd, s.closeStart, s.closeEnd, s.threadStart, s.threadBody, s.threadEnd} {
				if !within(at) {
					t.Fatalf("offset %d outside body of %d bytes: %+v", at, len(body), s)
				}
			}
			if s.openStart > s.openEnd || s.closeStart > s.closeEnd || s.threadStart > s.threadBody || s.threadBody > s.threadEnd {
				t.Fatalf("span out of order: %+v", s)
			}
			if s.openStart >= 0 && !strings.HasPrefix(body[s.openStart:s.openEnd], "<!--comment:") {
				t.Fatalf("open span does not cover its marker: %+v %q", s, body[s.openStart:s.openEnd])
			}
		}
		threads, _ := parseComments("page.md", body)
		if len(threads) > len(spans) {
			t.Fatalf("%d threads from %d spans", len(threads), len(spans))
		}
		prepare(body)
		if anchor == "" || strings.ContainsAny(anchor, "\r\n") {
			return
		}
		if at, err := findAnchor(body, spans, anchor); err == nil && body[at:at+len(anchor)] != anchor {
			t.Fatalf("anchor found at %d but body reads %q there", at, body[at:at+len(anchor)])
		}
	})
}

func FuzzMerge3(f *testing.F) {
	f.Add([]byte("---\na: 1\n---\n# T\n\nline\n"), []byte("---\na: 2\n---\n# T\n\nline\n"), []byte("---\na: 1\nb: 3\n---\n# T\n\nline\nmore\n"))
	f.Add([]byte("a\nb\nc\n"), []byte("a\nB\nc\n"), []byte("a\nb\nC\n"))
	f.Add([]byte("---\nbroken: [\n---\nx"), []byte("x\r\ny"), []byte(""))
	f.Fuzz(func(t *testing.T, base, current, yours []byte) {
		if _, conflict, err := merge3(base, base, yours, nil); err == nil && conflict != nil {
			t.Fatalf("unchanged current conflicts: %+v", conflict)
		}
		if _, conflict, err := merge3(base, current, base, nil); err == nil && conflict != nil {
			t.Fatalf("unchanged yours conflicts: %+v", conflict)
		}
		// An empty merge is a result: both sides may have emptied the page.
		_, conflict, err := merge3(base, current, yours, nil)
		if err != nil || conflict == nil {
			return
		}
		ids := map[string]bool{}
		resolve := map[string]string{}
		for _, c := range conflict.Body {
			if c.Line < 1 || ids[c.ID] {
				t.Fatalf("bad body conflict %+v", c)
			}
			ids[c.ID] = true
			resolve[c.ID] = TakeYours
		}
		for _, c := range conflict.Fields {
			if ids[c.ID] || c.ID != "field:"+c.Key {
				t.Fatalf("bad field conflict %+v", c)
			}
			ids[c.ID] = true
			resolve[c.ID] = TakeCurrent
		}
		// Settling every conflict settles the merge.
		if _, again, err := merge3(base, current, yours, resolve); err == nil && again != nil {
			t.Fatalf("conflicts remain after resolving all: %+v", again)
		}
	})
}

func BenchmarkRender(b *testing.B) {
	w := fuzzWorkspace(b)
	var body strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&body, "## Section %d\n\nSome *emphasis*, **strong** text, `code`, a [[alice|link]], @alice, ~~gone~~ and ![pic](pic.png).\n\n- [ ] item %d\n- [x] done\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n", i, i)
	}
	p := &Page{Path: "page.md", Body: body.String()}
	b.SetBytes(int64(body.Len()))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Renderer("alice", DefaultLinks).Page(p)
	}
}

func BenchmarkMerge3(b *testing.B) {
	var base, current, yours strings.Builder
	for i := 0; i < 2000; i++ {
		line := fmt.Sprintf("line %d of the page\n", i)
		base.WriteString(line)
		switch i % 50 {
		case 10:
			current.WriteString("current edit\n")
			yours.WriteString(line)
		case 30:
			current.WriteString(line)
			yours.WriteString("your edit\n")
		default:
			current.WriteString(line)
			yours.WriteString(line)
		}
	}
	bb, cb, yb := []byte("---\na: 1\n---\n"+base.String()), []byte("---\na: 2\n---\n"+current.String()), []byte("---\na: 1\nb: 3\n---\n"+yours.String())
	b.SetBytes(int64(len(bb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, conflict, err := merge3(bb, cb, yb, nil); err != nil || conflict != nil {
			b.Fatal("benchmark merge conflicts")
		}
	}
}
