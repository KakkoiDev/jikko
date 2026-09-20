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

func main() {
	log.SetFlags(0)
	log.SetPrefix("jikko: ")
	if len(os.Args) < 2 {
		usage()
		return
	}
	commands := map[string]func([]string) error{
		"list": list, "show": show, "auth": auth, "set": set,
		"perm": perm, "serve": serve, "check": check,
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) { log.Print(err) }
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`jikko <command> [flags]

  list    list pages visible to you
  show    print one page
  set     set a metadata property
  perm    grant or clear a capability on a page
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
	if extra != nil { extra(f) }
	var positional []string
	for {
		if err := f.Parse(args); err != nil { return nil, dir, token, err }
		rest := f.Args()
		if len(rest) == 0 { return positional, dir, token, nil }
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// openWorkspace loads a workspace and notes, without failing, that it has
// defects worth inspecting. Content problems never block ordinary reads.
func openWorkspace(dir string) (*jikko.Workspace, error) {
	w, err := jikko.Open(dir)
	if err != nil { return nil, err }
	if n := len(w.Problems); n > 0 {
		fmt.Fprintf(os.Stderr, "jikko: %d workspace problem(s); run `jikko check` for detail\n", n)
	}
	return w, nil
}

func actorFor(w *jikko.Workspace, token string) (string, error) {
	if token == "" { return "", nil }
	p, err := w.Authenticate(token)
	if err != nil { return "", err }
	return strings.TrimSuffix(p.Path, ".md"), nil
}

func parseKindFlag(s string) (jikko.Kind, error) {
	if s == "" { return "", nil }
	kind, ok := jikko.ParseKind(s)
	if !ok { return "", fmt.Errorf("unknown type %q: expected document, task, view, or identity", s) }
	return kind, nil
}

func visiblePages(w *jikko.Workspace, actor string, pages []*jikko.Page) []*jikko.Page {
	visible := make([]*jikko.Page, 0, len(pages))
	for _, p := range pages {
		if w.Allowed(actor, p, jikko.Read) { visible = append(visible, p) }
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
	if err != nil { return err }
	if len(args) != 0 { return errors.New("usage: jikko list [flags]") }
	kind, err := parseKindFlag(*typ)
	if err != nil { return err }
	w, err := openWorkspace(*dir)
	if err != nil { return err }
	actor, err := actorFor(w, *token)
	if err != nil { return err }
	visible := visiblePages(w, actor, w.List(kind, *status))
	if *asJSON { return json.NewEncoder(os.Stdout).Encode(visible) }
	for _, p := range visible { fmt.Printf("%-9s %s\n", p.Kind, p.Path) }
	return nil
}

func show(args []string) error {
	var asJSON *bool
	args, dir, token, err := flags("show", args, func(f *flag.FlagSet) { asJSON = f.Bool("json", false, "JSON output") })
	if err != nil { return err }
	if len(args) != 1 { return errors.New("usage: jikko show [flags] <reference>") }
	w, err := openWorkspace(*dir)
	if err != nil { return err }
	actor, err := actorFor(w, *token)
	if err != nil { return err }
	p, ok := w.Resolve(args[0])
	// One answer for absent, ambiguous, and forbidden: a denial must not
	// disclose that a restricted page exists.
	if !ok || !w.Allowed(actor, p, jikko.Read) {
		return errors.New("reference not found, ambiguous, or not permitted")
	}
	if *asJSON { return json.NewEncoder(os.Stdout).Encode(p) }
	fmt.Printf("%s\n\n%s", p.Title, strings.TrimLeft(p.Body, "\n"))
	return nil
}

func set(args []string) error {
	args, dir, token, err := flags("set", args, nil)
	if err != nil { return err }
	if len(args) != 3 { return errors.New("usage: jikko set [flags] <reference> <property> <value>") }
	w, err := openWorkspace(*dir)
	if err != nil { return err }
	actor, err := actorFor(w, *token)
	if err != nil { return err }
	if actor == "" { return errors.New("authentication required: pass --token or set JIKKO_TOKEN") }
	return w.SetMetadata(actor, args[0], args[1], args[2])
}

func perm(args []string) error {
	args, dir, token, err := flags("perm", args, nil)
	if err != nil { return err }
	if len(args) < 2 {
		return errors.New("usage: jikko perm [flags] <reference> <read|comment|write|admin> [identity...]")
	}
	w, err := openWorkspace(*dir)
	if err != nil { return err }
	actor, err := actorFor(w, *token)
	if err != nil { return err }
	if actor == "" { return errors.New("authentication required: pass --token or set JIKKO_TOKEN") }
	return w.SetPermission(actor, args[0], args[1], args[2:]...)
}

func auth(args []string) error {
	args, dir, _, err := flags("auth", args, nil)
	if err != nil { return err }
	if len(args) != 2 { return errors.New("usage: jikko auth [flags] <create|revoke> <identity>") }
	w, err := openWorkspace(*dir)
	if err != nil { return err }
	switch args[0] {
	case "create":
		token, err := w.CreateCredential(args[1])
		if err != nil { return err }
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
	if err != nil { return err }
	if len(args) != 0 { return errors.New("usage: jikko check [flags]") }
	w, err := jikko.Open(*dir)
	if err != nil { return err }
	problems := append([]jikko.Problem(nil), w.Problems...)
	for _, p := range w.List("", "") {
		for _, ref := range append(append([]string{}, p.Links...), p.Embeds...) {
			if _, ok := w.Resolve(ref); !ok {
				problems = append(problems, jikko.Problem{Path: p.Path, Kind: "reference", Message: fmt.Sprintf("unresolved %q", ref)})
			}
		}
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Pages    int             `json:"pages"`
			Problems []jikko.Problem `json:"problems"`
		}{len(w.Pages), problems}); err != nil {
			return err
		}
	} else {
		for _, p := range problems { fmt.Printf("%s: [%s] %s\n", p.Path, p.Kind, p.Message) }
	}
	if len(problems) > 0 { return fmt.Errorf("%d problem(s) in %d pages", len(problems), len(w.Pages)) }
	if !*asJSON { fmt.Printf("ok: %d pages\n", len(w.Pages)) }
	return nil
}
