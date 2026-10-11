package jikko

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SourceAnchor turns text selected in a rendered page back into the source
// excerpt a comment anchors to (specification §8). A selection of "the
// first version" over "the **first version**" is not found in the source as
// written; the rendered text of each prose line is rebuilt with a map from
// every visible byte to the bytes it came from, the selection is located in
// it, and the span of source it covers is the anchor. A selection that
// touches a [[reference]] or an image takes the whole token, so an anchor
// never splits one. A selection that starts or ends inside emphasis or a
// code span widens to the whole token for the same reason. Text that is not
// rendered text but a source excerpt as written is kept as it is.
//
// The selection must come from one line of source and occur once in the
// page's rendered prose; the error says which rule failed.
func (w *Workspace) SourceAnchor(actor, ref, selected string) (string, error) {
	p, err := w.readable(actor, ref)
	if err != nil {
		return "", err
	}
	selected = strings.Join(strings.Fields(selected), " ")
	if selected == "" {
		return "", errors.New("select some text to comment on")
	}
	body := clean(p.Body)
	spans, _ := scanComments(body)
	label := func(target, alias string) string {
		if alias != "" {
			return alias
		}
		if q, ok := w.Resolve(target); ok && w.Allowed(actor, q, Read) {
			return q.Title
		}
		return target
	}
	excerpt, err := mapSelection(body, spans, selected, label)
	if err == nil {
		return excerpt, nil
	}
	// Not rendered text: perhaps a source excerpt pasted as written, such
	// as "**first**". It is kept as long as it does not cut a reference in
	// two, as "the ship" would in [[ship|the ship task]].
	if at, found := findAnchor(body, spans, selected); found == nil {
		marked := body[:at] + "<!--comment:x-->" + selected + "<!--/comment:x-->" + body[at+len(selected):]
		if refsKey(body) == refsKey(marked) && !strings.Contains(selected, "[[") && !strings.Contains(selected, "]]") {
			return selected, nil
		}
	}
	return "", err
}

// mapSelection locates selected in the rendered text of body's prose lines
// and returns the source it covers.
func mapSelection(body string, spans []commentSpan, selected string, label func(target, alias string) string) (string, error) {
	hidden := make([]bool, len(body))
	hide := func(from, to int) {
		for i := from; i >= 0 && i < to && i < len(body); i++ {
			hidden[i] = true
		}
	}
	for _, s := range spans {
		if s.threadStart >= 0 {
			hide(s.threadStart, s.threadEnd)
		}
		if s.openStart >= 0 && s.closeStart >= s.openEnd {
			hide(s.openStart, s.openEnd)
			hide(s.closeStart, s.closeEnd)
		}
	}
	found := ""
	matches := 0
	fence := ""
	for offset := 0; offset < len(body); {
		end := strings.IndexByte(body[offset:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += offset
		}
		line := body[offset:end]
		next := end + 1
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
			offset = next
			continue
		}
		if m := fencePattern.FindStringSubmatch(line); m != nil {
			fence = m[1]
			offset = next
			continue
		}
		// The line without comment markup, each byte mapped to its source.
		var cleaned []byte
		var orig []int
		for i := offset; i < end; i++ {
			if !hidden[i] {
				cleaned = append(cleaned, body[i])
				orig = append(orig, i)
			}
		}
		text := string(cleaned)
		from := blockPrefix(text)
		v := visibleLine(text[from:], label)
		for i := range v.start {
			v.start[i] = orig[v.start[i]+from]
			v.end[i] = orig[v.end[i]-1+from] + 1
			if v.tokStart[i] >= 0 {
				v.tokStart[i] = orig[v.tokStart[i]+from]
				v.tokEnd[i] = orig[v.tokEnd[i]-1+from] + 1
			}
		}
		v.collapse()
		for at := 0; ; {
			i := strings.Index(v.text[at:], selected)
			if i < 0 {
				break
			}
			i += at
			at = i + 1
			matches++
			if matches == 1 {
				// A selection starting or ending inside a token takes
				// the whole token: a marker between a delimiter and its
				// text would split the token.
				last := i + len(selected) - 1
				from, to := v.start[i], v.end[last]
				if v.tokStart[i] >= 0 {
					from = v.tokStart[i]
				}
				if v.tokEnd[last] >= 0 && v.tokEnd[last] > to {
					to = v.tokEnd[last]
				}
				for k := from; k < to; k++ {
					if hidden[k] {
						return "", fmt.Errorf("%q crosses the edge of an existing comment anchor; select text inside it or outside it", selected)
					}
				}
				found = body[from:to]
				if strings.Contains(found, "\r") {
					return "", fmt.Errorf("%q spans a bare carriage return in the source; select text on one side of it", selected)
				}
			}
		}
		offset = next
	}
	switch {
	case matches == 0:
		if strings.Contains(selected, " ") {
			return "", fmt.Errorf("%q was not found in the page's prose as one line; select text within a single line of the source", selected)
		}
		return "", fmt.Errorf("%q was not found in the page's prose", selected)
	case matches > 1:
		return "", fmt.Errorf("%q occurs more than once in the page; select a longer excerpt", selected)
	}
	return found, nil
}

var blockPrefixPattern = regexp.MustCompile(`^(?: {0,3}>[ ]?)*(?: {0,3}#{1,6}[ \t]+| *(?:[-*+]|[0-9]{1,9}[.)])[ \t]+(?:\[[ xX]\][ \t]+)?)?`)

// blockPrefix returns the length of the block syntax at the start of a
// line -- quote marks, a heading's hashes, a list marker and checkbox --
// which renders as structure, not text.
func blockPrefix(line string) int {
	return len(blockPrefixPattern.FindString(line))
}

// visible is the rendered text of a run of source with, for each byte of
// text, the source range it stands for.
type visible struct {
	text       string
	start, end []int
	// tokStart and tokEnd give the outermost emphasis or code token a byte
	// lies in, or -1.
	tokStart, tokEnd []int
}

func (v *visible) add(text string, start, end int) {
	v.text += text
	for i := 0; i < len(text); i++ {
		v.start = append(v.start, start)
		v.end = append(v.end, end)
		v.tokStart = append(v.tokStart, -1)
		v.tokEnd = append(v.tokEnd, -1)
	}
}

// addBytes adds text whose bytes come one for one from source at base.
func (v *visible) addBytes(text string, base int) {
	v.text += text
	for i := 0; i < len(text); i++ {
		v.start = append(v.start, base+i)
		v.end = append(v.end, base+i+1)
		v.tokStart = append(v.tokStart, -1)
		v.tokEnd = append(v.tokEnd, -1)
	}
}

// collapse folds runs of whitespace into one space and trims the ends, as
// a browser renders them, keeping the source range of each run.
func (v *visible) collapse() {
	out := visible{}
	var b strings.Builder
	keep := func(i int) {
		out.start = append(out.start, v.start[i])
		out.end = append(out.end, v.end[i])
		out.tokStart = append(out.tokStart, v.tokStart[i])
		out.tokEnd = append(out.tokEnd, v.tokEnd[i])
	}
	space := false
	for i := 0; i < len(v.text); i++ {
		c := v.text[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			if !space && b.Len() > 0 {
				space = true
				b.WriteByte(' ')
				keep(i)
			} else if space {
				out.end[len(out.end)-1] = v.end[i]
			}
			continue
		}
		space = false
		b.WriteByte(c)
		keep(i)
	}
	if space {
		n := b.Len() - 1
		out.start, out.end, out.tokStart, out.tokEnd = out.start[:n], out.end[:n], out.tokStart[:n], out.tokEnd[:n]
		out.text = b.String()[:n]
	} else {
		out.text = b.String()
	}
	*v = out
}

// wrap adds the text inside an emphasis token and records the token, so a
// selection that starts or ends inside it is widened to the whole token.
func (v *visible) wrap(inner string, start, end, delim int, label func(target, alias string) string) {
	before := len(v.text)
	visibleInto(v, inner, start+delim, label)
	v.extend(before, start, end)
}

// extend records that the visible bytes added since before lie in the
// token spanning [start, end). An outer token overrides an inner one.
func (v *visible) extend(before, start, end int) {
	for k := before; k < len(v.text); k++ {
		v.tokStart[k], v.tokEnd[k] = start, end
	}
}

// visibleLine renders one line of inline Markdown to its visible text the
// way Renderer.inline does, mapping each byte back to the source. base is
// the offset of s in the text the map is for.
func visibleLine(s string, label func(target, alias string) string) visible {
	v := visible{}
	visibleInto(&v, s, 0, label)
	return v
}

func visibleInto(v *visible, s string, base int, label func(target, alias string) string) {
	scan := newInlineScan(s)
	pos := 0
	for pos < len(s) {
		start, end, kind := scan.next(pos)
		if start < 0 {
			v.addBytes(s[pos:], base+pos)
			break
		}
		v.addBytes(s[pos:start], base+pos)
		t := s[start:end]
		switch kind {
		case "code":
			n := len(t) - len(strings.TrimLeft(t, "`"))
			inner := t[n : len(t)-n]
			lead := len(inner) - len(strings.TrimLeft(inner, " \t"))
			before := len(v.text)
			v.addBytes(strings.TrimSpace(inner), base+start+n+lead)
			v.extend(before, base+start, base+end)
		case "wiki":
			inner := t[strings.Index(t, "[[")+2 : len(t)-2]
			target, alias, _ := strings.Cut(inner, "|")
			v.add(label(strings.TrimSpace(target), strings.TrimSpace(alias)), base+start, base+end)
		case "image":
			m := inlineImage.FindStringSubmatch(t)
			text := m[1]
			if text == "" {
				text = m[2]
			}
			v.add(text, base+start, base+end)
		case "link":
			m := inlineLink.FindStringSubmatchIndex(t)
			v.addBytes(t[m[2]:m[3]], base+start+m[2])
		case "strong", "del":
			v.wrap(t[2:len(t)-2], base+start, base+end, 2, label)
		case "em":
			v.wrap(t[1:len(t)-1], base+start, base+end, 1, label)
		default:
			v.addBytes(t, base+start)
		}
		pos = end
	}
}
