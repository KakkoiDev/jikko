package jikko

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Commit records current workspace changes in Git with Jikko actor attribution.
// Git remains optional: workspaces without Git are still valid Jikko workspaces.
func (w *Workspace) Commit(actor, message, operation string) error {
	person, ok := w.ResolveIdentity(actor)
	if !ok || len(person.Members) != 0 {
		return fmt.Errorf("authentication required as an individual identity")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("commit message required")
	}
	if operation == "" {
		operation = "workspace-mutation"
	}
	// The operation is written into the trailer block. A line break would let
	// the caller append a forged Jikko-Actor trailer of its own.
	if strings.ContainsFunc(operation, unicode.IsControl) || strings.ContainsFunc(operation, unicode.IsSpace) {
		return fmt.Errorf("operation %q must be a single word", operation)
	}
	if err := gitRun(w.Root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("workspace is not Git-backed: %w", err)
	}
	if err := gitRun(w.Root, "add", "--", "."); err != nil {
		return err
	}
	// Do not create empty audit commits.
	check := exec.Command("git", "-C", w.Root, "diff", "--cached", "--quiet", "--exit-code")
	if err := check.Run(); err == nil {
		return nil
	}
	args := []string{"-c", "user.name=Jikko", "-c", "user.email=jikko@local", "-C", w.Root,
		"commit", "-m", message,
		"-m", "Jikko-Actor: " + strings.TrimSuffix(person.Path, ".md") + "\nJikko-Operation: " + operation}
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func gitRun(root string, args ...string) error {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Commit is one entry of the workspace's Git history as Jikko reads it: the
// audit trailers are parsed back so a caller can ask who acted and how.
type Commit struct {
	Hash      string    `json:"hash"`
	Date      time.Time `json:"date"`
	Subject   string    `json:"subject"`
	Actor     string    `json:"actor,omitempty"`
	Operation string    `json:"operation,omitempty"`
}

// History returns the newest commits, at most limit, or nothing when the
// workspace is not Git-backed. Commits made outside Jikko have no trailers
// and an empty Actor.
func (w *Workspace) History(limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 20
	}
	if err := gitRun(w.Root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, nil
	}
	cmd := exec.Command("git", "-C", w.Root, "log", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%aI%x1f%s%x1f%(trailers:key=Jikko-Actor,valueonly,separator=%x20)%x1f%(trailers:key=Jikko-Operation,valueonly,separator=%x20)%x1e")
	out, err := cmd.Output()
	if err != nil {
		// An empty repository has no HEAD; that is a history of nothing.
		return nil, nil
	}
	var commits []Commit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 3 {
			continue
		}
		c := Commit{Hash: f[0], Subject: f[2]}
		c.Date, _ = time.Parse(time.RFC3339, f[1])
		if len(f) > 3 {
			c.Actor = strings.TrimSpace(f[3])
		}
		if len(f) > 4 {
			c.Operation = strings.TrimSpace(f[4])
		}
		commits = append(commits, c)
	}
	return commits, nil
}
