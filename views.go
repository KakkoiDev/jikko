package jikko

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProblemView reports a View whose definition cannot be evaluated.
const ProblemView = "view"

// ViewSpec is a View's parsed definition (specification §3.3, §14):
//
//	filter:
//	  type: task
//	  tags: [architecture]
//	  status: [todo, doing]
//	view:
//	  layout: board
//	  group: status
//	  sort: due
type ViewSpec struct {
	Filter []ViewCondition `json:"filter,omitempty"`
	Layout string          `json:"layout,omitempty"`
	Group  string          `json:"group,omitempty"`
	Sort   []string        `json:"sort,omitempty"`
}

// ViewCondition selects pages whose property holds any of Values.
type ViewCondition struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// ViewItem is one page selected by a View.
type ViewItem struct {
	Path     string         `json:"path"`
	Title    string         `json:"title"`
	Type     Kind           `json:"type"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ViewGroup is the pages of one group, in sort order. Key is the rendered
// property value; it is empty for pages without the property.
type ViewGroup struct {
	Key   string     `json:"key"`
	Pages []ViewItem `json:"pages"`
}

// ViewResult is a View evaluated for one actor. Groups is set only when the
// View groups its pages.
type ViewResult struct {
	View   string      `json:"view"`
	Title  string      `json:"title"`
	Spec   ViewSpec    `json:"spec"`
	Pages  []ViewItem  `json:"pages"`
	Groups []ViewGroup `json:"groups,omitempty"`
}

// parseViewSpec reads a View's definition from its metadata and reports what
// makes it unusable. Unknown keys under `view` are presentation hints for
// other harnesses and are kept, not reported.
func parseViewSpec(meta map[string]any) (ViewSpec, []string) {
	var spec ViewSpec
	var problems []string
	switch filter := meta["filter"].(type) {
	case nil:
	case map[string]any:
		for _, key := range sortedKeys(filter) {
			values, ok := scalarList(filter[key])
			if !ok {
				problems = append(problems, fmt.Sprintf("filter %q must be a value or a list of values", key))
				continue
			}
			if len(values) == 0 {
				problems = append(problems, fmt.Sprintf("filter %q names no value", key))
				continue
			}
			if key == "type" {
				for _, v := range values {
					if _, known := ParseKind(v); !known || v == "" {
						problems = append(problems, fmt.Sprintf("filter type %q is not document, task, view, or identity", v))
					}
				}
			}
			spec.Filter = append(spec.Filter, ViewCondition{Key: key, Values: values})
		}
	default:
		problems = append(problems, "filter must be a mapping of property to value")
	}
	switch hints := meta["view"].(type) {
	case nil:
	case map[string]any:
		for _, key := range []string{"layout", "group"} {
			switch v := hints[key].(type) {
			case nil:
			case string:
				if key == "layout" {
					spec.Layout = v
				} else {
					spec.Group = v
				}
			default:
				problems = append(problems, fmt.Sprintf("view %s must be a property name", key))
			}
		}
		sortKeys, ok := stringList(hints["sort"])
		if !ok {
			problems = append(problems, "view sort must be a property name or a list of them")
		}
		for _, k := range sortKeys {
			if strings.TrimPrefix(k, "-") == "" {
				problems = append(problems, fmt.Sprintf("view sort %q names no property", k))
				continue
			}
			spec.Sort = append(spec.Sort, k)
		}
	default:
		problems = append(problems, "view must be a mapping of presentation hints")
	}
	return spec, problems
}

// scalarList reads a filter value: one scalar or a list of scalars, each
// rendered as text.
func scalarList(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := scalarText(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		s, ok := scalarText(v)
		if !ok {
			return nil, false
		}
		return []string{s}, true
	}
}

// scalarText renders a YAML scalar the way it would be typed: a date as
// 2026-09-20, a number without trailing zeros.
func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case time.Time:
		if x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0 {
			return x.Format("2006-01-02"), true
		}
		return x.Format(time.RFC3339), true
	default:
		return "", false
	}
}

// propertyValues returns the values a page holds for a filter, group, or
// sort key. type, title, and path are the page's own; every other key is
// frontmatter. A list holds several values.
func propertyValues(p *Page, key string) []any {
	switch key {
	case "type":
		return []any{string(p.Kind)}
	case "title":
		return []any{p.Title}
	case "path":
		return []any{p.Path}
	}
	switch v := p.Metadata[key].(type) {
	case nil:
		return nil
	case []any:
		return v
	default:
		return []any{v}
	}
}

// EvaluateView evaluates a View for an actor: the pages the actor may read
// that match every filter condition, sorted and grouped as the View says. The
// View itself must be readable.
func (w *Workspace) EvaluateView(actor, ref string) (*ViewResult, error) {
	view, err := w.readable(actor, ref)
	if err != nil {
		return nil, err
	}
	if view.Kind != View {
		return nil, fmt.Errorf("%s is a %s, not a view", view.Path, view.Kind)
	}
	spec, problems := parseViewSpec(view.Metadata)
	if len(problems) > 0 {
		return nil, errors.New(view.Path + ": " + strings.Join(problems, "; "))
	}
	var pages []*Page
	for _, p := range w.sorted() {
		if p == view || !w.Allowed(actor, p, Read) || !w.matchesView(p, spec) {
			continue
		}
		pages = append(pages, p)
	}
	sort.SliceStable(pages, func(i, j int) bool { return lessBy(pages[i], pages[j], spec.Sort) })
	res := &ViewResult{View: view.Path, Title: view.Title, Spec: spec, Pages: make([]ViewItem, 0, len(pages))}
	for _, p := range pages {
		res.Pages = append(res.Pages, viewItem(p))
	}
	if spec.Group != "" {
		res.Groups = groupPages(pages, spec)
	}
	return res, nil
}

func viewItem(p *Page) ViewItem {
	return ViewItem{Path: p.Path, Title: p.Title, Type: p.Kind, Metadata: p.Metadata}
}

// matchesView reports whether a page satisfies every condition. A condition
// holds when any value of the page's property equals any listed value, as
// text, or when both name the same page -- so `assignee: alice` selects a
// Task assigned to `people/alice`.
func (w *Workspace) matchesView(p *Page, spec ViewSpec) bool {
	for _, c := range spec.Filter {
		if c.Key == "type" {
			ok := false
			for _, v := range c.Values {
				if kind, _ := ParseKind(v); kind == p.Kind {
					ok = true
				}
			}
			if !ok {
				return false
			}
			continue
		}
		ok := false
		for _, have := range propertyValues(p, c.Key) {
			text, isScalar := scalarText(have)
			if !isScalar {
				continue
			}
			for _, want := range c.Values {
				if text == want || w.sameTarget(text, want) {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (w *Workspace) sameTarget(a, b string) bool {
	pa, ok := w.Resolve(a)
	if !ok {
		return false
	}
	pb, ok := w.Resolve(b)
	return ok && pa == pb
}

// lessBy orders pages by the sort keys, then by path. A "-" prefix sorts
// descending. Pages without a value sort after those with one, either way.
func lessBy(a, b *Page, keys []string) bool {
	for _, k := range keys {
		desc := strings.HasPrefix(k, "-")
		k = strings.TrimPrefix(k, "-")
		va, vb := firstValue(a, k), firstValue(b, k)
		switch {
		case va == nil && vb == nil:
			continue
		case va == nil:
			return false
		case vb == nil:
			return true
		}
		c := compareValues(va, vb)
		if c == 0 {
			continue
		}
		if desc {
			return c > 0
		}
		return c < 0
	}
	return a.Path < b.Path
}

func firstValue(p *Page, key string) any {
	values := propertyValues(p, key)
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

// compareValues orders numbers numerically, dates chronologically, and
// everything else, mixed types included, by its text.
func compareValues(a, b any) int {
	if fa, ok := number(a); ok {
		if fb, ok := number(b); ok {
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		}
	}
	if ta, ok := a.(time.Time); ok {
		if tb, ok := b.(time.Time); ok {
			return ta.Compare(tb)
		}
	}
	sa, _ := scalarText(a)
	sb, _ := scalarText(b)
	return strings.Compare(sa, sb)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, !math.IsNaN(x)
	}
	return 0, false
}

// groupPages groups sorted pages by a property. A page with a list value
// appears in every group it names. Groups the filter lists for the same
// property come first, in the filter's order and even when empty, so a board
// shows its columns; other values follow in order, and pages without the
// property come last.
func groupPages(pages []*Page, spec ViewSpec) []ViewGroup {
	index := map[string]int{}
	var groups []ViewGroup
	add := func(key string) int {
		if i, ok := index[key]; ok {
			return i
		}
		index[key] = len(groups)
		groups = append(groups, ViewGroup{Key: key, Pages: []ViewItem{}})
		return index[key]
	}
	for _, c := range spec.Filter {
		if c.Key == spec.Group {
			for _, v := range c.Values {
				add(v)
			}
		}
	}
	var rest []any
	seen := map[string]bool{}
	members := map[string][]ViewItem{}
	for _, p := range pages {
		values := propertyValues(p, spec.Group)
		if len(values) == 0 {
			values = []any{nil}
		}
		for _, v := range values {
			key, ok := scalarText(v)
			if !ok {
				key = fmt.Sprint(v)
			}
			if _, listed := index[key]; !listed && !seen[key] && key != "" {
				seen[key] = true
				rest = append(rest, v)
			}
			members[key] = append(members[key], viewItem(p))
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return compareValues(rest[i], rest[j]) < 0 })
	for _, v := range rest {
		key, ok := scalarText(v)
		if !ok {
			key = fmt.Sprint(v)
		}
		add(key)
	}
	if len(members[""]) > 0 {
		add("")
	}
	for key, items := range members {
		if i, ok := index[key]; ok {
			groups[i].Pages = items
		}
	}
	return groups
}
