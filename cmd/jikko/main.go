package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"strconv"
	"strings"

	jikko "github.com/KakkoiDev/jikko"
)

var commands = map[string]func([]string) error{
	"list": list, "show": show, "auth": auth, "set": set,
	"perm": perm, "serve": serve, "check": check,
	"tree": tree, "mentions": mentions, "create": create, "commit": commit, "export": exportWorkspace,
	"comment": comment, "rename": rename, "delete": deletePage,
	"view": view, "save": save, "upload": upload,
	"init": initWorkspace, "log": logHistory, "version": printVersion,
	"web": web, "identity": identity,
}

// help reads commands, so it is registered after the map is built.
func init() { commands["help"] = help }

func main() {
	log.SetFlags(0)
	log.SetPrefix("jikko: ")
	os.Exit(run(os.Args[1:]))
}

// run dispatches one command and returns the process exit status: 0 for
// success and for help, 1 for a failed command, 2 for a usage error or an
// unknown command.
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 0
	}
	name := args[0]
	if name == "-h" || name == "--help" {
		name = "help"
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "jikko: unknown command %q; run `jikko help` for the list\n", name)
		return 2
	}
	err := cmd(args[1:])
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintf(os.Stderr, "jikko: %v\n", err)
	var usageErr usageError
	if errors.As(err, &usageErr) {
		return 2
	}
	return 1
}

// flags builds a flag set carrying the options every command shares and
// returns the positional arguments. Flags may appear anywhere, so
// `jikko set task status done --dir w` reads as naturally as the other order.
func flags(name string, args []string, extra func(*flag.FlagSet)) ([]string, *string, *string, error) {
	var token *string
	positional, dir, err := parseFlags(name, args, func(f *flag.FlagSet) {
		token = f.String("token", os.Getenv("JIKKO_TOKEN"), "bearer token (or JIKKO_TOKEN)")
		if extra != nil {
			extra(f)
		}
	})
	return positional, dir, token, err
}

// flagsNoToken is flags for a command that acts as nobody: it would be
// misleading to accept a token it ignores.
func flagsNoToken(name string, args []string, extra func(*flag.FlagSet)) ([]string, *string, error) {
	return parseFlags(name, args, extra)
}

func parseFlags(name string, args []string, extra func(*flag.FlagSet)) ([]string, *string, error) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Usage = func() {}
	dir := f.String("dir", ".", "workspace directory")
	if extra != nil {
		extra(f)
	}
	var positional []string
	for {
		if err := f.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printHelp(name, f, os.Stdout)
				return nil, dir, err
			}
			return nil, dir, usageError{command: name, detail: err.Error()}
		}
		rest := f.Args()
		if len(rest) == 0 {
			return positional, dir, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// commitFlag adds --commit to a mutating command.
func commitFlag(f *flag.FlagSet) *bool {
	return f.Bool("commit", false, "record the change as one Git commit with Jikko-Actor and Jikko-Operation trailers")
}

// recorder reports the commits a workspace records when --commit is on.
type recorder struct {
	last *jikko.CommitResult
}

// record turns on attributed commits for the workspace when on is true, and
// notes each one on standard error.
func record(w *jikko.Workspace, on bool) *recorder {
	r := &recorder{}
	if !on {
		return r
	}
	w.AutoCommit = true
	w.OnCommit = func(res *jikko.CommitResult) {
		r.last = res
		fmt.Fprintf(os.Stderr, "jikko: committed %s (%s)\n", shortHash(res.Commit), res.Operation)
	}
	return r
}

// commit returns the hash of the last recorded commit, for JSON output.
func (r *recorder) commit() string {
	if r.last == nil {
		return ""
	}
	return r.last.Commit
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// openWorkspace loads a workspace and notes, without failing, that it has
// defects worth inspecting. Content problems never block ordinary reads.
func openWorkspace(dir string) (*jikko.Workspace, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("workspace directory %q does not exist; run `jikko init --dir %s` to create one", dir, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %q is not a directory", dir)
	}
	w, err := jikko.Open(dir)
	if err != nil {
		return nil, err
	}
	if n := len(w.Problems); n > 0 {
		fmt.Fprintf(os.Stderr, "jikko: %d workspace problem(s); run `jikko check` for detail\n", n)
	}
	return w, nil
}

// warnWithoutGit says once, on standard error, that a workspace has no Git
// history behind it (specification §10).
func warnWithoutGit(root string) {
	if !jikko.GitBacked(root) {
		fmt.Fprintf(os.Stderr, "jikko: warning: %s is not Git-backed; history, audit, and three-way merge are unavailable (run `git init` or `jikko init`)\n", root)
	}
}

// warnTrackedAuth warns prominently that credential hashes are in history.
func warnTrackedAuth(root string) {
	if jikko.AuthTrackedByGit(root) {
		fmt.Fprintln(os.Stderr, "jikko: WARNING: .auth.md is tracked by Git. Credential hashes are in repository history;")
		fmt.Fprintln(os.Stderr, "jikko:          adding it to .gitignore now does not remove them. Untrack it and rotate tokens.")
	}
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

var errAuthRequired = errors.New("authentication required: pass --token or set JIKKO_TOKEN")

// authenticated opens a workspace and requires an authenticated caller.
func authenticated(dir, token string) (*jikko.Workspace, string, error) {
	w, err := openWorkspace(dir)
	if err != nil {
		return nil, "", err
	}
	actor, err := actorFor(w, token)
	if err != nil {
		return nil, "", err
	}
	if actor == "" {
		return nil, "", errAuthRequired
	}
	return w, actor, nil
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

// readInput reads a file, or standard input for "-".
func readInput(name string) ([]byte, error) {
	if name == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading standard input: %w", err)
		}
		return b, nil
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", name, reason(err))
	}
	return b, nil
}

// reason strips the path from a file error, which the message already names.
func reason(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

func list(args []string) error {
	var typ, status, assignee *string
	var asJSON *bool
	args, dir, token, err := flags("list", args, func(f *flag.FlagSet) {
		typ = f.String("type", "", "document, task, view, or identity")
		status = f.String("status", "", "status metadata")
		assignee = f.String("assignee", "", "pages assigned to an identity, or `me` for the authenticated one")
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("list")
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
	if *assignee != "" {
		who := *assignee
		if who == "me" {
			if actor == "" {
				return fmt.Errorf("--assignee me: %w", errAuthRequired)
			}
			who = actor
		}
		if _, ok := w.ResolveIdentity(who); !ok {
			return fmt.Errorf("identity %q not found or ambiguous", who)
		}
		assigned := visible[:0]
		for _, p := range visible {
			if w.AssignedTo(p, who) {
				assigned = append(assigned, p)
			}
		}
		visible = assigned
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(visible)
	}
	for _, p := range visible {
		fmt.Printf("%-9s %s\n", p.Kind, p.Path)
	}
	return nil
}

func show(args []string) error {
	var asJSON, raw *bool
	args, dir, token, err := flags("show", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		raw = f.Bool("raw", false, "print one page's exact source, for editing and `jikko save`")
	})
	if err != nil {
		return err
	}
	if len(args) < 1 || (*raw && (len(args) != 1 || *asJSON)) {
		return usageOf("show")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	if *raw {
		src, p, err := w.Source(actor, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "jikko: %s rev %s\n", p.Path, p.Rev)
		_, err = os.Stdout.Write(src)
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
	var doCommit *bool
	args, dir, token, err := flags("set", args, func(f *flag.FlagSet) { doCommit = commitFlag(f) })
	if err != nil {
		return err
	}
	if len(args) != 3 {
		return usageOf("set")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	if err := w.SetMetadata(actor, args[0], args[1], args[2]); err != nil {
		return err
	}
	warnUnresolved(w, args[0])
	return nil
}

// warnUnresolved notes, without failing, what the change left unmet: a Task
// marked done with unresolved comments, unfinished dependencies, or unmet
// completion requirements. The specification asks for warnings, not refusals.
func warnUnresolved(w *jikko.Workspace, ref string) {
	p, ok := w.Resolve(ref)
	if !ok {
		return
	}
	for _, warning := range w.Warnings() {
		if warning.Path == p.Path {
			fmt.Fprintf(os.Stderr, "jikko: warning: %s: %s\n", warning.Path, warning.Message)
		}
	}
}

func comment(args []string) error {
	var asJSON, doCommit *bool
	args, dir, token, err := flags("comment", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usageOf("comment")
	}
	op, args := args[0], args[1:]
	arity := map[string]int{"add": 3, "reply": 3, "resolve": 2}
	if n, known := arity[op]; (known && len(args) != n) || (!known && (op != "list" || len(args) > 1)) {
		return usageOf("comment")
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
		return errAuthRequired
	}
	rec := record(w, *doCommit)
	id := ""
	switch op {
	case "add":
		if id, err = w.AddComment(actor, args[0], args[1], args[2]); err != nil {
			return err
		}
	case "reply":
		id = args[1]
		err = w.ReplyComment(actor, args[0], id, args[2])
	default:
		id = args[1]
		err = w.ResolveComment(actor, args[0], id)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		out := map[string]string{"id": id}
		if p, ok := w.Resolve(args[0]); ok {
			out["path"] = p.Path
		}
		if c := rec.commit(); c != "" {
			out["commit"] = c
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if op == "add" {
		fmt.Println(id)
	}
	return nil
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
	var doCommit *bool
	args, dir, token, err := flags("perm", args, func(f *flag.FlagSet) { doCommit = commitFlag(f) })
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return usageOf("perm")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	return w.SetPermission(actor, args[0], args[1], args[2:]...)
}

func auth(args []string) error {
	var id *string
	var asJSON *bool
	args, dir, err := flagsNoToken("auth", args, func(f *flag.FlagSet) {
		id = f.String("id", "", "with revoke: the one credential to remove, from `jikko auth list`")
		asJSON = f.Bool("json", false, "with list: JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usageOf("auth")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	switch {
	case args[0] == "create" && len(args) == 2:
		token, err := w.CreateCredential(args[1])
		if err != nil {
			return err
		}
		warnTrackedAuth(w.Root)
		fmt.Println(token)
	case args[0] == "list" && len(args) == 1:
		credentials, err := w.Credentials()
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(credentials)
		}
		for _, c := range credentials {
			created := c.Created
			if created == "" {
				created = "-"
			}
			fmt.Printf("%-24s %s  %s\n", c.Identity, c.ID, created)
		}
	case args[0] == "revoke" && len(args) == 2:
		if *id != "" {
			return w.RevokeCredential(args[1], *id)
		}
		return w.RevokeCredentials(args[1])
	default:
		return usageOf("auth")
	}
	return nil
}

func check(args []string) error {
	var asJSON *bool
	args, dir, err := flagsNoToken("check", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("check")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	warnWithoutGit(w.Root)
	unresolved, err := w.UnresolvedReferences()
	if err != nil {
		return err
	}
	problems := append(append([]jikko.Problem(nil), w.Problems...), unresolved...)
	warnings := w.Warnings()
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
		return usageOf("tree")
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
	var since *string
	args, dir, token, err := flags("mentions", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		since = f.String("since", "", "only pages changed since this Git revision")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("mentions")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	pages := w.MentionDetails(actor)
	if *since != "" {
		changed, err := w.ChangedSince(*since)
		switch {
		case errors.Is(err, jikko.ErrNoGit):
			fmt.Fprintf(os.Stderr, "jikko: warning: %s is not Git-backed; --since is ignored and every mention is listed\n", w.Root)
		case err != nil:
			return err
		default:
			set := map[string]bool{}
			for _, p := range changed {
				set[p] = true
			}
			kept := pages[:0]
			for _, m := range pages {
				if set[m.Path] {
					kept = append(kept, m)
				}
			}
			pages = kept
		}
	}
	if pages == nil {
		pages = []jikko.MentionPage{}
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(pages)
	}
	for _, m := range pages {
		for _, hit := range m.Matches {
			where := fmt.Sprintf("%s:%d", m.Path, hit.Line)
			if hit.Comment != "" {
				where += " (" + hit.Comment + ")"
			}
			fmt.Printf("%s: %s\n", where, hit.Text)
		}
	}
	return nil
}

func create(args []string) error {
	var body, file, typ *string
	var asJSON, doCommit *bool
	args, dir, token, err := flags("create", args, func(f *flag.FlagSet) {
		body = f.String("body", "", "Markdown body")
		file = f.String("file", "", "read the body from a file, or - for standard input")
		typ = f.String("type", "document", "document, task, view, or identity")
		asJSON = f.Bool("json", false, "JSON output")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) != 1 || (*body != "" && *file != "") {
		return usageOf("create")
	}
	kind, err := parseKindFlag(*typ)
	if err != nil {
		return err
	}
	content := *body
	if *file != "" {
		b, err := readInput(*file)
		if err != nil {
			return err
		}
		content = string(b)
	}
	source, err := jikko.NewPageSource(kind, content)
	if err != nil {
		return err
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	rec := record(w, *doCommit)
	if err := w.CreatePage(actor, args[0], string(source)); err != nil {
		return err
	}
	p, ok := w.Resolve(args[0])
	if !ok {
		return fmt.Errorf("%s was created but does not resolve; run `jikko check`", args[0])
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Path   string     `json:"path"`
			Type   jikko.Kind `json:"type"`
			Rev    string     `json:"rev"`
			Commit string     `json:"commit,omitempty"`
		}{p.Path, p.Kind, p.Rev, rec.commit()})
	}
	fmt.Printf("created %s\n", p.Path)
	return nil
}

func commit(args []string) error {
	var operation *string
	var asJSON *bool
	args, dir, token, err := flags("commit", args, func(f *flag.FlagSet) {
		operation = f.String("operation", "workspace-mutation", "audit operation name")
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return usageOf("commit")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	warnTrackedAuth(w.Root)
	res, err := w.CommitChanges(actor, strings.Join(args, " "), *operation, nil)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	if res.Empty {
		fmt.Println("nothing to commit")
		return nil
	}
	fmt.Printf("committed %s\n", shortHash(res.Commit))
	return nil
}

func logHistory(args []string) error {
	var asJSON *bool
	var limit *int
	args, dir, token, err := flags("log", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		limit = f.Int("limit", 0, "show at most this many commits (0: all)")
	})
	if err != nil {
		return err
	}
	if len(args) > 1 {
		return usageOf("log")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	ref := ""
	if len(args) == 1 {
		ref = args[0]
	}
	entries, err := w.Log(actor, ref, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(entries)
	}
	for _, e := range entries {
		by, op := e.Actor, e.Operation
		if by == "" {
			by = e.Author + " (git)"
		}
		if op == "" {
			op = "-"
		}
		fmt.Printf("%s  %s  %s  %s  %s\n", shortHash(e.Commit), e.Date, by, op, e.Subject)
	}
	return nil
}

func exportWorkspace(args []string) error {
	var output *string
	args, dir, err := flagsNoToken("export", args, func(f *flag.FlagSet) {
		output = f.String("output", "jikko-workspace.zip", "output ZIP path")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("export")
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

func rename(args []string) error {
	var asJSON, noRewrite, doCommit *bool
	args, dir, token, err := flags("rename", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		noRewrite = f.Bool("no-rewrite", false, "move the file only and report the references that break")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return usageOf("rename")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	res, err := w.Rename(actor, args[0], args[1], jikko.RenameOptions{NoRewrite: *noRewrite})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	fmt.Printf("renamed %s -> %s\n", res.From, res.To)
	for _, c := range res.Rewritten {
		fmt.Printf("  rewrote %s (%s): %s -> %s\n", c.Path, c.Field, c.From, c.To)
	}
	reportRefs("no longer resolves", res.Broken)
	reportRefs("now resolves to "+res.To, res.Captured)
	reportHidden(res.Hidden)
	return nil
}

func deletePage(args []string) error {
	var asJSON, prune, doCommit *bool
	args, dir, token, err := flags("delete", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		prune = f.Bool("prune", false, "remove a deleted identity from group members and access policies")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageOf("delete")
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	res, err := w.Delete(actor, args[0], jikko.DeleteOptions{Prune: *prune})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	fmt.Printf("deleted %s\n", res.Path)
	for _, c := range res.Pruned {
		fmt.Printf("  removed %s from %s (%s)\n", c.From, c.Path, c.Field)
	}
	reportRefs("no longer resolves", res.Broken)
	reportRefs("now resolves", res.Captured)
	reportHidden(res.Hidden)
	return nil
}

func reportRefs(what string, changes []jikko.RefChange) {
	for _, c := range changes {
		fmt.Fprintf(os.Stderr, "jikko: warning: %s (%s): %q %s\n", c.Path, c.Field, c.From, what)
	}
}

func reportHidden(n int) {
	if n > 0 {
		fmt.Fprintf(os.Stderr, "jikko: %d affected reference(s) are in pages you cannot read\n", n)
	}
}

func view(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("view", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageOf("view")
	}
	w, err := openWorkspace(*dir)
	if err != nil {
		return err
	}
	actor, err := actorFor(w, *token)
	if err != nil {
		return err
	}
	res, err := w.EvaluateView(actor, args[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	printItems := func(indent string, items []jikko.ViewItem) {
		for _, it := range items {
			fmt.Printf("%s%-9s %-30s %s\n", indent, it.Type, it.Path, it.Title)
		}
	}
	if res.Groups == nil {
		printItems("", res.Pages)
		return nil
	}
	for _, g := range res.Groups {
		key := g.Key
		if key == "" {
			key = "(no " + res.Spec.Group + ")"
		}
		fmt.Printf("%s: %s (%d)\n", res.Spec.Group, key, len(g.Pages))
		printItems("  ", g.Pages)
	}
	return nil
}

func save(args []string) error {
	var asJSON, doCommit *bool
	var rev, file, base, against *string
	resolve := map[string]string{}
	args, dir, token, err := flags("save", args, func(f *flag.FlagSet) {
		against = f.String("against", "", "current revision a conflict was reported for; --resolve applies only while the page is still at it")
		f.Func("resolve", "settle a reported conflict: <id>=current or <id>=yours (repeatable)", func(v string) error {
			id, side, ok := strings.Cut(v, "=")
			if !ok || (side != jikko.TakeCurrent && side != jikko.TakeYours) {
				return fmt.Errorf("--resolve takes <conflict id>=current or <conflict id>=yours")
			}
			resolve[id] = side
			return nil
		})
		asJSON = f.Bool("json", false, "JSON output, including a structured conflict")
		rev = f.String("rev", "", "revision the edit was based on (from show --json or show --raw)")
		file = f.String("file", "-", "edited source, or - for standard input")
		base = f.String("base", "", "the source as originally read, so a concurrent change can be merged without Git history")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageOf("save")
	}
	yours, err := readInput(*file)
	if err != nil {
		return err
	}
	opt := jikko.SaveOptions{Resolve: resolve, ResolveRev: *against}
	if len(resolve) > 0 && *against == "" {
		return errors.New("--resolve needs --against <current rev> from the reported conflict")
	}
	if *base != "" {
		if opt.Base, err = readInput(*base); err != nil {
			return err
		}
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	res, err := w.SaveSource(actor, args[0], *rev, yours, opt)
	var conflict *jikko.ConflictError
	if errors.As(err, &conflict) {
		if *asJSON {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"conflict": conflict.Conflict}); err != nil {
				return err
			}
		} else {
			printConflict(conflict.Conflict)
		}
		return err
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	verb := "saved"
	if res.Merged {
		verb = "merged"
	}
	fmt.Printf("%s %s rev %s\n", verb, res.Path, res.Rev)
	return nil
}

func printConflict(c jikko.Conflict) {
	fmt.Printf("conflict in %s: base %s, current %s\n", c.Path, c.BaseRev, c.CurrentRev)
	if !c.BaseKnown {
		fmt.Println("  the base revision was not found: pass --base with the source you edited, or commit with Git")
	}
	value := func(v *string) string {
		if v == nil {
			return "(absent)"
		}
		return *v
	}
	for _, f := range c.Fields {
		fmt.Printf("  [%s] base %s, current %s, yours %s\n", f.ID, value(f.Base), value(f.Current), value(f.Yours))
	}
	indent := func(s string) string {
		return strings.TrimSuffix(strings.ReplaceAll("\n"+s, "\n", "\n      "), "\n      ")
	}
	for _, b := range c.Body {
		fmt.Printf("  [%s] body at line %d:\n    base:%s\n    current:%s\n    yours:%s\n", b.ID, b.Line, indent(b.Base), indent(b.Current), indent(b.Yours))
	}
	fmt.Printf("  settle with: --against %s --resolve <id>=current|yours\n", c.CurrentRev)
}

func upload(args []string) error {
	var asJSON, doCommit *bool
	var into, as, maxSize *string
	args, dir, token, err := flags("upload", args, func(f *flag.FlagSet) {
		asJSON = f.Bool("json", false, "JSON output")
		into = f.String("into", "", "page to embed the file into")
		as = f.String("as", "", "destination path in the workspace (default: the file's name next to --into, or at the root)")
		maxSize = f.String("max-upload", os.Getenv("JIKKO_MAX_UPLOAD"), "maximum upload size, such as 25M (or JIKKO_MAX_UPLOAD)")
		doCommit = commitFlag(f)
	})
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageOf("upload")
	}
	limit, err := parseSize(*maxSize)
	if err != nil {
		return err
	}
	info, err := os.Stat(args[0])
	if err != nil {
		return fmt.Errorf("cannot read %s: %v", args[0], reason(err))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", args[0])
	}
	if info.Size() > limit {
		return fmt.Errorf("%s is %d bytes; the maximum upload size is %d bytes", args[0], info.Size(), limit)
	}
	data, err := readInput(args[0])
	if err != nil {
		return err
	}
	w, actor, err := authenticated(*dir, *token)
	if err != nil {
		return err
	}
	record(w, *doCommit)
	res, err := w.Upload(actor, args[0], data, jikko.UploadOptions{Path: *as, Into: *into, MaxBytes: limit})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	if res.Into != "" {
		fmt.Printf("uploaded %s (%d bytes) and embedded ![[%s]] in %s\n", res.Path, res.Size, res.Embed, res.Into)
	} else {
		fmt.Printf("uploaded %s (%d bytes)\n", res.Path, res.Size)
	}
	return nil
}

// parseSize reads a byte count such as 1048576, 512K, 25M, or 1G (binary
// multiples). Empty means the default maximum upload size.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return jikko.DefaultMaxUpload, nil
	}
	mult := int64(1)
	for suffix, m := range map[string]int64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30} {
		if trimmed, ok := strings.CutSuffix(strings.TrimSuffix(strings.TrimSuffix(s, "B"), "I"), suffix); ok {
			s, mult = trimmed, m
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q: use a byte count such as 26214400 or 25M", s)
	}
	return n * mult, nil
}
