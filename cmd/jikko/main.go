package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	jikko "github.com/KakkoiDev/jikko"
)

var commands = map[string]func([]string) error{
	"list": list, "show": show, "auth": auth, "set": set,
	"perm": perm, "serve": serve, "check": check,
	"tree": tree, "mentions": mentions, "create": create, "commit": commit, "export": exportWorkspace,
	"comment": comment,
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("jikko: ")
	if len(os.Args) < 2 {
		usage()
		return
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			log.Print(err)
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`jikko <command> [flags]

  list    list pages visible to you
  show    print one or more pages (batch reads)
  tree    list the permission-filtered workspace tree
  mentions list pages addressing the authenticated identity
  create  create a Markdown document or task
  commit  record changes with actor-attributed Git audit trailers
  export  export a portable workspace ZIP
  set     set a metadata property
  perm    grant or clear a capability on a page
  comment add, reply to, resolve, or list inline comments
  auth    create or revoke a credential
  check   report workspace problems and broken references
  serve   serve the workspace over HTTP

Set JIKKO_TOKEN for authenticated CLI operations.`)
}

// flags builds a flag set carrying the options every command shares and
// returns the positional arguments. Flags may appear anywhere, so
// `jikko set task status done --dir w` reads as naturally as the other order.
func flags(name string, args []string, extra func(*flag.FlagSet)) ([]string, *string, *string, error) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	dir := f.String("dir", ".", "workspace directory")
	token := f.String("token", os.Getenv("JIKKO_TOKEN"), "bearer token (or JIKKO_TOKEN)")
	if extra != nil {
		extra(f)
	}
	var positional []string
	for {
		if err := f.Parse(args); err != nil {
			return nil, dir, token, err
		}
		rest := f.Args()
		if len(rest) == 0 {
			return positional, dir, token, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// openWorkspace loads a workspace and notes, without failing, that it has
// defects worth inspecting. Content problems never block ordinary reads.
func openWorkspace(dir string) (*jikko.Workspace, error) {
	w, err := jikko.Open(dir)
	if err != nil {
		return nil, err
	}
	if n := len(w.Problems); n > 0 {
		fmt.Fprintf(os.Stderr, "jikko: %d workspace problem(s); run `jikko check` for detail\n", n)
	}
	return w, nil
}

func actorFor(w *jikko.Workspace, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	p, err := w.Authenticate(token)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p.Path, ".md"), nil
}

func parseKindFlag(s string) (jikko.Kind, error) {
	if s == "" {
		return "", nil
	}
	kind, ok := jikko.ParseKind(s)
	if !ok {
		return "", fmt.Errorf("unknown type %q: expected document, task, view, or identity", s)
	}
	return kind, nil
}

func visiblePages(w *jikko.Workspace, actor string, pages []*jikko.Page) []*jikko.Page {
	visible := make([]*jikko.Page, 0, len(pages))
	for _, p := range pages {
		if w.Allowed(actor, p, jikko.Read) {
			visible = append(visible, p)
		}
	}
	return visible
}

func list(args []string) error {
	var typ, status *string
	var asJSON *bool
	args, dir, token, err := flags("list", args, func(f *flag.FlagSet) {
		typ = f.String("type", "", "document, task, view, or identity")
		status = f.String("status", "", "status metadata")
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko list [flags]")
	}
	kind, err := parseKindFlag(*typ)
	if err != nil {
		return err
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	visible := visiblePages(w, actor, w.List(kind, *status))
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(visible)
	}
	for _, p := range visible {
		fmt.Printf("%-9s %s\n", p.Kind, p.Path)
	}
	return nil
}

func show(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("show", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return errors.New("usage: jikko show [flags] <reference> [reference...]")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	pages, err := w.ReadMany(actor, args...)
	if err != nil {
		return err
	}
	if *asJSON {
		if len(pages) == 1 {
			return json.NewEncoder(os.Stdout).Encode(pages[0])
		}
		return json.NewEncoder(os.Stdout).Encode(pages)
	}
	for i, p := range pages {
		if i > 0 {
			fmt.Println("\n---")
		}
		fmt.Printf("%s\n\n%s", p.Title, strings.TrimLeft(p.Body, "\n"))
	}
	return nil
}

func set(args []string) error {
	args, dir, token, err := flags("set", args, nil)
	if err != nil {
		return err
	}
	if len(args) != 3 {
		return errors.New("usage: jikko set [flags] <reference> <property> <value>")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	if err := w.SetMetadata(actor, args[0], args[1], args[2]); err != nil {
		return err
	}
	warnUnresolved(w, args[0])
	return nil
}

// warnUnresolved notes, without failing, that a Task now marked done still has
// unresolved comments. The specification asks for a warning, not a refusal.
func warnUnresolved(w *jikko.Workspace, ref string) {
	p, ok := w.Resolve(ref)
	if !ok {
		return
	}
	for _, warning := range w.UnresolvedCommentWarnings() {
		if warning.Path == p.Path {
			fmt.Fprintf(os.Stderr, "jikko: warning: %s: %s\n", warning.Path, warning.Message)
		}
	}
}

const commentUsage = `usage:
  jikko comment add [flags] <reference> <anchor text> <message>
  jikko comment reply [flags] <reference> <id> <message>
  jikko comment resolve [flags] <reference> <id>
  jikko comment list [flags] [reference]`

func comment(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("comment", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New(commentUsage)
	}
	op, args := args[0], args[1:]
	arity := map[string]int{"add": 3, "reply": 3, "resolve": 2}
	if n, known := arity[op]; (known && len(args) != n) || (!known && (op != "list" || len(args) > 1)) {
		return errors.New(commentUsage)
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if op == "list" {
		return listComments(w, actor, args, *asJSON)
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	switch op {
	case "add":
		id, err := w.AddComment(actor, args[0], args[1], args[2])
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"id": id})
		}
		fmt.Println(id)
		return nil
	case "reply":
		return w.ReplyComment(actor, args[0], args[1], args[2])
	default:
		return w.ResolveComment(actor, args[0], args[1])
	}
}

func listComments(w *jikko.Workspace, actor string, args []string, asJSON bool) error {
	comments := w.Comments(actor)
	if len(args) == 1 {
		pages, err := w.ReadMany(actor, args[0])
		if err != nil {
			return err
		}
		kept := comments[:0]
		for _, c := range comments {
			if c.Path == pages[0].Path {
				kept = append(kept, c)
			}
		}
		comments = kept
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(comments)
	}
	for _, c := range comments {
		fmt.Printf("%s#%s %q\n", c.Path, c.ID, c.Anchor)
		for _, e := range c.Entries {
			author := e.Author
			if author == "" {
				author = "?"
			}
			fmt.Printf("  @%s: %s\n", author, strings.ReplaceAll(e.Text, "\n", "\n    "))
		}
	}
	return nil
}

func perm(args []string) error {
	args, dir, token, err := flags("perm", args, nil)
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("usage: jikko perm [flags] <reference> <read|comment|write|admin> [identity...]")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	return w.SetPermission(actor, args[0], args[1], args[2:]...)
}

func auth(args []string) error {
	args, dir, _, err := flags("auth", args, nil)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return errors.New("usage: jikko auth [flags] <create|revoke> <identity>")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		token, err := w.CreateCredential(args[1])
		if err != nil {
			return err
		}
		if jikko.AuthTrackedByGit(w.Root) {
			fmt.Fprintln(os.Stderr, "jikko: WARNING: .auth.md is tracked by Git. Credential hashes are in repository history;")
			fmt.Fprintln(os.Stderr, "jikko:          adding it to .gitignore now does not remove them. Untrack it and rotate tokens.")
		}
		fmt.Println(token)
	case "revoke":
		return w.RevokeCredentials(args[1])
	default:
		return errors.New("usage: jikko auth [flags] <create|revoke> <identity>")
	}
	return nil
}

func check(args []string) error {
	var asJSON *bool
	args, dir, _, err := flags("check", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko check [flags]")
	}
	w, err := jikko.Open(*dir)
	if err != nil {
		return err
	}
	problems := append([]jikko.Problem(nil), w.Problems...)
	for _, p := range w.List("", "") {
		for _, ref := range append(append([]string{}, p.Links...), p.Embeds...) {
			if _, ok := w.Resolve(ref); !ok {
				problems = append(problems, jikko.Problem{Path: p.Path, Kind: "reference", Message: fmt.Sprintf("unresolved %q", ref)})
			}
		}
	}
	warnings := w.UnresolvedCommentWarnings()
	if warnings == nil {
		warnings = []jikko.Problem{}
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Pages    int             `json:"pages"`
			Problems []jikko.Problem `json:"problems"`
			Warnings []jikko.Problem `json:"warnings"`
		}{len(w.Pages), problems, warnings}); err != nil {
			return err
		}
	} else {
		for _, p := range problems {
			fmt.Printf("%s: [%s] %s\n", p.Path, p.Kind, p.Message)
		}
		// Warnings do not fail the check: completing a Task with open
		// discussion is allowed, only surfaced.
		for _, p := range warnings {
			fmt.Printf("%s: warning: [%s] %s\n", p.Path, p.Kind, p.Message)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s) in %d pages", len(problems), len(w.Pages))
	}
	if !*asJSON {
		fmt.Printf("ok: %d pages\n", len(w.Pages))
	}
	return nil
}

func tree(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("tree", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko tree [flags]")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	entries := w.Tree(actor)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(entries)
	}
	for _, e := range entries {
		fmt.Printf("%-9s %s\n", e.Type, e.Path)
	}
	return nil
}

func mentions(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("mentions", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko mentions [flags]")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	pages := w.Mentions(actor)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(pages)
	}
	for _, p := range pages {
		fmt.Printf("%s\n", p.Path)
	}
	return nil
}

func create(args []string) error {
	var body, typ *string
	args, dir, token, err := flags("create", args, func(f *flag.FlagSet) {
		body = f.String("body", "", "Markdown body")
		typ = f.String("type", "document", "document or task")
	})
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return errors.New("usage: jikko create [flags] <path> --body <markdown> [--type document|task]")
	}
	if *typ != "document" && *typ != "task" {
		return errors.New("--type must be document or task")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	content := *body
	if *typ == "task" {
		content = "---\ntype: task\nstatus: todo\n---\n" + content
	}
	return w.CreatePage(actor, args[0], content)
}

func commit(args []string) error {
	var operation *string
	args, dir, token, err := flags("commit", args, func(f *flag.FlagSet) {
		operation = f.String("operation", "workspace-mutation", "audit operation name")
	})
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return errors.New("usage: jikko commit [flags] <message>")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if actor == "" {
		return errors.New("authentication required: pass --token or set JIKKO_TOKEN")
	}
	return w.Commit(actor, strings.Join(args, " "), *operation)
}

func exportWorkspace(args []string) error {
	var output *string
	args, dir, _, err := flags("export", args, func(f *flag.FlagSet) {
		output = f.String("output", "jikko-workspace.zip", "output ZIP path")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return errors.New("usage: jikko export [flags]")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	data, err := w.ExportZIP()
	if err != nil {
		return err
	}
	return os.WriteFile(*output, data, 0644)
}
