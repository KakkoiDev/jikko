package main

import (
	"net/http"
	"path"
	"strings"

	jikko "github.com/KakkoiDev/jikko"
)

// Pages for creating, renaming and deleting pages, and the problems page. Each reuses a core operation and answers through the
// same templates, anti-forgery check and permission filter as the rest.

// newView is the create-page form.
type newView struct {
	Path, Type, Body string
	Error            string
}

// changeReport presents what a rename or deletion did to references.
type changeReport struct {
	Title  string
	Lists  []changeList
	Hidden int
}

type changeList struct {
	Label   string
	Changes []jikko.RefChange
}

// problemsView groups check results by page.
type problemsView struct {
	Groups []problemGroup
	Hidden int
	Count  int
}

type problemGroup struct {
	Path, Title string
	Problems    []jikko.Problem
	Warnings    []jikko.Problem
}

// newPage shows the create form. It needs a login, like the operation.
func (s *server) newPage(w http.ResponseWriter, r *http.Request) {
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.actor(r, ws, true)
	if actor == "" {
		s.refuse(w, r, http.StatusUnauthorized, actor, "Log in to create a page.")
		return
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), New: &newView{Type: pageType(r.URL.Query().Get("type"))}})
}

func pageType(t string) string {
	switch t {
	case "task", "identity":
		return t
	}
	return "document"
}

// createPage creates a document, task or identity from the form, the same
// core operation as `jikko create`.
func (s *server) createPage(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	form := &newView{Path: strings.TrimSpace(r.PostFormValue("path")), Type: pageType(r.PostFormValue("type")), Body: strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n")}
	content := strings.TrimLeft(form.Body, "\n")
	if strings.TrimSpace(content) == "" {
		stem := strings.TrimSuffix(path.Base(form.Path), ".md")
		content = "# " + stem + "\n"
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	switch form.Type {
	case "task":
		content = "---\ntype: task\nstatus: todo\n---\n" + content
	case "identity":
		content = "---\ntype: identity\n---\n" + content
	}
	if form.Path == "" {
		form.Error = "A path for the page is required."
		s.respond(w, r, http.StatusUnprocessableEntity, pageData{Actor: actor, CSRF: s.csrfFor(w, r), New: form})
		return
	}
	if err := ws.CreatePage(actor, form.Path, content); err != nil {
		form.Error = err.Error()
		status := http.StatusUnprocessableEntity
		if jikko.IsPermissionError(err) {
			status = http.StatusForbidden
		}
		s.respond(w, r, status, pageData{Actor: actor, CSRF: s.csrfFor(w, r), New: form})
		return
	}
	target := form.Path
	if path.Ext(target) == "" {
		target += ".md"
	}
	target = path.Clean(target)
	if isHTMX(r) {
		w.Header().Set("HX-Push-Url", jikko.DefaultLinks.Page(target))
	}
	s.done(w, r, ws, actor, target, "Created "+target+".")
}

// renamePage moves a page and rewrites the references to it, the same core
// operation as `jikko rename`, and shows what was rewritten.
func (s *server) renamePage(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	pages, err := ws.ReadMany(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	p := pages[0]
	to := strings.TrimSpace(r.PostFormValue("to"))
	res, err := ws.Rename(actor, p.Path, to, jikko.RenameOptions{NoRewrite: r.PostFormValue("no_rewrite") != ""})
	if err != nil {
		s.operationFailed(w, r, ws, actor, p.Path, err)
		return
	}
	report := &changeReport{Title: "Renamed " + res.From + " to " + res.To + ".", Hidden: res.Hidden}
	report.add("Rewritten to keep pointing at this page", res.Rewritten)
	report.add("No longer resolve", res.Broken)
	report.add("Now resolve to this page, which changed their meaning", res.Captured)
	if actor == strings.TrimSuffix(res.From, ".md") {
		// An identity that renamed itself: its session is bound to the old
		// name and ends, so say so rather than show a page it cannot read.
		if isHTMX(r) {
			w.Header().Set("HX-Push-Url", "/")
		}
		s.respond(w, r, http.StatusOK, pageData{CSRF: s.csrfFor(w, r), Report: report, Flash: "You renamed your own identity, so this session has ended. Log in again with the same token."})
		return
	}
	again, err := ws.ReadMany(actor, res.To)
	if err != nil {
		s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Report: report})
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Push-Url", jikko.DefaultLinks.Page(res.To))
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Report: report, Detail: s.detail(ws, actor, again[0])})
}

// deletePage removes a page, the same core operation as `jikko delete`,
// and shows which references it breaks.
func (s *server) deletePage(w http.ResponseWriter, r *http.Request) {
	ws, actor, ok := s.mutationContext(w, r)
	if !ok {
		return
	}
	pages, err := ws.ReadMany(actor, r.PathValue("path"))
	if err != nil {
		s.refuse(w, r, http.StatusNotFound, actor, msgNotFound)
		return
	}
	p := pages[0]
	res, err := ws.Delete(actor, p.Path, jikko.DeleteOptions{Prune: r.PostFormValue("prune") != ""})
	if err != nil {
		s.operationFailed(w, r, ws, actor, p.Path, err)
		return
	}
	report := &changeReport{Title: "Deleted " + res.Path + ".", Hidden: res.Hidden}
	report.add("Removed from groups and access policies", res.Pruned)
	report.add("No longer resolve", res.Broken)
	report.add("Now resolve elsewhere, which changed their meaning", res.Captured)
	if isHTMX(r) {
		w.Header().Set("HX-Push-Url", "/")
	}
	if actor == strings.TrimSuffix(res.Path, ".md") {
		actor = ""
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Report: report})
}

func (c *changeReport) add(label string, changes []jikko.RefChange) {
	if len(changes) > 0 {
		c.Lists = append(c.Lists, changeList{Label: label, Changes: changes})
	}
}

// operationFailed shows the page again with the operation's error.
func (s *server) operationFailed(w http.ResponseWriter, r *http.Request, ws *jikko.Workspace, actor, pagePath string, err error) {
	status := http.StatusUnprocessableEntity
	if jikko.IsPermissionError(err) {
		status = http.StatusForbidden
	}
	pages, readErr := ws.ReadMany(actor, pagePath)
	if readErr != nil {
		s.refuse(w, r, status, actor, err.Error())
		return
	}
	s.respond(w, r, status, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Error: err.Error(), Detail: s.detail(ws, actor, pages[0])})
}

// problems shows what `jikko check` reports, for the pages the viewer may
// read.
func (s *server) problems(w http.ResponseWriter, r *http.Request) {
	ws, err := s.cache.load()
	if err != nil {
		s.fail(w, err)
		return
	}
	actor := s.actor(r, ws, true)
	problems, warnings, hidden, err := ws.CheckResults(actor)
	if err != nil {
		s.fail(w, err)
		return
	}
	view := &problemsView{Hidden: hidden, Count: len(problems)}
	groups := map[string]*problemGroup{}
	var order []string
	group := func(pagePath string) *problemGroup {
		g, ok := groups[pagePath]
		if !ok {
			g = &problemGroup{Path: pagePath, Title: ws.Pages[pagePath].Title}
			groups[pagePath] = g
			order = append(order, pagePath)
		}
		return g
	}
	for _, pr := range problems {
		g := group(pr.Path)
		g.Problems = append(g.Problems, pr)
	}
	for _, wn := range warnings {
		g := group(wn.Path)
		g.Warnings = append(g.Warnings, wn)
	}
	for _, pagePath := range order {
		view.Groups = append(view.Groups, *groups[pagePath])
	}
	s.respond(w, r, http.StatusOK, pageData{Actor: actor, CSRF: s.csrfFor(w, r), Problems: view})
}
