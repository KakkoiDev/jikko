package jikko

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExportZIPIsPortableAndExcludesRuntimeSecrets(t *testing.T) {
	root := t.TempDir()
	must := func(name, body string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	must("notes/a.md", "# A")
	must("assets/map.txt", "map")
	must(".auth.md", "secret")
	must(".data/index", "derived")
	must(".git/config", "git")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := w.ExportZIP()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if !got["notes/a.md"] || !got["assets/map.txt"] {
		t.Fatalf("missing source/assets: %v", got)
	}
	for _, bad := range []string{".auth.md", ".data/index", ".git/config"} {
		if got[bad] {
			t.Fatalf("export leaked %s", bad)
		}
	}
}
