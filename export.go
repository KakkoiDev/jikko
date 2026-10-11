package jikko

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"os"
	"strings"
)

// ExportZIP returns a portable snapshot of the workspace source and assets.
// Runtime internals, Git history and credentials are deliberately excluded, as
// are symbolic links, other non-regular files, and whatever the workspace's
// ignore files exclude.
func (w *Workspace) ExportZIP() ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	err := WalkFiles(w.Root, func(full, rel string, d fs.DirEntry) error {
		// A .git file marks a worktree or submodule and is Git internals too.
		if strings.EqualFold(d.Name(), ".auth.md") || d.Name() == ".git" {
			return nil
		}
		// Only regular files are source. Following a symbolic link would copy
		// whatever it points at -- outside the root, or the credential file --
		// into a portable archive.
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.HasPrefix(rel, "../") {
			return fs.ErrInvalid
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Method = zip.Deflate
		dst, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		src, err := os.Open(full)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
