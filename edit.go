package jikko

import (
	"bytes"
	"errors"
	"strings"

	"gopkg.in/yaml.v3"
)

// Field is one top-level frontmatter property as a form shows it.
type Field struct {
	Key string `json:"key"`
	// Value is the property as text: a scalar as written, a list of scalars
	// joined with ", ", anything else as YAML.
	Value string `json:"value"`
	// Editable is false for a property that a single line of text cannot
	// express without loss, such as a mapping or a nested list. Those are
	// edited in the source.
	Editable bool `json:"editable"`
}

// SourceFields lists the frontmatter properties of a page source in order.
// A source whose frontmatter cannot be read has none.
func SourceFields(src []byte) []Field {
	front, _, ok := splitFrontmatter(src)
	if !ok || len(bytes.TrimSpace(front)) == 0 {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(front, &doc) != nil {
		return nil
	}
	m := mappingOf(&doc)
	if m == nil {
		return nil
	}
	var out []Field
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i].Value, m.Content[i+1]
		f := Field{Key: k}
		switch v.Kind {
		case yaml.ScalarNode:
			f.Value, f.Editable = v.Value, !strings.ContainsAny(v.Value, "\r\n")
		case yaml.SequenceNode:
			items := make([]string, 0, len(v.Content))
			f.Editable = true
			for _, item := range v.Content {
				if item.Kind != yaml.ScalarNode || strings.ContainsAny(item.Value, ",\r\n") {
					f.Editable = false
				}
				items = append(items, item.Value)
			}
			f.Value = strings.Join(items, ", ")
		}
		if !f.Editable {
			text, err := encodeNode(v)
			if err != nil {
				text = ""
			}
			f.Value = text
		}
		out = append(out, f)
	}
	return out
}

// SourceEdit is a structured edit of a page source.
type SourceEdit struct {
	// Set assigns properties as `jikko set` does: YAML infers the type, and a
	// list property takes a comma-separated value.
	Set map[string]string
	// Remove deletes properties.
	Remove []string
	// Body, when not nil, replaces the Markdown body.
	Body *string
}

// EditSource applies a structured edit to a page source and returns the new
// source. Properties not named keep their bytes; the body is replaced
// exactly. The access policy is a mapping and cannot be set this way.
func EditSource(src []byte, e SourceEdit) ([]byte, error) {
	for _, k := range append(sortedKeys(e.Set), e.Remove...) {
		if strings.TrimSpace(k) == "" {
			return nil, errors.New("property name required")
		}
		if k == "permissions" {
			return nil, errors.New("permissions is an access-control policy: edit it in the source or with `jikko perm`")
		}
	}
	out := src
	if e.Body != nil {
		next, err := replaceBody(out, *e.Body)
		if err != nil {
			return nil, err
		}
		out = next
	}
	if len(e.Set) == 0 && len(e.Remove) == 0 {
		return out, nil
	}
	remove := map[string]bool{}
	for _, k := range e.Remove {
		remove[k] = true
	}
	return rewriteFrontmatter(out, func(m *yaml.Node) error {
		for i := 0; i+1 < len(m.Content); {
			if remove[m.Content[i].Value] {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
				continue
			}
			i += 2
		}
		for _, k := range sortedKeys(e.Set) {
			if remove[k] {
				continue
			}
			if err := setMappingValue(m, k, e.Set[k]); err != nil {
				return err
			}
		}
		return nil
	})
}

// replaceBody swaps a source's body, keeping its frontmatter bytes.
func replaceBody(raw []byte, body string) ([]byte, error) {
	_, old, hasFront := splitFrontmatter(raw)
	if !hasFront {
		if opensFrontmatter(raw) {
			return nil, errors.New(`frontmatter opens with "---" but has no closing "---" line; fix the file before mutating it`)
		}
		if strings.HasPrefix(strings.TrimPrefix(body, "\ufeff"), "---") {
			return nil, errors.New(`body must not open with a "---" frontmatter fence; use set or perm to change metadata`)
		}
		lead := []byte(nil)
		if bytes.HasPrefix(raw, bom) {
			lead = bom
		}
		return append(lead, body...), nil
	}
	next := append([]byte(nil), raw[:len(raw)-len(old)]...)
	if !bytes.HasSuffix(next, []byte("\n")) && body != "" {
		if usesCRLF(next) {
			next = append(next, '\r')
		}
		next = append(next, '\n')
	}
	return append(next, body...), nil
}

// SourceBody returns the Markdown body of a page source: everything after
// the frontmatter, or the whole source when it has none.
func SourceBody(src []byte) string {
	_, body, ok := splitFrontmatter(src)
	if !ok {
		return string(bytes.TrimPrefix(src, bom))
	}
	return string(body)
}
