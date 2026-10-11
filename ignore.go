package jikko

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// A workspace is walked the same way everywhere -- to open it, to stamp it
// for change detection, to index its files and to export it -- and the walk
// honours the same exclusions: Git internals and derived .data (harness
// state), node_modules (never workspace source, and often enormous), and
// whatever .gitignore and .jikkoignore files exclude, in the directory
// they sit in and below, with Git's pattern rules. .jikkoignore is for
// files Git tracks that are still not pages: a vendored docs tree, say.

// IgnoreFiles are the files whose patterns exclude paths from a workspace.
var IgnoreFiles = []string{".gitignore", ".jikkoignore"}

// skippedDirs are never entered, whatever the ignore files say.
var skippedDirs = map[string]bool{".git": true, ".data": true, "node_modules": true}

// WalkFiles calls fn for every file of the workspace that is not excluded,
// in lexical order, with its path on disk, its slash-separated path from
// the root, and its directory entry. Directories are not reported. An
// error from fn stops the walk, as with filepath.WalkDir.
func WalkFiles(root string, fn func(full, rel string, d fs.DirEntry) error) error {
	rules := ignoreRules{}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				rules.load(p, "")
				return nil
			}
			if skippedDirs[d.Name()] || rules.excluded(rel, true) {
				return filepath.SkipDir
			}
			rules.load(p, rel)
			return nil
		}
		if rules.excluded(rel, false) {
			return nil
		}
		return fn(p, rel, d)
	})
}

// ignoreRules holds the patterns read so far, by the directory they came
// from, so a path is judged by every ignore file above it.
type ignoreRules struct {
	sets []ruleSet
}

type ruleSet struct {
	dir   string // slash-separated path from the root; "" for the root
	rules []ignoreRule
}

type ignoreRule struct {
	match   *regexp.Regexp
	negate  bool
	dirOnly bool
}

// load reads the ignore files of a directory, if any.
func (r *ignoreRules) load(full, rel string) {
	var rules []ignoreRule
	for _, name := range IgnoreFiles {
		f, err := os.Open(filepath.Join(full, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if rule, ok := parseIgnoreLine(scanner.Text()); ok {
				rules = append(rules, rule)
			}
		}
		f.Close()
	}
	if len(rules) > 0 {
		r.sets = append(r.sets, ruleSet{dir: rel, rules: rules})
	}
}

// excluded reports whether a path is ignored: the last matching pattern
// decides, deeper ignore files after shallower ones.
func (r *ignoreRules) excluded(rel string, isDir bool) bool {
	ignored := false
	for _, set := range r.sets {
		sub := rel
		if set.dir != "" {
			if !strings.HasPrefix(rel, set.dir+"/") {
				continue
			}
			sub = rel[len(set.dir)+1:]
		}
		for _, rule := range set.rules {
			if rule.dirOnly && !isDir {
				continue
			}
			if rule.match.MatchString(sub) {
				ignored = !rule.negate
			}
		}
	}
	return ignored
}

// parseIgnoreLine compiles one line of an ignore file. It follows
// gitignore: comments and blank lines are skipped, "!" negates, a trailing
// "/" matches directories only, a pattern with a slash elsewhere is
// relative to the ignore file's directory and one without matches a name
// at any depth, "*" and "?" stop at slashes, "**" crosses them.
func parseIgnoreLine(line string) (ignoreRule, bool) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	rule := ignoreRule{}
	if strings.HasPrefix(line, "!") {
		rule.negate = true
		line = line[1:]
	}
	line = strings.TrimPrefix(line, "\\")
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	anchored := strings.HasPrefix(line, "/") || strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return ignoreRule{}, false
	}
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("(?:^|/)")
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '*' && strings.HasPrefix(line[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(line[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(line[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := line[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteString(regexp.QuoteMeta(string(line[i])))
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// A pattern for a directory also covers everything inside it.
	b.WriteString("(?:/.*)?$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return ignoreRule{}, false
	}
	rule.match = re
	return rule, true
}

// Ignored reports whether a workspace-relative path is excluded from the
// workspace: by a skipped directory on its way or by an ignore pattern.
// It walks nothing; it reads the ignore files above the path.
func Ignored(root, rel string) bool {
	rel = strings.TrimPrefix(path.Clean(filepath.ToSlash(rel)), "./")
	rules := ignoreRules{}
	rules.load(root, "")
	parts := strings.Split(rel, "/")
	for i := 0; i < len(parts)-1; i++ {
		dir := strings.Join(parts[:i+1], "/")
		if skippedDirs[parts[i]] || rules.excluded(dir, true) {
			return true
		}
		rules.load(filepath.Join(root, filepath.FromSlash(dir)), dir)
	}
	return rules.excluded(rel, false)
}
