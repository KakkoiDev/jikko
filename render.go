package jikko

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Links tells the renderer where a page or a workspace file is served.
type Links struct {
	Page func(pagePath string) string
	File func(filePath string) string
}

// DefaultLinks serves pages under /p/ and files under /files/, each path
// segment escaped.
var DefaultLinks = Links{
	Page: func(p string) string { return "/p/" + escapePath(p) },
	File: func(p string) string { return "/files/" + escapePath(p) },
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// Renderer turns page Markdown into HTML for one actor. It is safe by
// construction: every character of source text is escaped, and the only
// elements in its output are the ones it writes itself. Raw HTML in a page
// is shown as text, never passed through. References render as links only
// to pages the actor may read; an embed of a page the actor may not read
// renders as unavailable, exactly as a missing one does.
type Renderer struct {
	w      *Workspace
	actor  string
	links  Links
	stack  []string
	assets *assetIndex
}

// maxEmbedDepth bounds nested embeds independently of cycle detection.
const maxEmbedDepth = 5

// Renderer returns a renderer for actor.
func (w *Workspace) Renderer(actor string, links Links) *Renderer {
	return &Renderer{w: w, actor: actor, links: links}
}

// Page renders a page's body. Comment anchors become highlights linked to
// their thread; the threads themselves are left out, for a sidebar to show.
func (r *Renderer) Page(p *Page) template.HTML {
	r.stack = append(r.stack[:0], p.Path)
	return template.HTML(r.body(p.Path, p.Body))
}

// Inline renders one paragraph of text, such as a comment message.
func (r *Renderer) Inline(text string) template.HTML {
	return template.HTML(r.inline("", clean(text)))
}

// Private-use code points stand in for comment anchor markers while a body
// is parsed, so the markers survive escaping. Source text cannot forge them:
// clean replaces any it contains.
const (
	anchorOpen  = ""
	anchorID    = ""
	anchorClose = ""
)

func clean(s string) string {
	return strings.NewReplacer(anchorOpen, "�", anchorID, "�", anchorClose, "�", "\r\n", "\n").Replace(s)
}

// prepare removes comment threads and turns anchor markers into
// placeholders. Malformed markup is left as text.
func prepare(body string) string {
	body = clean(body)
	spans, _ := scanComments(body)
	type cut struct {
		start, end int
		text       string
	}
	var cuts []cut
	for _, s := range spans {
		if s.threadStart >= 0 && s.openStart >= 0 && s.closeStart >= s.openEnd {
			cuts = append(cuts,
				cut{s.threadStart, s.threadEnd, ""},
				cut{s.openStart, s.openEnd, anchorOpen + s.id + anchorID},
				cut{s.closeStart, s.closeEnd, anchorClose})
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start < cuts[j].start })
	var b strings.Builder
	last := 0
	for _, c := range cuts {
		b.WriteString(body[last:c.start])
		b.WriteString(c.text)
		last = c.end
	}
	b.WriteString(body[last:])
	return b.String()
}

var (
	headingPattern    = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	rulePattern       = regexp.MustCompile(`^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	quotePattern      = regexp.MustCompile(`^ {0,3}> ?`)
	listPattern       = regexp.MustCompile(`^( *)([-*+]|[0-9]{1,9}[.)])[ \t]+(.*)$`)
	blockEmbedPattern = regexp.MustCompile(`^ {0,3}!\[\[([^\[\]\n]+)\]\][ \t]*$`)
	taskPattern       = regexp.MustCompile(`^\[([ xX])\][ \t]+`)
	// tableDelimPattern is the row under a table's header: cells of dashes
	// with optional colons for alignment (GitHub Flavored Markdown).
	tableDelimPattern = regexp.MustCompile(`^ {0,3}\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
)

func (r *Renderer) body(from, body string) string {
	return r.blocks(from, strings.Split(prepare(body), "\n"))
}

// blocks renders a sequence of lines as block elements.
func (r *Renderer) blocks(from string, lines []string) string {
	var b strings.Builder
	var para []string
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + r.inline(from, strings.Join(para, "\n")) + "</p>\n")
			para = nil
		}
	}
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			i++
		case fencePattern.MatchString(line):
			flush()
			m := fencePattern.FindStringSubmatch(line)
			lang := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), m[1][:1]))
			var code []string
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), m[1]) {
				code = append(code, lines[i])
				i++
			}
			i++ // closing fence
			class := "jk-code"
			if lang == "mermaid" {
				// Mermaid source stays canonical; it is shown, not executed.
				class += " jk-mermaid"
			}
			b.WriteString(`<pre class="` + class + `"`)
			if lang != "" {
				b.WriteString(` data-lang="` + html.EscapeString(lang) + `"`)
			}
			b.WriteString("><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case headingPattern.MatchString(line):
			flush()
			m := headingPattern.FindStringSubmatch(line)
			level := len(m[1])
			fmt.Fprintf(&b, "<h%d>%s</h%d>\n", level, r.inline(from, m[2]), level)
			i++
		case rulePattern.MatchString(line):
			flush()
			b.WriteString("<hr>\n")
			i++
		case quotePattern.MatchString(line):
			flush()
			var quoted []string
			for i < len(lines) && quotePattern.MatchString(lines[i]) {
				quoted = append(quoted, quotePattern.ReplaceAllString(lines[i], ""))
				i++
			}
			b.WriteString("<blockquote>\n" + r.blocks(from, quoted) + "</blockquote>\n")
		case listPattern.MatchString(line):
			flush()
			n := r.list(&b, from, lines[i:])
			i += n
		case blockEmbedPattern.MatchString(line):
			flush()
			ref := strings.TrimSpace(blockEmbedPattern.FindStringSubmatch(line)[1])
			b.WriteString(r.embed(from, ref))
			i++
		case isTableStart(lines, i):
			flush()
			i += r.table(&b, from, lines[i:])
		default:
			para = append(para, line)
			i++
		}
	}
	flush()
	return b.String()
}

// list renders the list starting at lines[0] and returns how many lines it
// used. Items are lines with the first item's indent and marker kind; lines
// indented further belong to the item before them, so nested lists nest.
func (r *Renderer) list(b *strings.Builder, from string, lines []string) int {
	first := listPattern.FindStringSubmatch(lines[0])
	indent := len(first[1])
	ordered := !strings.ContainsAny(first[2][:1], "-*+")
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	b.WriteString("<" + tag + ">\n")
	i := 0
	for i < len(lines) {
		m := listPattern.FindStringSubmatch(lines[i])
		if m == nil || len(m[1]) != indent || ordered == strings.ContainsAny(m[2][:1], "-*+") {
			break
		}
		text := m[3]
		var sub []string
		i++
		for i < len(lines) {
			l := lines[i]
			if strings.TrimSpace(l) == "" {
				// A blank line continues the list only if more of it follows.
				if i+1 < len(lines) && leadingSpaces(lines[i+1]) > indent {
					sub = append(sub, "")
					i++
					continue
				}
				break
			}
			if leadingSpaces(l) <= indent {
				break
			}
			sub = append(sub, strings.TrimPrefix(l, strings.Repeat(" ", indent+2)))
			i++
		}
		b.WriteString("<li>")
		if t := taskPattern.FindStringSubmatch(text); t != nil {
			checked := ""
			if t[1] != " " {
				checked = " checked"
			}
			b.WriteString(`<input type="checkbox" disabled` + checked + `> `)
			text = text[len(t[0]):]
		}
		b.WriteString(r.inline(from, text))
		if len(sub) > 0 {
			b.WriteString("\n" + r.blocks(from, sub))
		}
		b.WriteString("</li>\n")
		// A blank line between items does not end the list.
		if i < len(lines) && strings.TrimSpace(lines[i]) == "" && i+1 < len(lines) {
			if next := listPattern.FindStringSubmatch(lines[i+1]); next != nil && len(next[1]) == indent {
				i++
			}
		}
	}
	b.WriteString("</" + tag + ">\n")
	return i
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

// isTableStart reports whether lines[i] opens a GFM table: a header row with
// a delimiter row of the same width under it.
func isTableStart(lines []string, i int) bool {
	if i+1 >= len(lines) || !strings.Contains(lines[i], "|") || !tableDelimPattern.MatchString(lines[i+1]) {
		return false
	}
	return len(tableCells(lines[i])) == len(tableCells(lines[i+1]))
}

// tableCells splits a table row into its cells. A pipe escaped as \| is
// text, and one leading and one trailing pipe are delimiters, not cells.
func tableCells(row string) []string {
	row = strings.TrimSpace(row)
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteByte('|')
			i++
		case row[i] == '|':
			cells = append(cells, cell.String())
			cell.Reset()
		default:
			cell.WriteByte(row[i])
		}
	}
	cells = append(cells, cell.String())
	if len(cells) > 0 && strings.HasPrefix(row, "|") {
		cells = cells[1:]
	}
	if len(cells) > 0 && strings.HasSuffix(row, "|") && !strings.HasSuffix(row, "\\|") {
		cells = cells[:len(cells)-1]
	}
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// table renders the GFM table starting at lines[0] and returns how many
// lines it used. The body ends at a blank line or a line without a pipe;
// short rows are padded and long ones cut to the header's width.
func (r *Renderer) table(b *strings.Builder, from string, lines []string) int {
	header := tableCells(lines[0])
	aligns := make([]string, len(header))
	for i, d := range tableCells(lines[1]) {
		switch left, right := strings.HasPrefix(d, ":"), strings.HasSuffix(d, ":"); {
		case left && right:
			aligns[i] = "center"
		case right:
			aligns[i] = "right"
		case left:
			aligns[i] = "left"
		}
	}
	cell := func(tag string, i int, text string) {
		b.WriteString("<" + tag)
		if aligns[i] != "" {
			b.WriteString(` class="jk-align-` + aligns[i] + `"`)
		}
		b.WriteString(">" + r.inline(from, text) + "</" + tag + ">")
	}
	b.WriteString(`<table class="jk-table"><thead><tr>`)
	for i, h := range header {
		cell("th", i, h)
	}
	b.WriteString("</tr></thead><tbody>\n")
	n := 2
	for n < len(lines) && strings.TrimSpace(lines[n]) != "" && strings.Contains(lines[n], "|") {
		cells := tableCells(lines[n])
		b.WriteString("<tr>")
		for i := range header {
			text := ""
			if i < len(cells) {
				text = cells[i]
			}
			cell("td", i, text)
		}
		b.WriteString("</tr>\n")
		n++
	}
	b.WriteString("</tbody></table>\n")
	return n
}

var (
	mediaImage = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true}
	mediaVideo = map[string]bool{".mp4": true, ".webm": true, ".ogv": true, ".mov": true}
	mediaAudio = map[string]bool{".mp3": true, ".ogg": true, ".oga": true, ".wav": true, ".m4a": true, ".flac": true}
	mediaPDF   = map[string]bool{".pdf": true}
)

// embed renders ![[ref]] on a line of its own (specification §5.2): a
// readable page inside a bordered container, a media file natively, any
// other file as a card. Cycles and excessive depth stop expansion (§16).
func (r *Renderer) embed(from, ref string) string {
	anchor := `<code class="jk-embed-ref">![[` + html.EscapeString(ref) + `]]</code>`
	if p, ok := r.w.Resolve(ref); ok && r.w.Allowed(r.actor, p, Read) {
		head := `<figure class="jk-embed" data-path="` + html.EscapeString(p.Path) + `"><figcaption><a href="` + html.EscapeString(r.links.Page(p.Path)) + `">` + html.EscapeString(p.Title) + `</a> ` + anchor + `</figcaption>`
		for _, seen := range r.stack {
			if seen == p.Path {
				return head + `<p class="jk-embed-stop">Embed cycle: ` + html.EscapeString(p.Path) + ` already appears above.</p></figure>` + "\n"
			}
		}
		if len(r.stack) >= maxEmbedDepth {
			return head + `<p class="jk-embed-stop">Embeds nested too deeply to expand.</p></figure>` + "\n"
		}
		var inner string
		if p.Kind == View {
			inner = r.viewList(p)
		} else {
			r.stack = append(r.stack, p.Path)
			inner = r.body(p.Path, p.Body)
			r.stack = r.stack[:len(r.stack)-1]
		}
		return head + `<div class="jk-embed-body">` + inner + "</div></figure>\n"
	}
	if file, ok := r.file(from, ref); ok {
		src := html.EscapeString(r.links.File(file))
		name := html.EscapeString(path.Base(file))
		ext := strings.ToLower(path.Ext(file))
		var media string
		switch {
		case mediaImage[ext]:
			media = `<img src="` + src + `" alt="` + name + `" loading="lazy">`
		case mediaVideo[ext]:
			media = `<video controls preload="metadata" src="` + src + `"></video>`
		case mediaAudio[ext]:
			media = `<audio controls preload="metadata" src="` + src + `"></audio>`
		case mediaPDF[ext]:
			// The document is framed from /files/, whose sandboxing policy
			// keeps anything inside it out of the application's origin.
			media = `<iframe class="jk-pdf" src="` + src + `" title="` + name + `" loading="lazy"></iframe>`
			anchor += ` <a href="` + src + `" download>Download ` + name + `</a>`
		default:
			return `<a class="jk-file" href="` + src + `" download>` + name + "</a>\n"
		}
		return `<figure class="jk-media">` + media + `<figcaption>` + anchor + "</figcaption></figure>\n"
	}
	return `<p class="jk-embed jk-missing">` + anchor + " is not available.</p>\n"
}

// viewList renders an embedded View as the list of pages it selects.
func (r *Renderer) viewList(p *Page) string {
	res, err := r.w.EvaluateView(r.actor, p.Path)
	if err != nil {
		return `<p class="jk-embed-stop">` + html.EscapeString(err.Error()) + "</p>"
	}
	if len(res.Pages) == 0 {
		return `<p class="jk-muted">No pages.</p>`
	}
	var b strings.Builder
	b.WriteString(`<ul class="jk-view-list">`)
	for _, it := range res.Pages {
		b.WriteString(`<li><a href="` + html.EscapeString(r.links.Page(it.Path)) + `">` + html.EscapeString(it.Title) + `</a></li>`)
	}
	b.WriteString("</ul>")
	return b.String()
}

// file resolves a reference to a workspace file.
func (r *Renderer) file(from, ref string) (string, bool) {
	if !isAssetRef(ref) {
		return "", false
	}
	if r.assets == nil {
		idx, err := r.w.assets()
		if err != nil {
			return "", false
		}
		r.assets = &idx
	}
	f, ok, _ := r.assets.resolve(from, ref)
	return f, ok
}

var (
	inlineWiki     = regexp.MustCompile(`!?\[\[([^\[\]\n]+)\]\]`)
	inlineLink     = regexp.MustCompile(`\[([^\[\]\n]+)\]\(([^()\s]+)\)`)
	inlineImage    = regexp.MustCompile(`!\[([^\[\]\n]*)\]\(([^()\s]+)\)`)
	inlineStrong   = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	inlineEm       = regexp.MustCompile(`\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	inlineDel      = regexp.MustCompile(`~~([^~\n]+?)~~`)
	inlineAutolink = regexp.MustCompile(`https?://[^\s<>()\[\]"']+[^\s<>()\[\]"'.,;:!?]`)
)

// inline renders a run of text. Comment anchors are split out first, so
// every element it writes wraps content rendered on its own and the output
// is always well formed.
func (r *Renderer) inline(from, s string) string {
	if i := strings.Index(s, anchorOpen); i >= 0 {
		rest := s[i+len(anchorOpen):]
		j := strings.Index(rest, anchorID)
		if j < 0 {
			return r.inline(from, s[:i]) + r.inline(from, rest)
		}
		id := rest[:j]
		rest = rest[j+len(anchorID):]
		// Find this anchor's end, allowing anchors nested inside it.
		depth, end := 0, -1
		for k := 0; k < len(rest); {
			switch {
			case strings.HasPrefix(rest[k:], anchorOpen):
				depth++
				k += len(anchorOpen)
			case strings.HasPrefix(rest[k:], anchorClose):
				if depth == 0 {
					end = k
					k = len(rest)
					continue
				}
				depth--
				k += len(anchorClose)
			default:
				k++
			}
		}
		inner, after := rest, ""
		if end >= 0 {
			inner, after = rest[:end], rest[end+len(anchorClose):]
		}
		safeID := html.EscapeString(id)
		return r.inline(from, s[:i]) +
			`<mark class="jk-anchor" id="anchor-` + safeID + `">` + r.inline(from, inner) + `</mark>` +
			`<a class="jk-anchor-ref" href="#comment-` + safeID + `">` + safeID + `</a>` +
			r.inline(from, after)
	}
	s = strings.ReplaceAll(s, anchorClose, "")
	var b strings.Builder
	scan := newInlineScan(s)
	pos := 0
	for pos < len(s) {
		start, end, kind := scan.next(pos)
		if start < 0 {
			b.WriteString(html.EscapeString(s[pos:]))
			break
		}
		b.WriteString(html.EscapeString(s[pos:start]))
		b.WriteString(r.token(from, kind, s[start:end]))
		pos = end
	}
	return b.String()
}

// inlineKinds are the inline tokens in the order ties are broken.
var inlineKinds = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"wiki", inlineWiki}, {"image", inlineImage}, {"link", inlineLink}, {"strong", inlineStrong},
	{"em", inlineEm}, {"del", inlineDel}, {"url", inlineAutolink}, {"mention", mentionPattern},
}

// inlineScan finds the earliest inline token at or after a position. Each
// pattern is searched again only once the position passes its last match,
// so a run of many tokens costs one pass per pattern over the text rather
// than one pass per token, which was quadratic in the length of a paragraph.
// A match found from an earlier position stays the leftmost one from a later
// position up to it: no pattern looks behind, and the only anchor, the
// mention's start of text, is searched again whenever a token ends at "@".
type inlineScan struct {
	s     string
	found [][]int // absolute [start, end] per kind, nil when none remains
	done  []bool
	// tick is the first backtick at or after the last code search; the code
	// span cached with it holds until the position passes that backtick.
	tick int
	code []int
}

func newInlineScan(s string) *inlineScan {
	return &inlineScan{s: s, found: make([][]int, len(inlineKinds)), done: make([]bool, len(inlineKinds)), tick: -1}
}

func (c *inlineScan) next(pos int) (start, end int, kind string) {
	start = -1
	consider := func(k string, loc []int) {
		if loc != nil && (start < 0 || loc[0] < start) {
			start, end, kind = loc[0], loc[1], k
		}
	}
	if c.tick < pos {
		c.tick, c.code = len(c.s), nil
		if i := strings.IndexByte(c.s[pos:], '`'); i >= 0 {
			i += pos
			n := len(c.s[i:]) - len(strings.TrimLeft(c.s[i:], "`"))
			c.tick = i
			if j := strings.Index(c.s[i+n:], strings.Repeat("`", n)); j >= 0 {
				c.code = []int{i, i + n + j + n}
			}
		}
	}
	consider("code", c.code)
	for k, kind := range inlineKinds {
		stale := !c.done[k] || (c.found[k] != nil && c.found[k][0] < pos)
		if kind.name == "mention" && c.s[pos] == '@' {
			stale = true
		}
		if stale {
			c.done[k], c.found[k] = true, nil
			if kind.name == "mention" {
				if m := kind.pattern.FindStringSubmatchIndex(c.s[pos:]); m != nil {
					c.found[k] = []int{pos + m[4] - 1, pos + m[5]}
				}
			} else if loc := kind.pattern.FindStringIndex(c.s[pos:]); loc != nil {
				c.found[k] = []int{pos + loc[0], pos + loc[1]}
			}
		}
		consider(kind.name, c.found[k])
	}
	return start, end, kind
}

func (r *Renderer) token(from, kind, t string) string {
	switch kind {
	case "code":
		n := len(t) - len(strings.TrimLeft(t, "`"))
		return "<code>" + html.EscapeString(strings.TrimSpace(t[n:len(t)-n])) + "</code>"
	case "wiki":
		inner := t[strings.Index(t, "[[")+2 : len(t)-2]
		target, alias, _ := strings.Cut(inner, "|")
		target = strings.TrimSpace(target)
		label := strings.TrimSpace(alias)
		if p, ok := r.w.Resolve(target); ok && r.w.Allowed(r.actor, p, Read) {
			if label == "" {
				label = p.Title
			}
			return `<a class="jk-ref" href="` + html.EscapeString(r.links.Page(p.Path)) + `">` + html.EscapeString(label) + "</a>"
		}
		if label == "" {
			label = target
		}
		if f, ok := r.file(from, target); ok {
			return `<a class="jk-ref" href="` + html.EscapeString(r.links.File(f)) + `">` + html.EscapeString(label) + "</a>"
		}
		return `<span class="jk-unresolved">` + html.EscapeString(label) + "</span>"
	case "link":
		m := inlineLink.FindStringSubmatch(t)
		if href, ok := safeHref(m[2]); ok {
			return `<a href="` + html.EscapeString(href) + `" rel="noopener noreferrer">` + html.EscapeString(m[1]) + "</a>"
		}
		return html.EscapeString(m[1])
	case "image":
		m := inlineImage.FindStringSubmatch(t)
		return r.image(from, m[1], m[2])
	case "url":
		return `<a href="` + html.EscapeString(t) + `" rel="noopener noreferrer">` + html.EscapeString(t) + "</a>"
	case "strong":
		return "<strong>" + r.inline(from, t[2:len(t)-2]) + "</strong>"
	case "em":
		return "<em>" + r.inline(from, t[1:len(t)-1]) + "</em>"
	case "del":
		return "<del>" + r.inline(from, t[2:len(t)-2]) + "</del>"
	case "mention":
		name := strings.TrimRight(t[1:], "./-")
		tail := t[1+len(name):]
		out := `<span class="jk-mention">@` + html.EscapeString(name) + "</span>"
		if p, ok := r.w.ResolveIdentity(name); ok && r.w.Allowed(r.actor, p, Read) {
			out = `<a class="jk-mention" href="` + html.EscapeString(r.links.Page(p.Path)) + `">@` + html.EscapeString(name) + "</a>"
		}
		return out + html.EscapeString(tail)
	}
	return html.EscapeString(t)
}

// image renders ![alt](src), ordinary Markdown for a picture. The source
// takes the same rules as a link: a path in the workspace is served through
// the file route, to a reader of this page; a web address is shown as a link
// rather than fetched, since the interface loads no third-party content; and
// any other scheme is refused and only the text shown.
func (r *Renderer) image(from, alt, src string) string {
	label := alt
	if label == "" {
		label = src
	}
	href, ok := safeHref(src)
	if !ok {
		return html.EscapeString(label)
	}
	if u, err := url.Parse(href); err == nil && u.Scheme != "" {
		if u.Scheme == "mailto" {
			return html.EscapeString(label)
		}
		return `<a class="jk-remote-image" href="` + html.EscapeString(href) + `" rel="noopener noreferrer">` + html.EscapeString(label) + "</a>"
	}
	if strings.HasPrefix(href, "/") {
		return `<img src="` + html.EscapeString(href) + `" alt="` + html.EscapeString(alt) + `" loading="lazy">`
	}
	file, ok := r.file(from, imageSource(src))
	if !ok {
		return `<span class="jk-unresolved">` + html.EscapeString(label) + "</span>"
	}
	target := html.EscapeString(r.links.File(file))
	if !mediaImage[strings.ToLower(path.Ext(file))] {
		return `<a class="jk-ref" href="` + target + `">` + html.EscapeString(label) + "</a>"
	}
	return `<img src="` + target + `" alt="` + html.EscapeString(alt) + `" loading="lazy">`
}

// imageSource turns the source of a Markdown image into a file reference:
// percent-encoding is undone and a leading ./ dropped.
func imageSource(src string) string {
	if decoded, err := url.PathUnescape(src); err == nil {
		src = decoded
	}
	return strings.TrimPrefix(src, "./")
}

// imageSources lists the workspace files a body's Markdown images name, in
// prose, so a reader of the page may fetch them (see ReadableFile).
func imageSources(body string) []string {
	var out []string
	scanProse(body, func(line string) {
		for _, m := range inlineImage.FindAllStringSubmatch(line, -1) {
			if u, err := url.Parse(m[2]); err == nil && u.Scheme == "" && !strings.HasPrefix(m[2], "/") {
				out = append(out, imageSource(m[2]))
			}
		}
	})
	return out
}

// safeHref accepts web and mail links and links within the site. Anything
// else -- javascript:, data:, vbscript:, or a scheme the browser might treat
// as one -- is refused and its text shown plain.
func safeHref(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return u.String(), true
	case "":
		if strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\") {
			return "", false
		}
		return u.String(), true
	}
	return "", false
}
