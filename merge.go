package jikko

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Conflict is the structured result of an edit that could not be merged
// with a concurrent change (specification §11). "Current" is the page as it
// is now, "yours" the caller's edit, and "base" the revision the caller
// edited. Nothing was written.
type Conflict struct {
	Path       string `json:"path"`
	BaseRev    string `json:"base_rev"`
	CurrentRev string `json:"current_rev"`
	// BaseKnown is false when the base revision could not be found, so every
	// difference between current and yours is a conflict.
	BaseKnown bool            `json:"base_known"`
	Fields    []FieldConflict `json:"fields,omitempty"`
	Body      []BodyConflict  `json:"body,omitempty"`
}

// FieldConflict is one frontmatter key changed differently by both sides.
// Each value is its YAML text; nil means the key is absent on that side.
type FieldConflict struct {
	// ID names the conflict in a resolution: "field:<key>".
	ID      string  `json:"id"`
	Key     string  `json:"key"`
	Base    *string `json:"base"`
	Current *string `json:"current"`
	Yours   *string `json:"yours"`
}

// BodyConflict is one region of the body changed differently by both sides.
// Line is where the region starts in the current body, counting from 1.
type BodyConflict struct {
	// ID names the conflict in a resolution: "body:<n>", counting from 0.
	ID      string `json:"id"`
	Line    int    `json:"line"`
	Base    string `json:"base"`
	Current string `json:"current"`
	Yours   string `json:"yours"`
}

// ConflictError carries a Conflict as an error.
type ConflictError struct{ Conflict Conflict }

func (e *ConflictError) Error() string {
	n := len(e.Conflict.Fields) + len(e.Conflict.Body)
	return fmt.Sprintf("%s changed since revision %s and %d region(s) conflict with your edit; nothing was written", e.Conflict.Path, short(e.Conflict.BaseRev), n)
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// Resolution sides.
const (
	TakeCurrent = "current"
	TakeYours   = "yours"
)

// merge3 merges yours into current, both edits of base. It returns the
// merged source, or the conflicts when the edits overlap. Frontmatter is
// merged key by key and the body line by line. When the frontmatter of any
// side cannot be read as a mapping, the whole source is merged as text.
// resolve settles conflicts by id with TakeCurrent or TakeYours; a conflict
// it does not settle is returned.
func merge3(base, current, yours []byte, resolve map[string]string) ([]byte, *Conflict, error) {
	b, bErr := splitSource(base)
	c, cErr := splitSource(current)
	y, yErr := splitSource(yours)
	if bErr != nil || cErr != nil || yErr != nil {
		merged, conflicts := mergeLines(string(base), string(current), string(yours), resolve)
		if len(conflicts) > 0 {
			return nil, &Conflict{Body: conflicts}, nil
		}
		return []byte(merged), nil, nil
	}
	conflict := &Conflict{}
	fields, fieldConflicts := mergeFields(b, c, y, resolve)
	conflict.Fields = fieldConflicts
	body, bodyConflicts := mergeLines(b.body, c.body, y.body, resolve)
	conflict.Body = bodyConflicts
	if len(conflict.Fields)+len(conflict.Body) > 0 {
		return nil, conflict, nil
	}
	out, err := assemble(current, c, fields, body)
	return out, nil, err
}

// conflict2 reports how yours differs from current when no base is known.
func conflict2(current, yours []byte) *Conflict {
	c, cErr := splitSource(current)
	y, yErr := splitSource(yours)
	if cErr != nil || yErr != nil {
		_, conflicts := mergeLines("", string(current), string(yours), nil)
		return &Conflict{Body: conflicts}
	}
	empty := source{fields: map[string]string{}, nodes: map[string]*yaml.Node{}}
	_, fields := mergeFields(empty, c, y, nil)
	_, body := mergeLines("", c.body, y.body, nil)
	return &Conflict{Fields: fields, Body: body}
}

// source is a page source split for merging.
type source struct {
	hasFront bool
	order    []string
	fields   map[string]string // key -> encoded value
	nodes    map[string]*yaml.Node
	body     string
}

func splitSource(raw []byte) (source, error) {
	s := source{fields: map[string]string{}, nodes: map[string]*yaml.Node{}}
	front, body, hasFront := splitFrontmatter(raw)
	if !hasFront {
		if opensFrontmatter(raw) {
			return s, fmt.Errorf("unterminated frontmatter")
		}
		s.body = string(bytes.TrimPrefix(raw, bom))
		return s, nil
	}
	s.hasFront, s.body = true, string(body)
	if len(bytes.TrimSpace(front)) == 0 {
		return s, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return s, err
	}
	m := mappingOf(&doc)
	if m == nil {
		return s, fmt.Errorf("frontmatter is not a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i].Value
		if _, dup := s.fields[key]; dup {
			return s, fmt.Errorf("duplicate key %q", key)
		}
		text, err := encodeNode(m.Content[i+1])
		if err != nil {
			return s, err
		}
		s.order = append(s.order, key)
		s.fields[key] = text
		s.nodes[key] = m.Content[i+1]
	}
	return s, nil
}

func encodeNode(n *yaml.Node) (string, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// fieldChange is the merged state of one key: its node, or nil when the key
// is deleted.
type fieldChange struct {
	key  string
	node *yaml.Node
}

// mergeFields merges frontmatter key by key. A key changed by one side takes
// that side's value; changed identically by both, it takes it once; changed
// differently, it conflicts. It returns the changes to apply to current.
func mergeFields(b, c, y source, resolve map[string]string) ([]fieldChange, []FieldConflict) {
	var changes []fieldChange
	var conflicts []FieldConflict
	keys := append([]string(nil), c.order...)
	seen := map[string]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	for _, list := range [][]string{y.order, b.order} {
		for _, k := range list {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	get := func(s source, k string) *string {
		if v, ok := s.fields[k]; ok {
			return &v
		}
		return nil
	}
	same := func(a, b *string) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	for _, k := range keys {
		bv, cv, yv := get(b, k), get(c, k), get(y, k)
		switch {
		case same(yv, bv), same(cv, yv):
			// Yours left it alone, or both agree: current stands.
		case same(cv, bv), resolve["field:"+k] == TakeYours:
			changes = append(changes, fieldChange{key: k, node: y.nodes[k]})
		case resolve["field:"+k] == TakeCurrent:
		default:
			conflicts = append(conflicts, FieldConflict{ID: "field:" + k, Key: k, Base: bv, Current: cv, Yours: yv})
		}
	}
	return changes, conflicts
}

// assemble builds the merged source: current's frontmatter bytes untouched
// unless a key changed, and the merged body.
func assemble(current []byte, c source, changes []fieldChange, body string) ([]byte, error) {
	_, oldBody, hasFront := splitFrontmatter(current)
	if !hasFront {
		oldBody = bytes.TrimPrefix(current, bom)
	}
	head := current[:len(current)-len(oldBody)]
	out := append(append([]byte(nil), head...), body...)
	if hasFront && !bytes.HasSuffix(head, []byte("\n")) && body != "" {
		// A closing fence at end of file has no line break.
		out = append(append(append([]byte(nil), head...), '\n'), body...)
	}
	if len(changes) == 0 {
		return out, nil
	}
	remaining := len(c.order)
	for _, ch := range changes {
		_, existed := c.fields[ch.key]
		if existed && ch.node == nil {
			remaining--
		} else if !existed && ch.node != nil {
			remaining++
		}
	}
	if remaining == 0 {
		// Every key is gone: drop the frontmatter block rather than leave "{}".
		lead := []byte(nil)
		if bytes.HasPrefix(current, bom) {
			lead = bom
		}
		return append(lead, body...), nil
	}
	return rewriteFrontmatter(out, func(m *yaml.Node) error {
		for _, ch := range changes {
			found := false
			for i := 0; i+1 < len(m.Content); i += 2 {
				if m.Content[i].Value != ch.key {
					continue
				}
				found = true
				if ch.node == nil {
					m.Content = append(m.Content[:i], m.Content[i+2:]...)
				} else {
					m.Content[i+1] = ch.node
				}
				break
			}
			if !found && ch.node != nil {
				m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: ch.key}, ch.node)
			}
		}
		return nil
	})
}

// mergeLines is a three-way line merge in the manner of diff3: lines that
// both sides keep anchor the merge, a region changed by one side takes that
// side, a region changed identically by both takes it once, and a region
// changed differently by both is a conflict.
//
// Conflicting regions are numbered from 0 in order; resolve settles region n
// by its id "body:<n>".
func mergeLines(base, current, yours string, resolve map[string]string) (string, []BodyConflict) {
	bl, cl, yl := lines(base), lines(current), lines(yours)
	mc := matchLines(bl, cl)
	my := matchLines(bl, yl)
	var out strings.Builder
	var conflicts []BodyConflict
	region := 0
	i, c, y := 0, 0, 0
	for {
		for i < len(bl) && mc[i] == c && my[i] == y {
			out.WriteString(bl[i])
			i, c, y = i+1, c+1, y+1
		}
		if i == len(bl) && c == len(cl) && y == len(yl) {
			break
		}
		j := i
		for j < len(bl) && (mc[j] < 0 || my[j] < 0) {
			j++
		}
		ce, ye := len(cl), len(yl)
		if j < len(bl) {
			ce, ye = mc[j], my[j]
		}
		bChunk, cChunk, yChunk := join(bl[i:j]), join(cl[c:ce]), join(yl[y:ye])
		switch {
		case yChunk == bChunk || yChunk == cChunk:
			out.WriteString(cChunk)
		case cChunk == bChunk:
			out.WriteString(yChunk)
		default:
			id := fmt.Sprintf("body:%d", region)
			region++
			switch resolve[id] {
			case TakeYours:
				out.WriteString(yChunk)
			case TakeCurrent:
				out.WriteString(cChunk)
			default:
				conflicts = append(conflicts, BodyConflict{ID: id, Line: c + 1, Base: bChunk, Current: cChunk, Yours: yChunk})
				out.WriteString(cChunk)
			}
		}
		i, c, y = j, ce, ye
	}
	return out.String(), conflicts
}

// lines splits text into lines that keep their line endings.
func lines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

func join(ls []string) string { return strings.Join(ls, "") }

// maxMatchCells bounds the work of matching two texts. Beyond it the changed
// middle of the texts is treated as one region, which can only turn a merge
// into a conflict, never into a wrong merge.
const maxMatchCells = 4 << 20

// matchLines returns, for each line of a, the index of the line of b it is
// matched with in a longest common subsequence, or -1. Matched indices
// increase strictly.
func matchLines(a, b []string) []int {
	m := make([]int, len(a))
	for i := range m {
		m[i] = -1
	}
	// Common prefix and suffix match without search.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		m[pre] = pre
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		m[len(a)-1-suf] = len(b) - 1 - suf
		suf++
	}
	a2, b2 := a[pre:len(a)-suf], b[pre:len(b)-suf]
	n, k := len(a2), len(b2)
	if n == 0 || k == 0 || n*k > maxMatchCells {
		return m
	}
	// lcs[i][j] is the LCS length of a2[i:] and b2[j:].
	width := k + 1
	lcs := make([]int32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := k - 1; j >= 0; j-- {
			switch {
			case a2[i] == b2[j]:
				lcs[i*width+j] = lcs[(i+1)*width+j+1] + 1
			case lcs[(i+1)*width+j] >= lcs[i*width+j+1]:
				lcs[i*width+j] = lcs[(i+1)*width+j]
			default:
				lcs[i*width+j] = lcs[i*width+j+1]
			}
		}
	}
	for i, j := 0, 0; i < n && j < k; {
		switch {
		case a2[i] == b2[j]:
			m[pre+i] = pre + j
			i, j = i+1, j+1
		case lcs[(i+1)*width+j] >= lcs[i*width+j+1]:
			i++
		default:
			j++
		}
	}
	return m
}
