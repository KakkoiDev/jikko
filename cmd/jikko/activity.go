package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	jikko "github.com/KakkoiDev/jikko"
)

// presenceView is one row of "who is here": an agent process recorded under
// .data/presence, or a person with a live browser session.
type presenceView struct {
	Ref, Name, URL string
	Initials       string
	Kind           string // agent, person, or group
	Online         bool
	State          string // working, idle, browsing, offline
	Task, TaskURL  string
	TaskTitle      string
	Since          string // how long in the current state: "23m"
	Seen           string // last heartbeat: "3d ago"
	Workdir        string
	LastTask       string
	LastTaskURL    string
	LastTaskTitle  string
	LastOutcome    string
	LastFinished   string
	// You marks the viewer's own row.
	You bool
}

// needView is one item of "Needs you" on the home page.
type needView struct {
	Why, Title, URL, Status   string
	Who, WhoKind, WhoInitials string
	Text                      string
	Action                    string
	State                     string // for the idle-agent row's dot
}

// cardView is one page in the home list, with the few facts shown beside it.
type cardView struct {
	*jikko.Page
	Status, Assignee, AssigneeState, Due, Extra string
}

// activityView is what an identity page shows about the identity's work.
type activityView struct {
	Ref      string
	Now      *presenceView
	Latest   *commentRef
	Queue    []*jikko.Page
	Comments []commentRef
	Commits  []commitView
}

type commentRef struct {
	Path, Title, URL, ID, Text string
}

type commitView struct {
	Short, Subject, Operation, Ago string
	Who, WhoKind                   string
}

var statusValues = []string{"todo", "doing", "blocked", "done"}

// initials renders a name as the one or two characters an avatar shows.
func initials(name string) string {
	name = strings.TrimSpace(path.Base(name))
	if name == "" {
		return "?"
	}
	r := []rune(strings.ToLower(name))
	if len(r) == 1 {
		return strings.ToUpper(string(r[0]))
	}
	return string(r[:2])
}

// isAgent reports whether an identity runs as an agent: it has a presence
// record, or belongs to a group called agents. Jikko has no agent primitive;
// the guide says such classifications are ordinary groups.
func isAgent(ws *jikko.Workspace, ref string) bool {
	if _, ok := ws.PresenceOf(ref); ok {
		return true
	}
	if g, ok := ws.ResolveIdentity("agents"); ok && len(g.Members) != 0 {
		return ws.MemberOf(ref, strings.TrimSuffix(g.Path, ".md"))
	}
	return false
}

// whoKind classifies an identity reference for avatars and bylines.
func whoKind(ws *jikko.Workspace, ref string) string {
	p, ok := ws.ResolveIdentity(ref)
	if !ok {
		return "human"
	}
	if len(p.Members) != 0 {
		return "group"
	}
	if isAgent(ws, strings.TrimSuffix(p.Path, ".md")) {
		return "agent"
	}
	return "human"
}

// online lists agents and people the actor may know about: agents from the
// presence records, people from live browser sessions. Identities the actor
// cannot read are left out, so presence never leaks a hidden page.
func (s *server) online(ws *jikko.Workspace, actor string) []presenceView {
	var out []presenceView
	seen := map[string]bool{}
	for _, p := range ws.Presences() {
		page, ok := ws.ResolveIdentity(p.Identity)
		if !ok || !ws.Allowed(actor, page, jikko.Read) {
			continue
		}
		row := presenceRow(ws, actor, page, &p)
		row.You = p.Identity == actor
		out = append(out, row)
		seen[p.Identity] = true
	}
	for ref, last := range s.sessions.active() {
		if seen[ref] {
			continue
		}
		page, ok := ws.ResolveIdentity(ref)
		if !ok || !ws.Allowed(actor, page, jikko.Read) {
			continue
		}
		out = append(out, presenceView{
			Ref: ref, Name: page.Title, URL: jikko.DefaultLinks.Page(page.Path), Initials: initials(page.Title), Kind: "person",
			Online: true, State: "browsing", Seen: ago(last), You: ref == actor,
		})
	}
	rank := func(v presenceView) int {
		switch {
		case v.Online && v.State == "working":
			return 0
		case v.Online && v.State == "idle":
			return 1
		case v.Online:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// hereSummary is the rail's one-line count: "2 working · 1 idle · you".
func hereSummary(rows []presenceView, actor string) string {
	working, idle, people := 0, 0, 0
	you := false
	for _, r := range rows {
		switch {
		case r.You:
			you = true
		case r.Online && r.State == "working":
			working++
		case r.Online && r.State == "idle":
			idle++
		case r.Online:
			people++
		}
	}
	var parts []string
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if idle > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", idle))
	}
	if people > 0 {
		parts = append(parts, fmt.Sprintf("%d here", people))
	}
	if len(parts) == 0 {
		if you || actor != "" {
			return "only you"
		}
		return "nobody"
	}
	if you {
		parts = append(parts, "you")
	}
	return strings.Join(parts, " · ")
}

func presenceRow(ws *jikko.Workspace, actor string, page *jikko.Page, p *jikko.Presence) presenceView {
	v := presenceView{Ref: p.Identity, Name: page.Title, URL: jikko.DefaultLinks.Page(page.Path), Initials: initials(page.Title), Kind: "agent", Online: p.Online, State: p.State, Workdir: p.Workdir}
	if !p.Online {
		v.State = "offline"
	}
	v.Since = dur(p.Since)
	v.Seen = ago(p.Seen)
	if p.Task != "" {
		v.Task, v.TaskURL, v.TaskTitle = taskLink(ws, actor, p.Task)
	}
	if p.LastTask != "" {
		v.LastTask, v.LastTaskURL, v.LastTaskTitle = taskLink(ws, actor, p.LastTask)
		v.LastOutcome = p.LastOutcome
		v.LastFinished = ago(p.LastFinished)
	}
	return v
}

func taskLink(ws *jikko.Workspace, actor, ref string) (string, string, string) {
	if t, ok := ws.Resolve(ref); ok && ws.Allowed(actor, t, jikko.Read) {
		return ref, jikko.DefaultLinks.Page(t.Path), t.Title
	}
	return ref, "", ref
}

// metaText flattens a front-matter value to display text.
func metaText(p *jikko.Page, key string) string {
	switch v := p.Metadata[key].(type) {
	case nil:
		return ""
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, ", ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// cards decorates the page list with status, assignee presence, and the like.
func (s *server) cards(ws *jikko.Workspace, pages []*jikko.Page) []cardView {
	active := s.sessions.active()
	out := make([]cardView, 0, len(pages))
	for _, p := range pages {
		c := cardView{Page: p}
		switch p.Kind {
		case jikko.Task:
			c.Status = strings.ToLower(metaText(p, "status"))
			c.Due = metaText(p, "due")
			if as := assignees(p); len(as) > 0 {
				c.Assignee = strings.Join(as, ", ")
				if who, ok := ws.ResolveIdentity(as[0]); ok {
					ref := strings.TrimSuffix(who.Path, ".md")
					c.Assignee = who.Title
					if pr, found := ws.PresenceOf(ref); found && pr.Online {
						c.AssigneeState = pr.State
					} else if _, live := active[ref]; live {
						c.AssigneeState = "browsing"
					}
				}
			} else {
				c.Assignee = "unassigned"
			}
			if n := len(p.Comments); n == 1 {
				c.Extra = "1 open thread"
			} else if n > 1 {
				c.Extra = fmt.Sprintf("%d open threads", n)
			}
		case jikko.Identity:
			if len(p.Members) != 0 {
				c.Extra = fmt.Sprintf("group of %d", len(p.Members))
			} else if isAgent(ws, strings.TrimSuffix(p.Path, ".md")) {
				c.Extra = "agent"
			} else {
				c.Extra = "person"
			}
		case jikko.View:
			c.Extra = "view"
		default:
			if n := len(p.Comments); n == 1 {
				c.Extra = "1 open thread"
			} else if n > 1 {
				c.Extra = fmt.Sprintf("%d open threads", n)
			} else if n := len(p.Links); n == 1 {
				c.Extra = "links to 1 page"
			} else if n > 1 {
				c.Extra = fmt.Sprintf("links to %d pages", n)
			}
		}
		out = append(out, c)
	}
	return out
}

// lastEntry returns the newest comment entry on a page, if any.
func lastEntry(p *jikko.Page) (jikko.CommentThread, jikko.CommentEntry, bool) {
	if len(p.Comments) == 0 {
		return jikko.CommentThread{}, jikko.CommentEntry{}, false
	}
	th := p.Comments[len(p.Comments)-1]
	if len(th.Entries) == 0 {
		return th, jikko.CommentEntry{}, false
	}
	return th, th.Entries[len(th.Entries)-1], true
}

var mentionWord = regexp.MustCompile(`@([A-Za-z0-9_./-]+)`)

// mentionsActor reports whether text addresses actor by name or path.
func mentionsActor(text, actor string) bool {
	if actor == "" {
		return false
	}
	base := path.Base(actor)
	for _, m := range mentionWord.FindAllStringSubmatch(text, -1) {
		name := strings.TrimRight(m[1], "./")
		if name == base || name == actor {
			return true
		}
	}
	return false
}

// needs lists what waits on the viewer: blocked tasks, threads that mention
// them, finished work with open threads, and idle agents with empty queues.
func (s *server) needs(ws *jikko.Workspace, actor string) []needView {
	var blocked, mentions, review, idle []needView
	listed := map[string]bool{}
	who := func(author string) (string, string, string) {
		name := strings.TrimPrefix(author, "@")
		if name == "" {
			return "", "human", "?"
		}
		if p, ok := ws.ResolveIdentity(name); ok {
			return p.Title, whoKind(ws, strings.TrimSuffix(p.Path, ".md")), initials(p.Title)
		}
		return path.Base(name), "human", initials(name)
	}
	for _, p := range ws.List(jikko.Task, "") {
		if !ws.Allowed(actor, p, jikko.Read) {
			continue
		}
		status := strings.ToLower(metaText(p, "status"))
		th, e, hasEntry := lastEntry(p)
		url := jikko.DefaultLinks.Page(p.Path)
		if hasEntry {
			url += "#comment-" + th.ID
		}
		switch status {
		case "blocked":
			n := needView{Why: "Question · blocked", Title: p.Title, URL: url, Status: status, Action: "Answer"}
			if hasEntry {
				n.Who, n.WhoKind, n.WhoInitials = who(e.Author)
				n.Text = e.Text
			} else {
				n.Text = "Blocked, with no thread explaining why."
			}
			blocked = append(blocked, n)
			listed[p.Path] = true
		case "done":
			if len(p.Comments) == 0 {
				continue
			}
			n := needView{Why: fmt.Sprintf("Result · done · %d open", len(p.Comments)), Title: p.Title, URL: url, Status: status, Action: "Review"}
			if hasEntry {
				n.Who, n.WhoKind, n.WhoInitials = who(e.Author)
				n.Text = e.Text
			}
			review = append(review, n)
			listed[p.Path] = true
		}
	}
	if actor != "" {
		for _, p := range ws.List("", "") {
			if listed[p.Path] || !ws.Allowed(actor, p, jikko.Read) {
				continue
			}
			for _, th := range p.Comments {
				if len(th.Entries) == 0 {
					continue
				}
				last := th.Entries[len(th.Entries)-1]
				if a := strings.TrimPrefix(last.Author, "@"); a == actor || a == path.Base(actor) {
					continue // you spoke last; the ball is not in your court
				}
				hit := false
				for _, e := range th.Entries {
					hit = hit || mentionsActor(e.Text, actor)
				}
				if !hit {
					continue
				}
				n := needView{Why: "Mentions you", Title: p.Title, URL: jikko.DefaultLinks.Page(p.Path) + "#comment-" + th.ID, Status: strings.ToLower(metaText(p, "status")), Action: "Reply", Text: last.Text}
				n.Who, n.WhoKind, n.WhoInitials = who(last.Author)
				mentions = append(mentions, n)
				listed[p.Path] = true
				break
			}
		}
	}
	for _, pr := range ws.Presences() {
		if !pr.Online || pr.State != "idle" {
			continue
		}
		page, ok := ws.ResolveIdentity(pr.Identity)
		if !ok || !ws.Allowed(actor, page, jikko.Read) || len(queuedTasks(ws, pr.Identity)) != 0 {
			continue
		}
		n := needView{Why: "Idle agent · " + dur(pr.Since), Title: page.Title, URL: jikko.DefaultLinks.Page(page.Path), Action: "Assign a task", State: "idle", Text: "has nothing in its queue."}
		if pr.LastTask != "" {
			_, _, title := taskLink(ws, actor, pr.LastTask)
			n.Text = fmt.Sprintf("has nothing in its queue. Last: %s · %s.", title, pr.LastOutcome)
		}
		idle = append(idle, n)
	}
	out := append(blocked, mentions...)
	out = append(out, review...)
	return append(out, idle...)
}

// activity gathers what an identity is doing and has done, from the pages the
// actor may read: presence, queued tasks, comments it wrote, commits it made.
func (s *server) activity(ws *jikko.Workspace, actor string, page *jikko.Page) *activityView {
	ref := strings.TrimSuffix(page.Path, ".md")
	a := &activityView{Ref: ref}
	if p, ok := ws.PresenceOf(ref); ok {
		row := presenceRow(ws, actor, page, &p)
		a.Now = &row
	}
	if len(page.Members) == 0 {
		a.Queue = queuedTasks(ws, ref)
	}
	names := map[string]bool{ref: true, path.Base(ref): true}
	for _, p := range ws.List("", "") {
		if !ws.Allowed(actor, p, jikko.Read) {
			continue
		}
		for _, th := range p.Comments {
			for _, e := range th.Entries {
				if names[strings.TrimPrefix(e.Author, "@")] {
					a.Comments = append(a.Comments, commentRef{Path: p.Path, Title: p.Title, URL: jikko.DefaultLinks.Page(p.Path) + "#comment-" + th.ID, ID: th.ID, Text: e.Text})
				}
			}
		}
	}
	if n := len(a.Comments); n > 0 {
		latest := a.Comments[n-1]
		a.Latest = &latest
		if n > 8 {
			a.Comments = a.Comments[n-8:]
		}
		for i, j := 0, len(a.Comments)-1; i < j; i, j = i+1, j-1 {
			a.Comments[i], a.Comments[j] = a.Comments[j], a.Comments[i] // newest first
		}
	}
	commits, _ := ws.Log(actor, "", 200)
	for _, c := range commits {
		if c.Actor != ref {
			continue
		}
		a.Commits = append(a.Commits, toCommitView(ws, c))
		if len(a.Commits) == 8 {
			break
		}
	}
	return a
}

func toCommitView(ws *jikko.Workspace, c jikko.HistoryEntry) commitView {
	short := c.Commit
	if len(short) > 7 {
		short = short[:7]
	}
	when, _ := time.Parse(time.RFC3339, c.Date)
	v := commitView{Short: short, Subject: c.Subject, Operation: c.Operation, Ago: ago(when)}
	switch {
	case c.Actor != "":
		if p, ok := ws.ResolveIdentity(c.Actor); ok {
			v.Who, v.WhoKind = p.Title, whoKind(ws, c.Actor)
		} else {
			v.Who, v.WhoKind = path.Base(c.Actor), "human"
		}
	default:
		v.Who, v.WhoKind = c.Author, "human"
	}
	return v
}

// ago renders a time as a coarse age: "just now", "4m ago", "3h ago", "2d ago".
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// dur renders how long since t, compactly: "23m", "1h 12m", "3d".
func dur(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) - h*60; m != 0 {
			return fmt.Sprintf("%dh %dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
