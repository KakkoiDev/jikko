package web

import "embed"

// Assets contains the pinned browser runtime shipped inside the Jikko binary,
// together with Jikko's own stylesheet and progressive-enhancement script.
//
//go:embed assets/*
var Assets embed.FS

// Templates holds the HTML templates of the browser interface. They are
// shared by every harness that serves the interface, so the markup and its
// HTMX interactions stay in one place.
//
//go:embed templates/*.html
var Templates embed.FS
