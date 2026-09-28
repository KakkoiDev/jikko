package jikko

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ExportZIP returns a portable snapshot of the workspace source and assets.
// Runtime internals, Git history and credentials are deliberately excluded.
func (w *Workspace) ExportZIP() ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	err := filepath.WalkDir(w.Root, func(full string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		rel, err := filepath.Rel(w.Root, full)
		if err != nil { return err }
		rel = filepath.ToSlash(rel)
		if rel == "." { return nil }
		if d.IsDir() {
			if d.Name()==".git" || d.Name()==".data" { return filepath.SkipDir }
			return nil
		}
		if d.Name()==".auth.md" { return nil }
		if strings.HasPrefix(rel, "../") { return fs.ErrInvalid }
		info, err := d.Info(); if err != nil { return err }
		h, err := zip.FileInfoHeader(info); if err != nil { return err }
		h.Name=rel; h.Method=zip.Deflate
		dst, err := zw.CreateHeader(h); if err != nil { return err }
		src, err := os.Open(full); if err != nil { return err }
		_, copyErr := io.Copy(dst,src); closeErr:=src.Close()
		if copyErr!=nil{return copyErr}; return closeErr
	})
	if err!=nil { _=zw.Close(); return nil,err }
	if err:=zw.Close();err!=nil{return nil,err}
	return out.Bytes(),nil
}
