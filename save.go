package jikko

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// SaveOptions adjusts SaveSource.
type SaveOptions struct {
	// Base is the source of the base revision as the caller read it. It is
	// used only when its fingerprint is that revision, so a caller cannot
	// pass off other text as the base.
	Base []byte
	// Resolve settles conflicts by id ("field:<key>", "body:<n>") with
	// TakeCurrent or TakeYours. Conflict ids are only meaningful for the
	// page as it was when they were reported, so Resolve applies only while
	// the page is still at revision ResolveRev; otherwise the conflicts are
	// reported afresh.
	Resolve    map[string]string
	ResolveRev string
}

// SaveResult reports a saved edit.
type SaveResult struct {
	Path string `json:"path"`
	// Rev is the page's revision after the save.
	Rev string `json:"rev"`
	// Merged is true when the page had changed since the base revision and
	// the edit was combined with that change by a three-way merge.
	Merged bool `json:"merged"`
}

// historyDepth bounds how far back Git history is searched for a base
// revision.
const historyDepth = 200

// Source returns a page's exact source and revision, for a caller that will
// edit it. It needs read.
func (w *Workspace) Source(actor, ref string) ([]byte, *Page, error) {
	p, err := w.readable(actor, ref)
	if err != nil {
		return nil, nil, err
	}
	raw, err := os.ReadFile(w.pagePath(p))
	if err != nil {
		return nil, nil, err
	}
	if fingerprint(raw) != p.Rev {
		return nil, nil, fmt.Errorf("%s changed on disk after it was read; re-read the workspace and retry", p.Path)
	}
	return raw, p, nil
}

func (w *Workspace) pagePath(p *Page) string {
	return filepath.Join(w.Root, filepath.FromSlash(p.Path))
}

// SaveSource replaces a page's whole source with yours, an edit of revision
// baseRev (specification §11). If the page still holds baseRev, yours is
// saved. If it has changed since, the base revision is looked up -- in
// opt.Base, then in the workspace's Git history -- and yours is merged with
// the current page three ways: frontmatter key by key, the body line by
// line. A clean merge is saved; overlapping changes return a *ConflictError
// with the structured conflict and write nothing. Without a base revision,
// every difference from the current page is a conflict.
//
// Saving needs write, and admin when the access policy changes. The result
// is staged and judged like any other mutation.
func (w *Workspace) SaveSource(actor, ref, baseRev string, yours []byte, opt SaveOptions) (*SaveResult, error) {
	if strings.TrimSpace(baseRev) == "" {
		return nil, errors.New("base revision required: send the rev you read")
	}
	defer w.lock()()
	cur, err := Open(w.Root)
	if err != nil {
		return nil, err
	}
	actor, err = cur.individual(actor)
	if err != nil {
		return nil, err
	}
	p, err := cur.readable(actor, ref)
	if err != nil {
		return nil, err
	}
	if !cur.Allowed(actor, p, Write) {
		return nil, fmt.Errorf("%s lacks write permission on %s", actor, p.Path)
	}
	target, err := cur.safePath(p)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	currentRev := fingerprint(raw)
	yours = matchLineEndings(yours, raw)
	res := &SaveResult{Path: p.Path, Rev: currentRev}
	next := yours
	if currentRev != baseRev {
		if bytes.Equal(yours, raw) {
			return res, nil
		}
		conflict := &Conflict{}
		base, known := cur.baseSource(p.Path, baseRev, opt.Base)
		var resolve map[string]string
		if opt.ResolveRev == currentRev {
			resolve = opt.Resolve
		}
		if known {
			merged, c, err := merge3(base, raw, yours, resolve)
			if err != nil {
				return nil, err
			}
			next, conflict = merged, c
		} else {
			conflict = conflict2(raw, yours)
		}
		if conflict != nil {
			conflict.Path, conflict.BaseRev, conflict.CurrentRev, conflict.BaseKnown = p.Path, baseRev, currentRev, known
			return nil, &ConflictError{Conflict: *conflict}
		}
		res.Merged = true
	}
	if bytes.Equal(next, raw) {
		return res, nil
	}
	need := Write
	if policyChanged(p.Path, raw, next) {
		need = Admin
		if !cur.Allowed(actor, p, Admin) {
			return nil, fmt.Errorf("%s lacks admin permission on %s, which changing its permissions needs", actor, p.Path)
		}
	}
	err = w.stage(actor, "edit", writes(map[string][]byte{p.Path: next}), func(current *Workspace) error {
		if now, ok := current.Pages[p.Path]; !ok || now.Rev != currentRev {
			return fmt.Errorf("%s changed on disk after it was read; retry", p.Path)
		} else if !current.Allowed(actor, now, need) {
			return fmt.Errorf("%s lacks %s permission on %s", actor, need, p.Path)
		}
		return nil
	}, func() error { return writeAtomic(target, next) })
	if err != nil {
		return nil, err
	}
	res.Rev = fingerprint(next)
	return res, nil
}

// matchLineEndings converts yours to the line endings of current. A browser
// submits every textarea with CRLF line breaks, and an agent may not know
// which the file uses; neither should rewrite every line of the page.
func matchLineEndings(yours, current []byte) []byte {
	lf := bytes.ReplaceAll(yours, []byte("\r\n"), []byte("\n"))
	if usesCRLF(current) {
		return bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	}
	return lf
}

// policyChanged reports whether two sources of one page carry different
// access policies.
func policyChanged(pagePath string, a, b []byte) bool {
	pa, _ := parseSource(pagePath, a, false)
	pb, _ := parseSource(pagePath, b, false)
	return pa.Restricted != pb.Restricted || pa.aclUsable != pb.aclUsable ||
		!reflect.DeepEqual(pa.Metadata["permissions"], pb.Metadata["permissions"])
}

// baseSource finds the source of revision rev of a page: the caller's copy
// if it is that revision (allowing for line endings a form changed), or a
// version in Git history.
func (w *Workspace) baseSource(pagePath, rev string, hint []byte) ([]byte, bool) {
	if hint != nil {
		lf := bytes.ReplaceAll(hint, []byte("\r\n"), []byte("\n"))
		for _, candidate := range [][]byte{hint, lf, bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))} {
			if fingerprint(candidate) == rev {
				return candidate, true
			}
		}
	}
	return w.historicalSource(pagePath, rev)
}

// historicalSource searches the page's Git history -- the index and the most
// recent commits that touched it -- for the version whose fingerprint is
// rev. It finds nothing where Git is unavailable.
func (w *Workspace) historicalSource(pagePath, rev string) ([]byte, bool) {
	prefix, err := exec.Command("git", "-C", w.Root, "rev-parse", "--show-prefix").Output()
	if err != nil {
		return nil, false
	}
	repoPath := strings.TrimSpace(string(prefix)) + pagePath
	commits, err := exec.Command("git", "-C", w.Root, "log", "--format=%H", "-n", strconv.Itoa(historyDepth), "--", pagePath).Output()
	if err != nil {
		return nil, false
	}
	objects := []string{":" + repoPath}
	for _, c := range strings.Fields(string(commits)) {
		objects = append(objects, c+":"+repoPath)
	}
	cmd := exec.Command("git", "-C", w.Root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(objects, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, false
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			continue // "<object> missing"
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, false
		}
		content := make([]byte, size+1)
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, false
		}
		content = content[:size]
		if fields[1] == "blob" && fingerprint(content) == rev {
			return content, true
		}
	}
}
