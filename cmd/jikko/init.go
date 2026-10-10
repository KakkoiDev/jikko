package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"

	jikko "github.com/KakkoiDev/jikko"
)

func initWorkspace(args []string) error {
	var identity *string
	var noGit, asJSON *bool
	args, dir, err := flagsNoToken("init", args, func(f *flag.FlagSet) {
		identity = f.String("identity", "", "name of your identity page (default: your user name)")
		noGit = f.Bool("no-git", false, "do not run git init")
		asJSON = f.Bool("json", false, "JSON output")
	})
	if err != nil {
		return err
	}
	if len(args) != 0 {
		return usageOf("init")
	}
	name := *identity
	if name == "" {
		name = defaultIdentity()
	}
	res, err := jikko.Init(*dir, jikko.InitOptions{Identity: name, NoGit: *noGit})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	switch {
	case res.GitInitialized:
		fmt.Fprintf(os.Stderr, "jikko: initialized %s as a Git repository\n", res.Root)
	case res.Git:
		fmt.Fprintf(os.Stderr, "jikko: %s is already Git-backed\n", res.Root)
	default:
		fmt.Fprintf(os.Stderr, "jikko: %s is not Git-backed; history, audit, and three-way merge are unavailable\n", res.Root)
	}
	if len(res.Created) > 0 {
		fmt.Fprintf(os.Stderr, "jikko: created %s\n", strings.Join(res.Created, ", "))
	}
	if res.Token == "" {
		fmt.Fprintf(os.Stderr, "jikko: %s already has a credential; run `jikko auth create %s` for another\n", res.Identity, res.Identity)
		return nil
	}
	fmt.Fprintf(os.Stderr, "jikko: token for %s, shown once; keep it as JIKKO_TOKEN:\n", res.Identity)
	fmt.Println(res.Token)
	return nil
}

// defaultIdentity derives an identity page name from the current user name,
// keeping only what a page name may hold.
func defaultIdentity() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:] // DOMAIN\user
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	clean := strings.Trim(b.String(), "-_")
	if clean == "" {
		return "me"
	}
	return clean
}
