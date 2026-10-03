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
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start > cuts[j].start })
	for _, c := range cuts {
		body = body[:c.start] + c.text + body[c.end:]
	}
	return body
}

var (
	headingPattern    = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	rulePattern       = regexp.MustCompile(`^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	quotePattern      = regexp.MustCompile(`^ {0,3}> ?`)
	listPattern       = regexp.MustCompile(`^( *)([-*+]|[0-9]{1,9}[.)])[ \t]+(.*)$`)
	blockEmbedPattern = regexp.MustCompile(`^ {0,3}!\[\[([^\[\]\n]+)\]\][ \t]*$`)
	taskPattern       = regexp.MustCompile(`^\[([ xX])\][ \t]+`)
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

var (
	mediaImage = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".svg": true}
	mediaVideo = map[string]bool{".mp4": true, ".webm": true, ".ogv": true, ".mov": true}
	mediaAudio = map[string]bool{".mp3": true, ".ogg": true, ".oga": true, ".wav": true, ".m4a": true, ".flac": true}
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
	inlineStrong   = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	inlineEm       = regexp.MustCompile(`\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
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
	for s != "" {
		start, end, kind := -1, -1, ""
		consider := func(k string, loc []int) {
			if loc != nil && (start < 0 || loc[0] < start) {
				start, end, kind = loc[0], loc[1], k
			}
		}
		if i := strings.IndexByte(s, '`'); i >= 0 {
			n := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
			fence := strings.Repeat("`", n)
			if j := strings.Index(s[i+n:], fence); j >= 0 {
				consider("code", []int{i, i + n + j + n})
			}
		}
		consider("wiki", inlineWiki.FindStringIndex(s))
		consider("link", inlineLink.FindStringIndex(s))
		consider("strong", inlineStrong.FindStringIndex(s))
		consider("em", inlineEm.FindStringIndex(s))
		consider("url", inlineAutolink.FindStringIndex(s))
		if m := mentionPattern.FindStringSubmatchIndex(s); m != nil {
			consider("mention", []int{m[4] - 1, m[5]})
		}
		if start < 0 {
			b.WriteString(html.EscapeString(s))
			break
		}
		b.WriteString(html.EscapeString(s[:start]))
		b.WriteString(r.token(from, kind, s[start:end]))
		s = s[end:]
	}
	return b.String()
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
	case "url":
		return `<a href="` + html.EscapeString(t) + `" rel="noopener noreferrer">` + html.EscapeString(t) + "</a>"
	case "strong":
		return "<strong>" + r.inline(from, t[2:len(t)-2]) + "</strong>"
	case "em":
		return "<em>" + r.inline(from, t[1:len(t)-1]) + "</em>"
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
