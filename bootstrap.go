package jikko

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// InitOptions adjusts Init.
type InitOptions struct {
	// Identity names the first individual Identity. It must be a bare page
	// name; the page is created at the workspace root.
	Identity string
	// NoGit skips `git init`.
	NoGit bool
}

// InitResult reports what Init did.
type InitResult struct {
	Root string `json:"root"`
	// Identity is the identity's reference and Token its new bearer token,
	// empty when the identity already had a credential.
	Identity string `json:"identity"`
	Token    string `json:"token,omitempty"`
	// Created lists the workspace-relative files Init wrote.
	Created []string `json:"created"`
	// Git reports whether the workspace is Git-backed afterwards, and
	// GitInitialized whether Init made it so.
	Git            bool `json:"git"`
	GitInitialized bool `json:"git_initialized"`
}

var identityNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Init bootstraps a workspace: the directory, a Git repository unless
// disabled or already present, a .gitignore for harness state, an
// individual Identity page with a credential, a welcome page, and an
// open-work view (specification §10 asks that Git initialization be easy).
// It never overwrites: every file that exists is kept, so running it again
// is harmless and completes whatever is missing.
func Init(dir string, opt InitOptions) (*InitResult, error) {
	name := strings.TrimSpace(opt.Identity)
	if !identityNamePattern.MatchString(name) {
		return nil, fmt.Errorf("identity %q must be a page name made of lowercase letters, digits, - and _", opt.Identity)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	} else if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	res := &InitResult{Root: root, Identity: name, Created: []string{}}

	res.Git = GitBacked(root)
	if !res.Git && !opt.NoGit {
		if _, err := exec.LookPath("git"); err != nil {
			return nil, errors.New("git is not installed; pass --no-git for a workspace without history")
		}
		if err := gitRun(root, "init", "--quiet"); err != nil {
			return nil, err
		}
		res.Git, res.GitInitialized = true, true
	}
	if err := ensureIgnored(root, ".auth.md", ".data/"); err != nil {
		return nil, err
	}

	identityPath := name + ".md"
	title := strings.ToUpper(name[:1]) + name[1:]
	created, err := writeIfAbsent(root, identityPath, "---\ntype: identity\n---\n\n# "+title+"\n")
	if err != nil {
		return nil, err
	}
	if created {
		res.Created = append(res.Created, identityPath)
	}
	w, err := Open(root)
	if err != nil {
		return nil, err
	}
	p, ok := w.ResolveIdentity(name)
	if !ok || p.Path != identityPath {
		return nil, fmt.Errorf("%s exists but is not an individual identity page", identityPath)
	}
	if len(p.Members) != 0 {
		return nil, fmt.Errorf("%s is a group; init needs an individual identity", identityPath)
	}

	pages := []struct{ path, content string }{
		{"open-work.md", "---\ntype: view\nfilter:\n  type: task\n  status: [todo, doing]\nview:\n  layout: board\n  group: status\n  sort: due\n---\n"},
		{"welcome.md", welcomePage(name)},
	}
	for _, page := range pages {
		created, err := writeIfAbsent(root, page.path, page.content)
		if err != nil {
			return nil, err
		}
		if created {
			res.Created = append(res.Created, page.path)
		}
	}

	credentials, err := w.Credentials()
	if err != nil {
		return nil, err
	}
	hasToken := false
	for _, c := range credentials {
		if c.Identity == name {
			hasToken = true
		}
	}
	if !hasToken {
		if res.Token, err = w.CreateCredential(name); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func welcomePage(identity string) string {
	return "# Welcome to Jikko\n\n" +
		"This folder is a Jikko workspace: plain Markdown that humans and agents read and change together.\n" +
		"A page is a Document unless its frontmatter says `type: task`, `type: view`, or `type: identity`.\n\n" +
		"- [[" + identity + "]] is you. Mention yourself with @" + identity + "; assign work with `assignee: " + identity + "`.\n" +
		"- [[open-work]] is a view of every task that is still `todo` or `doing`.\n" +
		"- `.auth.md` holds credential hashes and stays out of Git; `.data/` is disposable harness state.\n\n" +
		"Next steps:\n\n" +
		"```sh\n" +
		"jikko create tasks/first --type task --body '# First task'\n" +
		"jikko set tasks/first assignee " + identity + "\n" +
		"jikko view open-work\n" +
		"jikko serve\n" +
		"```\n\n" +
		"Run `jikko help` for every command and `jikko check` whenever the workspace looks wrong.\n"
}

// writeIfAbsent creates a file with content unless it exists.
func writeIfAbsent(root, rel, content string) (bool, error) {
	target := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return false, err
	}
	return true, writeAtomicPerm(target, []byte(content), 0644)
}

// ensureIgnored appends to .gitignore the patterns it does not list yet.
func ensureIgnored(root string, patterns ...string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, pattern := range patterns {
		if !have[pattern] && !have["/"+pattern] && !have[strings.TrimSuffix(pattern, "/")] && !have["/"+strings.TrimSuffix(pattern, "/")] {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	prefix := string(b)
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return os.WriteFile(path, []byte(prefix+strings.Join(missing, "\n")+"\n"), 0644)
}
