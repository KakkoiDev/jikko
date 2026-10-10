package main

import (
	"fmt"
	"path"
	"strings"
	"time"

	jikko "github.com/KakkoiDev/jikko"
)

// presenceView is one row of "who is here": an agent process recorded under
// .data/presence, or a person with a live browser session.
type presenceView struct {
	Ref, Name, URL string
	Kind           string // agent or person
	Online         bool
	State          string // working, idle, browsing, offline
	Task, TaskURL  string
	TaskTitle      string
	Since          string
	Seen           string
	Workdir        string
	LastTask       string
	LastTaskURL    string
	LastTaskTitle  string
	LastOutcome    string
	LastFinished   string
}

// activityView is what an identity page shows about the identity's work.
type activityView struct {
	Now      *presenceView
	Queue    []*jikko.Page
	Comments []commentRef
	Commits  []commitView
}

type commentRef struct {
	Path, Title, URL, ID, Text string
}

type commitView struct {
	Short, Subject, Operation, Ago string
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
		out = append(out, presenceRow(ws, actor, page, &p))
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
			Ref: ref, Name: page.Title, URL: jikko.DefaultLinks.Page(page.Path), Kind: "person",
			Online: true, State: "browsing", Seen: ago(last),
		})
	}
	// Working first, then idle, then people, then offline; names break ties.
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
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (rank(out[j]) < rank(out[j-1]) || rank(out[j]) == rank(out[j-1]) && out[j].Name < out[j-1].Name); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func presenceRow(ws *jikko.Workspace, actor string, page *jikko.Page, p *jikko.Presence) presenceView {
	v := presenceView{Ref: p.Identity, Name: page.Title, URL: jikko.DefaultLinks.Page(page.Path), Kind: "agent", Online: p.Online, State: p.State, Workdir: p.Workdir}
	if !p.Online {
		v.State = "offline"
	}
	v.Since = ago(p.Since)
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

// activity gathers what an identity is doing and has done, from the pages the
// actor may read: presence, queued tasks, comments it wrote, commits it made.
func (s *server) activity(ws *jikko.Workspace, actor string, page *jikko.Page) *activityView {
	ref := strings.TrimSuffix(page.Path, ".md")
	a := &activityView{}
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
	if len(a.Comments) > 12 {
		a.Comments = a.Comments[len(a.Comments)-12:]
	}
	commits, _ := ws.History(200)
	for _, c := range commits {
		if c.Actor != ref {
			continue
		}
		short := c.Hash
		if len(short) > 7 {
			short = short[:7]
		}
		a.Commits = append(a.Commits, commitView{Short: short, Subject: c.Subject, Operation: c.Operation, Ago: ago(c.Date)})
		if len(a.Commits) == 10 {
			break
		}
	}
	return a
}

// ago renders a time as a coarse age: "just now", "4m", "3h", "2d".
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
