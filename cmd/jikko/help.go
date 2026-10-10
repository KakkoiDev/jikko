package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

// synopsis describes one command for `jikko help`, usage errors, and the
// command listing.
type synopsis struct {
	// use lists the forms of the command, without the leading "jikko".
	use []string
	// short is the one-line description of the command listing.
	short string
	// long adds detail under the usage lines of `jikko help <command>`.
	long string
}

// synopses documents every command. A command missing here has no help, so
// the tests insist on one per command.
var synopses = map[string]synopsis{
	"init": {
		use:   []string{"init [--dir D] [--identity NAME] [--no-git] [--json]"},
		short: "bootstrap a workspace: Git, .gitignore, an identity, a token, a welcome page",
		long: `Creates the directory if needed, runs git init unless --no-git is given or
the directory is already in a repository, ignores .auth.md and .data/,
writes an individual identity page, a welcome page, and an open-work view,
and prints a token for the identity on standard output. Existing files are
never overwritten, so init can be run again to complete a workspace.
--identity defaults to your user name.`,
	},
	"list": {
		use:   []string{"list [--type T] [--status S] [--assignee ID|me] [--json]"},
		short: "list pages visible to you",
		long:  "--assignee me needs a token and lists the pages assigned to its identity, directly or through a group.",
	},
	"show": {
		use:   []string{"show <reference>... [--json]", "show --raw <reference>"},
		short: "print one or more pages (batch reads)",
		long:  "--raw prints one page's exact source for editing with `jikko save`; its rev goes to standard error.",
	},
	"tree": {
		use:   []string{"tree [--json]"},
		short: "list the permission-filtered workspace tree",
	},
	"view": {
		use:   []string{"view <view> [--json]"},
		short: "evaluate a view: filter, sort, and group readable pages",
	},
	"mentions": {
		use:   []string{"mentions [--since REV] [--json]"},
		short: "list pages addressing the authenticated identity, with the matching lines",
		long: `Each match names the page, the line of its source, and the comment thread it
is in, if any. --since REV limits the pages to those changed since a Git
revision; a workspace without Git lists every mention and says so.`,
	},
	"log": {
		use:   []string{"log [page] [--limit N] [--json]"},
		short: "show Git history with Jikko-Actor and Jikko-Operation trailers",
		long:  "A page's history follows it across renames. Paths of pages you may not read are omitted.",
	},
	"create": {
		use:   []string{"create <path> [--type document|task|view|identity] [--body MD | --file PATH|-] [--commit] [--json]"},
		short: "create a Markdown document, task, view, or identity",
		long: `--file - reads the body from standard input. A task gets status: todo
unless the body's frontmatter sets one; a view's body is its frontmatter.`,
	},
	"upload": {
		use:   []string{"upload <file> [--into PAGE] [--as PATH] [--max-upload SIZE] [--commit] [--json]"},
		short: "add a file to the workspace, optionally embedding it in a page",
	},
	"commit": {
		use:   []string{"commit <message>... [--operation NAME] [--json]"},
		short: "record every current change with actor-attributed Git audit trailers",
		long:  "A tracked .auth.md is never staged. Mutating commands take --commit to record just their own change.",
	},
	"export": {
		use:   []string{"export [--output FILE]"},
		short: "export a portable workspace ZIP",
	},
	"save": {
		use: []string{
			"save <reference> --rev REV [--file PATH|-] [--base PATH] [--commit] [--json]",
			"save <reference> --rev REV --against REV --resolve ID=current|yours ...",
		},
		short: "save an edited page, merging concurrent changes",
	},
	"set": {
		use:   []string{"set <reference> <property> <value> [--commit]"},
		short: "set a metadata property",
		long:  "The value is read as YAML. permissions is a policy: change it with `jikko perm`.",
	},
	"perm": {
		use:   []string{"perm <reference> <read|comment|write|admin> [identity...] [--commit]"},
		short: "grant or clear a capability on a page",
	},
	"rename": {
		use:   []string{"rename <reference> <new path> [--no-rewrite] [--commit] [--json]"},
		short: "move a page and rewrite every reference to it",
	},
	"delete": {
		use:   []string{"delete <reference> [--prune] [--commit] [--json]"},
		short: "delete a page, reporting references it breaks",
	},
	"comment": {
		use: []string{
			"comment add <reference> <anchor text> <message> [--commit] [--json]",
			"comment reply <reference> <id> <message> [--commit] [--json]",
			"comment resolve <reference> <id> [--commit] [--json]",
			"comment list [reference] [--json]",
		},
		short: "add, reply to, resolve, or list inline comments",
	},
	"auth": {
		use: []string{
			"auth create <identity>",
			"auth list [--json]",
			"auth revoke <identity> [--id ID]",
		},
		short: "create, list, or revoke credentials",
		long:  "revoke without --id removes every credential of the identity. Ids come from `jikko auth list`.",
	},
	"check": {
		use:   []string{"check [--json]"},
		short: "report workspace problems and broken references",
	},
	"serve": {
		use:   []string{"serve [--addr HOST:PORT] [--max-upload SIZE] [--behind-proxy] [--auto-commit]"},
		short: "serve the workspace over HTTP",
		long:  "--auto-commit records every change made through the browser as an attributed Git commit.",
	},
	"version": {
		use:   []string{"version"},
		short: "print the version",
	},
	"help": {
		use:   []string{"help [command]"},
		short: "show this listing, or one command's usage and flags",
	},
}

// usage prints the command listing.
func usage() {
	printUsage(os.Stdout)
}

func printUsage(out io.Writer) {
	names := make([]string, 0, len(synopses))
	for name := range synopses {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintln(out, "jikko <command> [flags]\n\nCommands:")
	for _, name := range names {
		fmt.Fprintf(out, "  %-9s %s\n", name, synopses[name].short)
	}
	fmt.Fprintln(out, `
Every command takes --dir (default: the current directory). Commands that
act as an identity take --token, or read JIKKO_TOKEN. Run "jikko help
<command>" for a command's forms and flags.`)
}

// usageLines renders the "usage:" block of a command.
func usageLines(name string) string {
	s, ok := synopses[name]
	if !ok {
		return "usage: jikko " + name
	}
	lines := make([]string, len(s.use))
	for i, u := range s.use {
		prefix := "       "
		if i == 0 {
			prefix = "usage: "
		}
		lines[i] = prefix + "jikko " + u
	}
	return strings.Join(lines, "\n")
}

// printHelp writes a command's usage, description, and flags.
func printHelp(name string, f *flag.FlagSet, out io.Writer) {
	fmt.Fprintln(out, usageLines(name))
	if s := synopses[name]; s.short != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, strings.ToUpper(s.short[:1])+s.short[1:]+".")
		if s.long != "" {
			fmt.Fprintln(out, s.long)
		}
	}
	if f != nil {
		fmt.Fprintln(out, "\nFlags:")
		f.SetOutput(out)
		f.PrintDefaults()
	}
}

// usageError is a command invoked with the wrong arguments. It exits with
// status 2, like an unknown command.
type usageError struct {
	command string
	detail  string
}

func (e usageError) Error() string {
	if e.detail != "" {
		return e.detail + "\n" + usageLines(e.command)
	}
	return usageLines(e.command)
}

// usageOf reports that a command was given the wrong arguments.
func usageOf(command string) error { return usageError{command: command} }

func help(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		return nil
	}
	if len(args) != 1 {
		return usageOf("help")
	}
	run, ok := commands[args[0]]
	if !ok {
		return usageError{command: "help", detail: fmt.Sprintf("unknown command %q", args[0])}
	}
	if err := run([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		return err
	}
	return nil
}

func printVersion(args []string) error {
	args, _, err := flagsNoToken("version", args, nil)
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("version")
	}
	fmt.Println("jikko " + version)
	return nil
}
