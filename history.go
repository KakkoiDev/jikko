package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ErrNoGit reports that a workspace is not inside a Git repository, so
// history, audit, and commit-based features are unavailable (specification
// §10). Such a workspace is still a valid Jikko workspace.
var ErrNoGit = errors.New("workspace is not Git-backed")

// CommitResult reports a recorded commit.
type CommitResult struct {
	// Commit is the full hash of the new commit, empty when nothing changed.
	Commit    string `json:"commit,omitempty"`
	Actor     string `json:"actor"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
	// Paths lists the workspace-relative paths the commit was limited to;
	// nil means the whole workspace was recorded.
	Paths []string `json:"paths,omitempty"`
	// Empty is true when there was nothing to record and no commit was made.
	Empty bool `json:"empty,omitempty"`
}

// HistoryEntry is one commit of a workspace's Git history as `jikko log`
// shows it. Actor and Operation come from the Jikko-Actor and
// Jikko-Operation trailers and are empty for commits made outside Jikko.
type HistoryEntry struct {
	Commit    string   `json:"commit"`
	Date      string   `json:"date"`
	Author    string   `json:"author"`
	Subject   string   `json:"subject"`
	Actor     string   `json:"actor,omitempty"`
	Operation string   `json:"operation,omitempty"`
	Paths     []string `json:"paths,omitempty"`
}

// GitBacked reports whether root lies inside a Git work tree.
func GitBacked(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run() == nil
}

// Commit records every current workspace change in Git with Jikko actor
// attribution. Git remains optional: workspaces without Git are still valid
// Jikko workspaces, and Commit fails with ErrNoGit there.
//
// A tracked .auth.md is never staged: credential hashes do not belong in
// history (identity-permissions.md §3.1).
func (w *Workspace) Commit(actor, message, operation string) error {
	_, err := w.CommitChanges(actor, message, operation, nil)
	return err
}

// CommitChanges records a commit limited to paths, or to the whole workspace
// when paths is nil, as one logical transaction (specification §10). The
// commit carries Jikko-Actor and Jikko-Operation trailers; the Git author
// stays whoever Git is configured for, with a harness identity used only
// where none is configured. Nothing is committed, and Empty is set, when the
// paths hold no change.
func (w *Workspace) CommitChanges(actor, message, operation string, paths []string) (*CommitResult, error) {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return nil, fmt.Errorf("authentication required as an individual identity")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("commit message required")
	}
	if operation == "" {
		operation = "workspace-mutation"
	}
	// The operation is written into the trailer block. A line break would let
	// the caller append a forged Jikko-Actor trailer of its own.
	if strings.ContainsFunc(operation, unicode.IsControl) || strings.ContainsFunc(operation, unicode.IsSpace) {
		return nil, fmt.Errorf("operation %q must be a single word", operation)
	}
	if !GitBacked(w.Root) {
		return nil, ErrNoGit
	}
	res := &CommitResult{Actor: identityRef(person), Operation: operation, Message: message}
	var pathspec []string
	if paths == nil {
		pathspec = []string{"."}
		// An ignored .auth.md is skipped by Git itself; a tracked one must
		// be excluded explicitly, or its hashes would be recorded again.
		if AuthTrackedByGit(w.Root) {
			pathspec = append(pathspec, ":(exclude).auth.md")
		}
	} else {
		stageable, err := w.stageable(paths)
		if err != nil {
			return nil, err
		}
		if len(stageable) == 0 {
			res.Empty = true
			return res, nil
		}
		res.Paths = stageable
		pathspec = stageable
	}
	status, err := gitOut(w.Root, append([]string{"status", "--porcelain", "--untracked-files=all", "--"}, pathspec...)...)
	if err != nil {
		return nil, err
	}
	// Do not create empty audit commits.
	if strings.TrimSpace(status) == "" {
		res.Empty = true
		return res, nil
	}
	if err := gitRun(w.Root, append([]string{"add", "-A", "--"}, pathspec...)...); err != nil {
		return nil, err
	}
	args := append(gitIdentity(w.Root), "-C", w.Root, "commit", "--quiet", "-m", message,
		"-m", "Jikko-Actor: "+res.Actor+"\nJikko-Operation: "+operation)
	if paths != nil {
		// --only commits exactly these paths, leaving anything else the
		// caller had staged in the index for a later commit.
		args = append(append(args, "--only", "--"), pathspec...)
	}
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	hash, err := gitOut(w.Root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	res.Commit = strings.TrimSpace(hash)
	return res, nil
}

// stageable narrows the paths a mutation touched to those Git can stage: a
// file on disk, or a tracked file the mutation removed. The credential file
// and anything outside the workspace are never staged.
func (w *Workspace) stageable(paths []string) ([]string, error) {
	var candidates []string
	for _, p := range paths {
		p = path.Clean(filepath.ToSlash(p))
		if p == "." || p == "" || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || reservedPath(p) {
			continue
		}
		candidates = append(candidates, p)
	}
	sort.Strings(candidates)
	candidates = dedupe(candidates)
	if len(candidates) == 0 {
		return nil, nil
	}
	tracked, err := gitOut(w.Root, append([]string{"ls-files", "-z", "--"}, candidates...)...)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, t := range strings.Split(tracked, "\x00") {
		if t != "" {
			known[t] = true
		}
	}
	var out []string
	for _, p := range candidates {
		if _, err := os.Lstat(filepath.Join(w.Root, filepath.FromSlash(p))); err == nil || known[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

// gitIdentity returns the -c options that give a commit an author where Git
// has none configured. A configured identity is never overridden: the actor
// is carried by the Jikko-Actor trailer, not by impersonating the author.
func gitIdentity(root string) []string {
	if os.Getenv("GIT_AUTHOR_NAME") != "" && os.Getenv("GIT_AUTHOR_EMAIL") != "" {
		return nil
	}
	name, errName := gitOut(root, "config", "--get", "user.name")
	email, errEmail := gitOut(root, "config", "--get", "user.email")
	if errName == nil && errEmail == nil && strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
		return nil
	}
	return []string{"-c", "user.name=Jikko", "-c", "user.email=jikko@local"}
}

// Log returns the workspace's Git history, newest first, limited to the
// commits that touched one page when ref is given. A page is followed across
// renames. The paths of each commit are limited to files the actor may read.
// limit bounds the number of entries; zero means no bound.
func (w *Workspace) Log(actor, ref string, limit int) ([]HistoryEntry, error) {
	if !GitBacked(w.Root) {
		return nil, ErrNoGit
	}
	args := []string{"log", "--format=%x1e%H%x1f%aI%x1f%an%x1f%s%x1f%(trailers:key=Jikko-Actor,valueonly)%x1f%(trailers:key=Jikko-Operation,valueonly)%x1f", "--name-only", "--no-renames", "--relative"}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	if ref != "" {
		p, err := w.readable(actor, ref)
		if err != nil {
			return nil, err
		}
		args = append(args, "--follow", "--", p.Path)
	} else {
		args = append(args, "--", ".")
	}
	out, err := gitOut(w.Root, args...)
	if err != nil {
		// A repository without a commit yet has no history, not an error.
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "unknown revision") {
			return []HistoryEntry{}, nil
		}
		return nil, err
	}
	entries := []HistoryEntry{}
	for _, record := range strings.Split(out, "\x1e") {
		fields := strings.SplitN(record, "\x1f", 7)
		if len(fields) != 7 {
			continue
		}
		e := HistoryEntry{
			Commit:    strings.TrimSpace(fields[0]),
			Date:      fields[1],
			Author:    fields[2],
			Subject:   fields[3],
			Actor:     strings.TrimSpace(fields[4]),
			Operation: strings.TrimSpace(fields[5]),
		}
		for _, line := range strings.Split(fields[6], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if p, exists := w.Pages[line]; exists && !w.Allowed(actor, p, Read) {
				continue
			}
			e.Paths = append(e.Paths, line)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ChangedSince lists the workspace-relative paths that differ from rev:
// files changed or added in the working tree since that commit, tracked or
// not. It fails with ErrNoGit outside Git and with an error for an unknown
// revision.
func (w *Workspace) ChangedSince(rev string) ([]string, error) {
	if !GitBacked(w.Root) {
		return nil, ErrNoGit
	}
	rev = strings.TrimSpace(rev)
	if rev == "" || strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("invalid revision %q", rev)
	}
	if err := gitRun(w.Root, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
		return nil, fmt.Errorf("unknown revision %q", rev)
	}
	changed, err := gitOut(w.Root, "diff", "--name-only", "--relative", "-z", rev, "--", ".")
	if err != nil {
		return nil, err
	}
	untracked, err := gitOut(w.Root, "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range strings.Split(changed+untracked, "\x00") {
		if p != "" {
			out = append(out, filepath.ToSlash(p))
		}
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func gitRun(root string, args ...string) error {
	_, err := gitOut(root, args...)
	return err
}

func gitOut(root string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
