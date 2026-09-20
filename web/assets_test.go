package web

import (
	"bytes"
	"testing"
)

// The vendored bundles are pinned by version. A silent swap for a different
// build is exactly what this test exists to catch, so it asserts rather than logs.
func TestPinnedAssetVersions(t *testing.T) {
	htmx, err := Assets.ReadFile("assets/htmx.min.js")
	if err != nil { t.Fatal(err) }
	if !bytes.Contains(htmx, []byte(`"4.0.0"`)) {
		t.Error("htmx bundle does not declare version 4.0.0")
	}
	for _, marker := range []string{"sse:connect", "hx-live"} {
		if !bytes.Contains(htmx, []byte(marker)) {
			t.Errorf("htmx bundle is missing the %q extension the browser harness depends on", marker)
		}
	}
	css, err := Assets.ReadFile("assets/basecoat.min.css")
	if err != nil { t.Fatal(err) }
	for _, marker := range []string{".btn", "item-group"} {
		if !bytes.Contains(css, []byte(marker)) {
			t.Errorf("basecoat bundle is missing the %q class the templates use", marker)
		}
	}
}
