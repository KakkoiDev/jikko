package jikko

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func renderPage(t *testing.T, w *Workspace, actor, ref string) string {
	t.Helper()
	p, ok := w.Resolve(ref)
	if !ok {
		t.Fatalf("%s not found", ref)
	}
	return string(w.Renderer(actor, DefaultLinks).Page(p))
}

// allowedTags are the only elements the renderer may emit.
var allowedTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"pre": true, "code": true, "blockquote": true, "ul": true, "ol": true, "li": true,
	"hr": true, "a": true, "strong": true, "em": true, "mark": true, "span": true,
	"figure": true, "figcaption": true, "div": true, "img": true, "video": true,
	"audio": true, "input": true, "table": true, "thead": true, "tbody": true,
	"tr": true, "th": true, "td": true, "del": true, "iframe": true,
}

func assertSafeHTML(t *testing.T, out string) {
	t.Helper()
	for _, m := range regexp.MustCompile(`<(/?)([a-zA-Z0-9]+)([^>]*)>`).FindAllStringSubmatch(out, -1) {
		if !allowedTags[strings.ToLower(m[2])] {
			t.Fatalf("unexpected element <%s> in:\n%s", m[2], out)
		}
		if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(m[3]) {
			t.Fatalf("event handler attribute in:\n%s", out)
		}
		if regexp.MustCompile(`(?i)(href|src)="\s*(javascript|data|vbscript):`).MatchString(m[3]) {
			t.Fatalf("unsafe URL in:\n%s", out)
		}
	}
	if strings.Contains(strings.ToLower(out), "style=") {
		t.Fatalf("inline style in:\n%s", out)
	}
}

func TestRenderEscapesEverything(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "evil.md", "# <script>alert(1)</script>\n\n"+
		"<img src=x onerror=alert(1)> and <!-- a comment --> <b>bold</b>\n\n"+
		"[click](javascript:alert(1)) [click2](JaVaScRiPt:void) [data](data:text/html,<script>) [proto](//evil.example/x) [vb](vbscript:x)\n\n"+
		"[ok](https://example.com/a?b=\"c\"&d) [rel](other/page.md) [mail](mailto:a@b.example)\n\n"+
		"[[evil|<img src=x onerror=alert(1)>]] [[nowhere|\"><script>]]\n\n"+
		"https://example.com/x\"onmouseover=alert(1)\n\n"+
		"```html\n<script>alert('code')</script>\n```\n\n"+
		"`<b>inline</b>` **<i>strong</i>** *<u>em</u>*\n\n"+
		"> <iframe src=x>\n\n"+
		"- <svg onload=alert(1)>\n- [x] done <em>\n\n"+
		"c1forged\n")
	w := openTest(t, d)
	out := renderPage(t, w, "", "evil")
	assertSafeHTML(t, out)
	for _, want := range []string{
		"<h1>&lt;script&gt;alert(1)&lt;/script&gt;</h1>",
		"&lt;img src=x onerror=alert(1)&gt;",
		"&lt;!-- a comment --&gt;",
		"[click](javascript:alert(1)) click2 data proto vb",
		`<a href="https://example.com/a?b=&#34;c&#34;&amp;d" rel="noopener noreferrer">ok</a>`,
		`<a href="other/page.md" rel="noopener noreferrer">rel</a>`,
		`<a href="mailto:a@b.example" rel="noopener noreferrer">mail</a>`,
		`<a class="jk-ref" href="/p/evil.md">&lt;img src=x onerror=alert(1)&gt;</a>`,
		`<span class="jk-unresolved">&#34;&gt;&lt;script&gt;</span>`,
		`<pre class="jk-code" data-lang="html"><code>&lt;script&gt;alert(&#39;code&#39;)&lt;/script&gt;</code></pre>`,
		"<code>&lt;b&gt;inline&lt;/b&gt;</code> <strong>&lt;i&gt;strong&lt;/i&gt;</strong> <em>&lt;u&gt;em&lt;/u&gt;</em>",
		"<blockquote>\n<p>&lt;iframe src=x&gt;</p>\n</blockquote>",
		"<li>&lt;svg onload=alert(1)&gt;</li>",
		`<li><input type="checkbox" disabled checked> done &lt;em&gt;</li>`,
		"�c1�forged�",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `onmouseover=alert(1)"`) && !strings.Contains(out, "&#34;onmouseover") {
		t.Errorf("autolink broke out of its attribute:\n%s", out)
	}
}

func TestRenderStructure(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n# Alice\n")
	writeTest(t, d, "doc.md", "# Title\n\n## Section\n\nA paragraph\nspanning lines with @alice and @nobody.\n\n1. one\n2. two\n   - nested\n   - list\n\n---\n\n```mermaid\nflowchart LR\n  A --> B\n```\n")
	w := openTest(t, d)
	out := renderPage(t, w, "", "doc")
	assertSafeHTML(t, out)
	for _, want := range []string{
		"<h1>Title</h1>", "<h2>Section</h2>",
		`<p>A paragraph` + "\n" + `spanning lines with <a class="jk-mention" href="/p/alice.md">@alice</a> and <span class="jk-mention">@nobody</span>.</p>`,
		"<ol>\n<li>one</li>\n<li>two\n<ul>\n<li>nested</li>\n<li>list</li>\n</ul>\n</li>\n</ol>",
		"<hr>",
		`<pre class="jk-code jk-mermaid" data-lang="mermaid"><code>flowchart LR` + "\n" + `  A --&gt; B</code></pre>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderCommentsAsAnchors(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "doc.md", "The harness should <!--comment:c1-->update **references**<!--/comment:c1--> on rename.\n\n<!--comment-thread:c1\n@alice: Links too?\n-->\n\nAfter.\n\n```\n<!--comment:c2-->code<!--/comment:c2-->\n```\n")
	w := openTest(t, d)
	out := renderPage(t, w, "", "doc")
	assertSafeHTML(t, out)
	if !strings.Contains(out, `<mark class="jk-anchor" id="anchor-c1">update <strong>references</strong></mark><a class="jk-anchor-ref" href="#comment-c1">c1</a> on rename.`) {
		t.Fatalf("anchor not rendered:\n%s", out)
	}
	if strings.Contains(out, "Links too?") || strings.Contains(out, "comment-thread") {
		t.Fatalf("thread rendered inline:\n%s", out)
	}
	if !strings.Contains(out, "&lt;!--comment:c2--&gt;code") {
		t.Fatalf("comment syntax in code was interpreted:\n%s", out)
	}
}

func TestRenderEmbeds(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "a.md", "# A\n\n![[b]]\n\n![[diagram.png]]\n\n![[talk.mp4]]\n\n![[notes.zip]]\n\n![[secret]]\n\n![[missing]]\n\n![[board]]\n")
	writeTest(t, d, "b.md", "# B <i>\n\nInside b.\n\n![[a]]\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  admin: alice\n---\n# Secret title\n\nClassified.\n")
	writeTest(t, d, "board.md", "---\ntype: view\nfilter:\n  type: identity\n---\n")
	for _, f := range []string{"diagram.png", "talk.mp4", "notes.zip"} {
		if err := os.WriteFile(filepath.Join(d, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	w := openTest(t, d)
	out := renderPage(t, w, "", "a")
	assertSafeHTML(t, out)
	for _, want := range []string{
		`<figure class="jk-embed" data-path="b.md"><figcaption><a href="/p/b.md">B &lt;i&gt;</a> <code class="jk-embed-ref">![[b]]</code></figcaption><div class="jk-embed-body"><h1>B &lt;i&gt;</h1>`,
		"Embed cycle: a.md already appears above.",
		`<img src="/files/diagram.png" alt="diagram.png" loading="lazy">`,
		`<video controls preload="metadata" src="/files/talk.mp4"></video>`,
		`<a class="jk-file" href="/files/notes.zip" download>notes.zip</a>`,
		`<code class="jk-embed-ref">![[secret]]</code> is not available.`,
		`<code class="jk-embed-ref">![[missing]]</code> is not available.`,
		`<ul class="jk-view-list"><li><a href="/p/alice.md">alice</a></li></ul>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Secret title") || strings.Contains(out, "Classified") {
		t.Fatalf("unreadable embed disclosed:\n%s", out)
	}
	// alice may read the secret, so it expands for her.
	if out := renderPage(t, w, "alice", "a"); !strings.Contains(out, "Classified.") {
		t.Fatalf("readable embed not expanded:\n%s", out)
	}
}

func TestRenderSelfEmbedAndDepth(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "self.md", "![[self]]\n")
	chain := ""
	for i := 0; i < 8; i++ {
		chain = "d" + string(rune('0'+i))
		writeTest(t, d, chain+".md", "![[d"+string(rune('0'+i+1))+"]]\n")
	}
	writeTest(t, d, "d8.md", "bottom\n")
	w := openTest(t, d)
	if out := renderPage(t, w, "", "self"); !strings.Contains(out, "Embed cycle") {
		t.Fatalf("self embed:\n%s", out)
	}
	out := renderPage(t, w, "", "d0")
	if !strings.Contains(out, "nested too deeply") || strings.Contains(out, "bottom") {
		t.Fatalf("depth limit:\n%s", out)
	}
}

func TestRenderInline(t *testing.T) {
	w := openTest(t, t.TempDir())
	out := string(w.Renderer("", DefaultLinks).Inline("See <b>[[x]]</b> and `code`"))
	if out != `See &lt;b&gt;<span class="jk-unresolved">x</span>&lt;/b&gt; and <code>code</code>` {
		t.Fatalf("inline = %q", out)
	}
}

func TestReadableFileFollowsThePagesThatUseIt(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "alice.md", "---\ntype: identity\n---\n")
	writeTest(t, d, "open.md", "![[img/public.png]]\n")
	writeTest(t, d, "secret.md", "---\npermissions:\n  admin: alice\n---\n![[private.png]]\n")
	writeTest(t, d, "task.md", "---\ntype: task\nproof: report.pdf\n---\n")
	for _, f := range []string{"img/public.png", "private.png", "report.pdf", "unused.png", ".env"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, f)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeTest(t, d, "dot.md", "[[.env]]\n")
	w := openTest(t, d)
	for _, tc := range []struct {
		actor, file string
		ok          bool
	}{
		{"", "img/public.png", true},
		{"", "../img/public.png", true},
		{"", "private.png", false},
		{"alice", "private.png", true},
		{"", "report.pdf", true},
		{"", "unused.png", false},
		{"", ".env", false},
		{"", "open.md", false},
		{"", ".auth.md", false},
		{"", "missing.png", false},
	} {
		if _, ok := w.ReadableFile(tc.actor, tc.file); ok != tc.ok {
			t.Errorf("ReadableFile(%q, %q) = %v", tc.actor, tc.file, ok)
		}
	}
}

// Inline tokens keep their meaning when the scanner reuses an earlier
// search: a mention right after a token still starts one, and a backtick run
// without a closing fence does not hide a code span further on.
func TestRenderInlineTokenBoundaries(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	w := openTest(t, d)
	r := w.Renderer("", DefaultLinks)
	for in, want := range map[string]string{
		"[[x]]@bob":   `<span class="jk-unresolved">x</span><a class="jk-mention" href="/p/bob.md">@bob</a>`,
		"@bob@bob":    `<a class="jk-mention" href="/p/bob.md">@bob</a><a class="jk-mention" href="/p/bob.md">@bob</a>`,
		"a@bob":       `a@bob`,
		"**b**@bob":   `<strong>b</strong><a class="jk-mention" href="/p/bob.md">@bob</a>`,
		"` *a* `b` c": "<code>*a*</code>b` c",
		"*a* ` *b*":   "<em>a</em> ` <em>b</em>",
		"*a*`x`*b*":   "<em>a</em><code>x</code><em>b</em>",
	} {
		if got := string(r.Inline(in)); got != want {
			t.Errorf("Inline(%q) = %q, want %q", in, got, want)
		}
	}
}

// Rendering is linear in the size of a page: a paragraph of many tokens, or a
// page with many comment anchors, must not cost a pass per token.
func TestRenderLargePagesStayFast(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "bob.md", "---\ntype: identity\n---\n")
	w := openTest(t, d)
	var body strings.Builder
	body.WriteString(strings.Repeat("*a* [[x]] @bob `c` ", 20000))
	body.WriteString("\n\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&body, "Para <!--comment:c%d-->word<!--/comment:c%d--> here.\n\n<!--comment-thread:c%d\n@bob: note\n-->\n\n", i, i, i)
	}
	writeTest(t, d, "page.md", body.String())
	w = openTest(t, d)
	start := time.Now()
	out := renderPage(t, w, "", "page")
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("rendering %d bytes took %v", body.Len(), took)
	}
	if strings.Count(out, "<em>a</em>") != 20000 || strings.Count(out, `<mark class="jk-anchor"`) != 3000 {
		t.Fatal("large page rendered incompletely")
	}
}

// Ordinary Markdown works without conversion (specification §2): tables,
// images, strikethrough, and task lists render, and a PDF embeds inline.
func TestRenderOrdinaryMarkdown(t *testing.T) {
	d := t.TempDir()
	writeTest(t, d, "doc.md", "# Doc\n\n"+
		"| Name | Count | Note |\n|:-----|------:|:----:|\n| a \\| b | 1 | **x** |\n| c | 2 |\n\n"+
		"![Diagram](img/diagram.png) ![](missing.png) ![remote](https://example.com/a.png) ![bad](vbscript:x) ![enc](img/sp%20ace.png) ![doc](report.pdf)\n\n"+
		"~~gone~~ and ~~**still**~~\n\n"+
		"- [ ] open\n- [x] done\n\n"+
		"![[report.pdf]]\n\n"+
		"not | a table\n")
	for _, f := range []string{"img/diagram.png", "img/sp ace.png", "report.pdf"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, f)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	w := openTest(t, d)
	out := renderPage(t, w, "", "doc")
	assertSafeHTML(t, out)
	for _, want := range []string{
		`<table class="jk-table"><thead><tr><th class="jk-align-left">Name</th><th class="jk-align-right">Count</th><th class="jk-align-center">Note</th></tr></thead><tbody>`,
		`<tr><td class="jk-align-left">a | b</td><td class="jk-align-right">1</td><td class="jk-align-center"><strong>x</strong></td></tr>`,
		`<tr><td class="jk-align-left">c</td><td class="jk-align-right">2</td><td class="jk-align-center"></td></tr>`,
		`<img src="/files/img/diagram.png" alt="Diagram" loading="lazy">`,
		`<span class="jk-unresolved">missing.png</span>`,
		`<a class="jk-remote-image" href="https://example.com/a.png" rel="noopener noreferrer">remote</a>`,
		` bad `,
		`<img src="/files/img/sp%20ace.png" alt="enc" loading="lazy">`,
		`<a class="jk-ref" href="/files/report.pdf">doc</a>`,
		`<del>gone</del> and <del><strong>still</strong></del>`,
		`<li><input type="checkbox" disabled> open</li>`,
		`<li><input type="checkbox" disabled checked> done</li>`,
		`<iframe class="jk-pdf" src="/files/report.pdf" title="report.pdf" loading="lazy"></iframe>`,
		`<a href="/files/report.pdf" download>Download report.pdf</a>`,
		`<p>not | a table</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "vbscript:") {
		t.Fatalf("unsafe image source kept:\n%s", out)
	}
	// A Markdown image makes its file readable to the page's readers, like
	// an embed does; a web address is not a workspace file.
	if _, ok := w.ReadableFile("", "img/diagram.png"); !ok {
		t.Fatal("image file not readable")
	}
	if _, ok := w.ReadableFile("", "img/sp ace.png"); !ok {
		t.Fatal("percent-encoded image file not readable")
	}
}
