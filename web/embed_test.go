package web

import "testing"

func TestEmbeddedBrowserAssets(t *testing.T) {
	for _, name := range []string{"assets/htmx.min.js", "assets/basecoat.min.css"} {
		b, err := Assets.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}
