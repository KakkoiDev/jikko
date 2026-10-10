package jikko

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DefaultMaxUpload is the default maximum upload size. Ordinary Git history
// copes badly with large binaries: GitHub warns about files over 50 MB and
// refuses files over 100 MB, so the default stays well below both while
// covering screenshots, diagrams, PDFs, and short recordings
// (specification §12).
const DefaultMaxUpload = 25 << 20

// UploadOptions adjusts Upload.
type UploadOptions struct {
	// Path is where the file goes, relative to the workspace. By default it
	// is the file's name in the folder of the page it is embedded into, or at
	// the workspace root.
	Path string
	// Into names a page to embed the file into: `![[file]]` is appended to
	// its body in the same operation.
	Into string
	// MaxBytes is the maximum upload size; zero means DefaultMaxUpload.
	MaxBytes int64
}

// UploadResult reports an upload.
type UploadResult struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Into and Embed are set when the file was embedded into a page.
	Into  string `json:"into,omitempty"`
	Embed string `json:"embed,omitempty"`
}

// Upload adds a file to the workspace as an ordinary workspace file
// (specification §12), optionally embedding it into a page as one operation.
// It needs an authenticated individual, and write on the page it embeds
// into. A file larger than the maximum upload size is refused, as are a
// destination that exists, a Markdown destination (create pages with
// CreatePage), a dotfile, harness state, and any path outside the workspace.
func (w *Workspace) Upload(actor, name string, data []byte, opt UploadOptions) (*UploadResult, error) {
	limit := opt.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxUpload
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is %d bytes; the maximum upload size is %d bytes", path.Base(filepath.ToSlash(name)), len(data), limit)
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
	var into *Page
	if opt.Into != "" {
		if into, err = cur.readable(actor, opt.Into); err != nil {
			return nil, err
		}
		if into.Kind == View {
			return nil, errors.New("views have no body to embed into")
		}
		if !cur.Allowed(actor, into, Write) {
			return nil, fmt.Errorf("%s lacks write permission on %s", actor, into.Path)
		}
	}
	dest := opt.Path
	if dest == "" {
		base := path.Base(filepath.ToSlash(strings.TrimSpace(name)))
		dest = base
		if into != nil {
			dest = path.Join(path.Dir(into.Path), base)
		}
	}
	rel, target, err := cur.newFilePath(dest)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, fmt.Errorf("%s already exists; choose another name", rel)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	res := &UploadResult{Path: rel, Size: int64(len(data))}
	writeFile := func() error {
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(target)
			return err
		}
		return f.Close()
	}
	if into == nil {
		if err := writeFile(); err != nil {
			return nil, err
		}
		w.adopt(cur)
		return res, nil
	}
	page, err := cur.safePath(into)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(page)
	if err != nil {
		return nil, err
	}
	if fingerprint(raw) != into.Rev {
		return nil, fmt.Errorf("%s changed on disk after it was read; retry", into.Path)
	}
	idx, err := cur.assets()
	if err != nil {
		return nil, err
	}
	res.Embed = rel
	// A bare name is enough when nothing else answers to it.
	if _, page := cur.Resolve(path.Base(rel)); len(idx.byBase[path.Base(rel)]) == 0 && !page {
		res.Embed = path.Base(rel)
	}
	res.Into = into.Path
	next := appendEmbed(raw, res.Embed)
	err = w.stage(actor, "upload", writes(map[string][]byte{into.Path: next}), func(current *Workspace) error {
		if now, ok := current.Pages[into.Path]; !ok || now.Rev != into.Rev {
			return fmt.Errorf("%s changed on disk after it was read; retry", into.Path)
		} else if !current.Allowed(actor, now, Write) {
			return fmt.Errorf("%s lacks write permission on %s", actor, into.Path)
		}
		return nil
	}, func() error {
		if err := writeFile(); err != nil {
			return err
		}
		if err := writeAtomic(page, next); err != nil {
			os.Remove(target)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// appendEmbed adds `![[ref]]` as a paragraph of its own at the end of a page.
func appendEmbed(raw []byte, ref string) []byte {
	nl := "\n"
	if usesCRLF(raw) {
		nl = "\r\n"
	}
	out := append([]byte(nil), raw...)
	if len(bytes.TrimSpace(out)) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, nl...)
		}
		if !bytes.HasSuffix(bytes.TrimSuffix(out, []byte(nl)), []byte("\n")) {
			out = append(out, nl...)
		}
	}
	return append(out, "![["+ref+"]]"+nl...)
}

// newFilePath validates where a non-Markdown workspace file may be written,
// with the same containment rules as page creation.
func (w *Workspace) newFilePath(p string) (rel, target string, err error) {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" || strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", "", errors.New("file path must stay inside the workspace")
	}
	rel = path.Clean(p)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", errors.New("file path must stay inside the workspace")
	}
	if strings.EqualFold(path.Ext(rel), ".md") {
		return "", "", errors.New("Markdown pages are created with `jikko create`, not uploaded")
	}
	if reservedPath(rel) {
		return "", "", fmt.Errorf("%s is reserved for the harness and is not workspace source", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return "", "", fmt.Errorf("%s: hidden files are not workspace source", rel)
		}
		if strings.ContainsAny(part, "[]|") {
			return "", "", fmt.Errorf("%s: a file name with [, ], or | cannot be referenced as ![[file]]", rel)
		}
	}
	target = filepath.Join(w.Root, filepath.FromSlash(rel))
	if err := w.containedDir(filepath.Dir(target)); err != nil {
		return "", "", err
	}
	return rel, target, nil
}
