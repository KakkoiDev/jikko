package jikko

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// A symbolic link used to be exported with the bytes of whatever it pointed at,
// so a link to a file outside the root, or to .auth.md, leaked it into the
// portable archive.
func TestExportZIPSkipsSymlinksAndHarnessState(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.md":         "# A\n",
		".auth.md":     "---\ncredentials: []\n---\n",
		"sub/.AUTH.md": "hash",
		"mod/.git":     "gitdir: ../.git/modules/mod\n",
		"mod/b.md":     "# B\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"leak.txt": filepath.Join(outside, "secret.txt"),
		"creds.md": filepath.Join(root, ".auth.md"),
		"outdir":   outside,
		"dangling": filepath.Join(outside, "missing"),
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
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
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if !f.Mode().IsRegular() {
			t.Errorf("%s exported with mode %v", f.Name, f.Mode())
		}
		if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "..") {
			t.Errorf("unsafe entry name %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(b), "TOPSECRET") || strings.Contains(string(b), "credentials") {
			t.Errorf("%s leaked %q", f.Name, b)
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "a.md,mod/b.md" {
		t.Fatalf("entries = %s", got)
	}
}
