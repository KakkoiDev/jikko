package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ProblemComment reports comment markup that cannot be read as one anchor and
// one thread per id.
const ProblemComment = "comment"

// CommentThread is one unresolved inline comment and its messages. Comments live in the Markdown they
// discuss (specification §8):
//
//	The harness should <!--comment:c17-->automatically update references<!--/comment:c17-->
//	when a file is renamed.
//
//	<!--comment-thread:c17
//	@alice: Should normal links update too?
//
//	@codex: Yes, links and embeds.
//	-->
//
// Every comment present in current Markdown is unresolved: resolving removes
// it, and Git keeps the discussion.
type CommentThread struct {
	ID      string         `json:"id"`
	Path    string         `json:"path"`
	Anchor  string         `json:"anchor"`
	Entries []CommentEntry `json:"entries"`
}

// CommentEntry is one message of a thread. Author is the identity reference
// written before the colon; it is empty for a paragraph without one.
type CommentEntry struct {
	Author string `json:"author,omitempty"`
	Text   string `json:"text"`
}

var (
	commentIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	commentOpenPattern   = regexp.MustCompile(`<!--comment:([A-Za-z0-9_-]+)-->`)
	commentClosePattern  = regexp.MustCompile(`<!--/comment:([A-Za-z0-9_-]+)-->`)
	commentThreadPattern = regexp.MustCompile(`(?s)<!--comment-thread:([A-Za-z0-9_-]+)[ \t]*\r?\n(.*?)-->`)
	commentAuthorPattern = regexp.MustCompile(`^@([[:alnum:]_./-]+):[ \t]?`)
	numberedIDPattern    = regexp.MustCompile(`^c([0-9]+)$`)
)

// commentSpan locates one comment's markup in a body. Offsets are byte
// offsets; a missing part is -1.
type commentSpan struct {
	id                    string
	openStart, openEnd    int
	closeStart, closeEnd  int
	threadStart           int
	threadBody, threadEnd int // threadBody: start of the entries; threadEnd: end of "-->"
}

// proseMask returns body with fenced code blocks and inline code spans
// blanked out, byte for byte, so offsets found in it are offsets in body.
// Comment syntax inside code is documentation, not a comment.
func proseMask(body string) string {
	out := []byte(body)
	open := ""
	pos := 0
	for _, line := range strings.SplitAfter(body, "\n") {
		content := strings.TrimRight(line, "\r\n")
		blank := false
		if open != "" {
			blank = true
			if strings.HasPrefix(strings.TrimSpace(content), open) {
				open = ""
			}
		} else if m := fencePattern.FindStringSubmatch(content); m != nil {
			open, blank = m[1], true
		}
		if blank {
			for i := 0; i < len(content); i++ {
				out[pos+i] = ' '
			}
		} else {
			for _, r := range inlineCodePattern.FindAllStringIndex(content, -1) {
				for i := r[0]; i < r[1]; i++ {
					out[pos+i] = ' '
				}
			}
		}
		pos += len(line)
	}
	return string(out)
}

// scanComments finds comment markup in a body and reports what does not pair
// up. Problem messages carry no positions, so an unrelated edit never turns
// an existing defect into a "new" one.
func scanComments(body string) ([]commentSpan, []string) {
	masked := proseMask(body)
	byID := map[string]*commentSpan{}
	var order []string
	span := func(id string) *commentSpan {
		if s, ok := byID[id]; ok {
			return s
		}
		s := &commentSpan{id: id, openStart: -1, openEnd: -1, closeStart: -1, closeEnd: -1, threadStart: -1, threadBody: -1, threadEnd: -1}
		byID[id] = s
		order = append(order, id)
		return s
	}
	var problems []string
	dup := map[string]bool{}
	duplicate := func(id, what string) {
		if !dup[id+what] {
			dup[id+what] = true
			problems = append(problems, fmt.Sprintf("comment id %q is used by more than one %s", id, what))
		}
	}
	threads := commentThreadPattern.FindAllStringSubmatchIndex(masked, -1)
	inThread := func(at int) bool {
		for _, t := range threads {
			if at >= t[0] && at < t[1] {
				return true
			}
		}
		return false
	}
	for _, t := range threads {
		s := span(masked[t[2]:t[3]])
		if s.threadStart >= 0 {
			duplicate(s.id, "thread")
			continue
		}
		s.threadStart, s.threadBody, s.threadEnd = t[0], t[4], t[1]
	}
	for _, m := range commentOpenPattern.FindAllStringSubmatchIndex(masked, -1) {
		if inThread(m[0]) {
			continue
		}
		s := span(masked[m[2]:m[3]])
		if s.openStart >= 0 {
			duplicate(s.id, "anchor")
			continue
		}
		s.openStart, s.openEnd = m[0], m[1]
	}
	for _, m := range commentClosePattern.FindAllStringSubmatchIndex(masked, -1) {
		if inThread(m[0]) {
			continue
		}
		s := span(masked[m[2]:m[3]])
		if s.closeStart >= 0 {
			duplicate(s.id, "anchor end")
			continue
		}
		s.closeStart, s.closeEnd = m[0], m[1]
	}
	spans := make([]commentSpan, 0, len(order))
	for _, id := range order {
		s := byID[id]
		switch {
		case s.openStart < 0 && s.closeStart >= 0:
			problems = append(problems, fmt.Sprintf("comment %q ends an anchor that never opens", id))
		case s.openStart >= 0 && s.closeStart < 0:
			problems = append(problems, fmt.Sprintf("comment %q opens an anchor that never ends", id))
		case s.openStart >= 0 && s.closeStart < s.openEnd:
			problems = append(problems, fmt.Sprintf("comment %q ends its anchor before opening it", id))
		case s.openStart >= 0 && s.threadStart < 0:
			problems = append(problems, fmt.Sprintf("comment %q is anchored but has no thread", id))
		case s.openStart < 0 && s.threadStart >= 0:
			problems = append(problems, fmt.Sprintf("comment %q has a thread but no anchor; its discussion is orphaned", id))
		}
		spans = append(spans, *s)
	}
	sort.Strings(problems)
	return spans, problems
}

// parseComments reads the comments of a page body, with problems.
func parseComments(rel, body string) ([]CommentThread, []Problem) {
	spans, msgs := scanComments(body)
	var problems []Problem
	for _, m := range msgs {
		problems = append(problems, Problem{Path: rel, Kind: ProblemComment, Message: m})
	}
	var out []CommentThread
	for _, s := range spans {
		if s.threadStart < 0 && s.openStart < 0 {
			continue
		}
		c := CommentThread{ID: s.id, Path: rel, Entries: []CommentEntry{}}
		if s.openStart >= 0 && s.closeStart >= s.openEnd {
			c.Anchor = stripCommentMarkers(body[s.openEnd:s.closeStart])
		}
		if s.threadStart >= 0 {
			c.Entries = threadEntries(body[s.threadBody : s.threadEnd-len("-->")])
		}
		out = append(out, c)
	}
	return out, problems
}

// threadEntries splits a thread into its blank-line separated messages.
func threadEntries(text string) []CommentEntry {
	entries := []CommentEntry{}
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		joined := strings.Join(para, "\n")
		e := CommentEntry{Text: joined}
		if m := commentAuthorPattern.FindStringSubmatch(joined); m != nil {
			e.Author, e.Text = m[1], joined[len(m[0]):]
		}
		e.Text = strings.TrimSpace(e.Text)
		entries = append(entries, e)
		para = nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, line)
	}
	flush()
	return entries
}

// stripCommentMarkers removes inline anchor markers, leaving the text they
// surround, so a title or an excerpt reads as the author wrote it.
func stripCommentMarkers(s string) string {
	s = commentOpenPattern.ReplaceAllString(s, "")
	return commentClosePattern.ReplaceAllString(s, "")
}

// blankCommentAuthors hides the "@name:" that attributes each thread message,
// so writing a comment does not count as mentioning yourself. Mentions in the
// message text still count.
func blankCommentAuthors(body string) string {
	spans, _ := scanComments(body)
	out := []byte(body)
	for _, s := range spans {
		if s.threadStart < 0 {
			continue
		}
		pos := s.threadBody
		for _, line := range strings.SplitAfter(body[s.threadBody:s.threadEnd], "\n") {
			if m := commentAuthorPattern.FindStringIndex(line); m != nil {
				for i := m[0]; i < m[1]; i++ {
					out[pos+i] = ' '
				}
			}
			pos += len(line)
		}
	}
	return string(out)
}

// Comments returns the unresolved comments of every page the actor may read,
// in path order.
func (w *Workspace) Comments(actor string) []CommentThread {
	out := []CommentThread{}
	for _, p := range w.sorted() {
		if w.Allowed(actor, p, Read) {
			out = append(out, p.Comments...)
		}
	}
	return out
}

// AddComment anchors a new comment on the one occurrence of anchor in the
// page's prose and opens its thread with text. It requires the comment
// capability and returns the new comment's id.
//
// The anchor must be a single line of ordinary prose that occurs exactly
// once in the body, outside code, and must not split a [[reference]]. The
// thread is placed after the paragraph holding the anchor.
func (w *Workspace) AddComment(actor, ref, anchor, text string) (string, error) {
	if anchor == "" || strings.ContainsAny(anchor, "\r\n") {
		return "", errors.New("anchor must be a non-empty excerpt from a single line")
	}
	if err := checkCommentText(anchor); err != nil {
		return "", fmt.Errorf("anchor: %w", err)
	}
	text, err := cleanCommentText(text)
	if err != nil {
		return "", err
	}
	id := ""
	err = w.commentMutation(actor, ref, func(person, body string) (string, error) {
		spans, _ := scanComments(body)
		id = nextCommentID(spans)
		at, err := findAnchor(body, spans, anchor)
		if err != nil {
			return "", err
		}
		end := at + len(anchor)
		next := body[:at] + "<!--comment:" + id + "-->" + anchor + "<!--/comment:" + id + "-->" + body[end:]
		if a, b := refsKey(body), refsKey(next); a != b {
			return "", errors.New("the anchor would split a [[reference]]; choose an excerpt that contains it whole or not at all")
		}
		thread := "<!--comment-thread:" + id + "\n@" + person + ": " + text + "\n-->\n"
		return insertThread(next, end+len("<!--comment:"+id+"--><!--/comment:"+id+"-->"), thread), nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// ReplyComment appends a message to an existing comment thread. It requires
// the comment capability.
func (w *Workspace) ReplyComment(actor, ref, id, text string) error {
	text, err := cleanCommentText(text)
	if err != nil {
		return err
	}
	return w.commentMutation(actor, ref, func(person, body string) (string, error) {
		s, err := commentByID(body, id)
		if err != nil {
			return "", err
		}
		if s.threadStart < 0 {
			return "", fmt.Errorf("comment %q has no thread to reply to", id)
		}
		closing := s.threadEnd - len("-->")
		entry := "@" + person + ": " + text + "\n"
		// Separate the reply from the previous message by a blank line.
		if closing > s.threadBody {
			entry = "\n" + entry
			if !strings.HasSuffix(body[:closing], "\n") {
				entry = "\n" + entry
			}
		}
		return body[:closing] + entry + body[closing:], nil
	})
}

// ResolveComment removes a comment's anchor markers, keeping the text they
// surround, and its whole thread. Git history keeps the discussion. It
// requires the comment capability, which the permission semantics define as
// including resolve.
func (w *Workspace) ResolveComment(actor, ref, id string) error {
	return w.commentMutation(actor, ref, func(_, body string) (string, error) {
		s, err := commentByID(body, id)
		if err != nil {
			return "", err
		}
		// Remove from the end backwards so earlier offsets stay valid.
		type cut struct{ start, end int }
		var cuts []cut
		if s.threadStart >= 0 {
			cuts = append(cuts, cut{s.threadStart, s.threadEnd})
		}
		if s.closeStart >= 0 {
			cuts = append(cuts, cut{s.closeStart, s.closeEnd})
		}
		if s.openStart >= 0 {
			cuts = append(cuts, cut{s.openStart, s.openEnd})
		}
		sort.Slice(cuts, func(i, j int) bool { return cuts[i].start > cuts[j].start })
		for _, c := range cuts {
			if c.start == s.threadStart {
				body = removeBlock(body, c.start, c.end)
			} else {
				body = body[:c.start] + body[c.end:]
			}
		}
		return body, nil
	})
}

// commentMutation runs a body edit under the comment capability through the
// staged mutation path. edit receives the actor's identity reference and the
// body with line endings normalized to "\n"; CRLF files keep CRLF.
func (w *Workspace) commentMutation(actor, ref string, edit func(person, body string) (string, error)) error {
	defer w.lock()()
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return errors.New("authentication required as an individual identity")
	}
	actor = identityRef(person)
	p, ok := w.Resolve(ref)
	if !ok {
		return fmt.Errorf("reference %q not found or ambiguous", ref)
	}
	if p.Kind == View {
		return errors.New("views have no body to comment on")
	}
	if !w.Allowed(actor, p, Comment) {
		return fmt.Errorf("%s lacks comment permission on %s", actor, p.Path)
	}
	return w.mutateFile(actor, p, Comment, func(raw []byte) ([]byte, error) {
		_, old, hasFront := splitFrontmatter(raw)
		if !hasFront {
			if opensFrontmatter(raw) {
				return nil, errors.New(`frontmatter opens with "---" but has no closing "---" line; fix the file before mutating it`)
			}
			old = bytes.TrimPrefix(raw, bom)
		}
		head := raw[:len(raw)-len(old)]
		fenced := hasFront
		crlf := usesCRLF(old)
		body := string(old)
		if crlf {
			body = strings.ReplaceAll(body, "\r\n", "\n")
		}
		next, err := edit(actor, body)
		if err != nil {
			return nil, err
		}
		if crlf {
			next = strings.ReplaceAll(next, "\n", "\r\n")
		}
		out := append([]byte(nil), head...)
		if fenced && !bytes.HasSuffix(head, []byte("\n")) && next != "" && !strings.HasPrefix(next, "\n") && !strings.HasPrefix(next, "\r\n") {
			// A closing fence at end of file has no line break.
			if usesCRLF(head) {
				out = append(out, '\r')
			}
			out = append(out, '\n')
		}
		return append(out, next...), nil
	})
}

func commentByID(body, id string) (commentSpan, error) {
	spans, problems := scanComments(body)
	for _, p := range problems {
		if strings.Contains(p, fmt.Sprintf("%q", id)) {
			return commentSpan{}, fmt.Errorf("comment markup is malformed: %s", p)
		}
	}
	for _, s := range spans {
		if s.id == id {
			return s, nil
		}
	}
	return commentSpan{}, fmt.Errorf("no unresolved comment %q", id)
}

// nextCommentID returns c<n> one above the highest numbered id in the body.
// Ids are unique within a file; a resolved comment's id may be reused.
func nextCommentID(spans []commentSpan) string {
	highest := 0
	for _, s := range spans {
		if m := numberedIDPattern.FindStringSubmatch(s.id); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
				highest = n
			}
		}
	}
	return "c" + strconv.Itoa(highest+1)
}

// findAnchor returns the offset of the single prose occurrence of anchor.
func findAnchor(body string, spans []commentSpan, anchor string) (int, error) {
	masked := proseMask(body)
	found := -1
	for from := 0; ; {
		i := strings.Index(body[from:], anchor)
		if i < 0 {
			break
		}
		at := from + i
		from = at + 1
		if masked[at:at+len(anchor)] != anchor {
			continue // inside code
		}
		inside := false
		for _, s := range spans {
			if s.threadStart >= 0 && at < s.threadEnd && at+len(anchor) > s.threadStart {
				inside = true
			}
		}
		if inside {
			continue
		}
		if found >= 0 {
			return 0, fmt.Errorf("anchor %q occurs more than once; quote a longer excerpt", anchor)
		}
		found = at
	}
	if found < 0 {
		return 0, fmt.Errorf("anchor %q not found in the page's prose", anchor)
	}
	return found, nil
}

// insertThread places a thread after the paragraph that ends at or after
// offset at, or at the end of the body.
func insertThread(body string, at int, thread string) string {
	if i := strings.Index(body[at:], "\n\n"); i >= 0 {
		pos := at + i + 1
		return body[:pos] + "\n" + thread + body[pos:]
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + "\n" + thread
}

// removeBlock deletes a thread block and the blank line insertThread added
// with it, so add then resolve restores the original text.
func removeBlock(body string, start, end int) string {
	if strings.HasPrefix(body[end:], "\n") {
		end++
	}
	before, after := body[:start], body[end:]
	if strings.HasSuffix(before, "\n\n") && (after == "" || strings.HasPrefix(after, "\n")) {
		before = before[:len(before)-1]
	}
	return before + after
}

// refsKey summarizes a body's references. Markers that split a reference
// change it, so comparing keys catches an anchor that would break a link.
func refsKey(body string) string {
	links, embeds := refsIn(body)
	return strings.Join(links, "\x00") + "\x01" + strings.Join(embeds, "\x00")
}

// checkCommentText refuses text that would end or forge comment markup.
func checkCommentText(s string) error {
	for _, bad := range []string{"<!--", "-->", "--!>"} {
		if strings.Contains(s, bad) {
			return fmt.Errorf("must not contain %q", bad)
		}
	}
	return nil
}

// cleanCommentText validates one thread message. A message is one paragraph:
// a blank line would split it into two messages, and a code fence would hide
// the rest of the page from reference and mention scanning.
func cleanCommentText(text string) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return "", errors.New("comment text required")
	}
	if strings.Contains(text, "\r") {
		return "", errors.New("comment text must not contain a bare carriage return")
	}
	if err := checkCommentText(text); err != nil {
		return "", fmt.Errorf("comment text %w", err)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			return "", errors.New("comment text must be one paragraph; a blank line would split it into two messages")
		}
		if fencePattern.MatchString(line) {
			return "", errors.New("comment text must not contain a code fence")
		}
	}
	return text, nil
}

// UnresolvedCommentWarnings reports Tasks marked done while comments remain
// unresolved. The specification asks for a warning rather than a rejection,
// so these are not Problems.
func (w *Workspace) UnresolvedCommentWarnings() []Problem {
	var out []Problem
	for _, p := range w.sorted() {
		if p.Kind != Task || len(p.Comments) == 0 || !matchesStatus(p.Metadata["status"], "done") {
			continue
		}
		out = append(out, Problem{Path: p.Path, Kind: "review", Message: fmt.Sprintf("task is done but has %d unresolved comment(s)", len(p.Comments))})
	}
	return out
}
