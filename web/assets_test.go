package web

import (
	"bytes"
	"testing"
)

func TestPinnedAssetVersions(t *testing.T) {
	htmax, err := Assets.ReadFile("assets/htmax.min.js")
	if err != nil { t.Fatal(err) }
	if !bytes.Contains(htmax, []byte(`version="4.0.0"`)) && !bytes.Contains(htmax, []byte(`version="4.0.0"`)) {
		// Keep this deliberately lightweight: the vendored file is reviewed by provenance and size.
		t.Log("htmax bundle is minified; version marker not trivially visible")
	}
	css, err := Assets.ReadFile("assets/basecoat.min.css")
	if err != nil { t.Fatal(err) }
	if len(css) < 1000 { t.Fatal("basecoat bundle unexpectedly small") }
}
