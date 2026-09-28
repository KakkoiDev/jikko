package jikko

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
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
	if err := gitRun(w.Root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("workspace is not Git-backed: %w", err)
	}
	if err := gitRun(w.Root, "add", "--", "."); err != nil { return err }
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
