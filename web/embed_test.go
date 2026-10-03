package web

import "testing"

func TestEmbeddedBrowserAssets(t *testing.T) {
	for _, name := range []string{"assets/htmx.min.js", "assets/basecoat.min.css", "assets/jikko.css", "assets/jikko.js"} {
		b, err := Assets.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
}

func TestEmbeddedTemplates(t *testing.T) {
	b, err := Templates.ReadFile("templates/ui.html")
	if err != nil || len(b) == 0 {
		t.Fatalf("templates/ui.html: %v", err)
	}
}
