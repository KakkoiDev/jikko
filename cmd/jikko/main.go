package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	jikko "github.com/KakkoiDev/jikko"
)

func main() {
	if len(os.Args) < 2 { usage(); return }
	switch os.Args[1] {
	case "list": list(os.Args[2:])
	case "show": show(os.Args[2:])
	case "serve": serve(os.Args[2:])
	case "check": check(os.Args[2:])
	default: usage()
	}
}

func open(dir string) *jikko.Workspace { w, err := jikko.Open(dir); if err != nil { log.Fatal(err) }; return w }

func list(args []string) {
	f := flag.NewFlagSet("list", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); typ := f.String("type", "", "document, task, or view"); status := f.String("status", "", "status metadata"); asJSON := f.Bool("json", false, "JSON output"); f.Parse(args)
	pages := open(*dir).List(jikko.Kind(*typ), *status)
	if *asJSON { json.NewEncoder(os.Stdout).Encode(pages); return }
	for _, p := range pages { fmt.Printf("%-9s %s\n", p.Kind, p.Path) }
}

func show(args []string) {
	f := flag.NewFlagSet("show", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); asJSON := f.Bool("json", false, "JSON output"); f.Parse(args)
	if f.NArg() != 1 { log.Fatal("usage: jikko show [--json] <reference>") }
	p, ok := open(*dir).Resolve(f.Arg(0)); if !ok { log.Fatal("reference not found or ambiguous") }
	if *asJSON { json.NewEncoder(os.Stdout).Encode(p); return }
	fmt.Printf("%s\n\n%s", p.Title, p.Body)
}

func check(args []string) {
	f := flag.NewFlagSet("check", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); f.Parse(args)
	w := open(*dir); broken := 0
	for _, p := range w.Pages { for _, ref := range append(append([]string{}, p.Links...), p.Embeds...) { if _, ok := w.Resolve(ref); !ok { fmt.Printf("%s: unresolved %q\n", p.Path, ref); broken++ } } }
	if broken > 0 { os.Exit(1) }; fmt.Printf("ok: %d pages\n", len(w.Pages))
}

func serve(args []string) {
	f := flag.NewFlagSet("serve", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); addr := f.String("addr", "127.0.0.1:8080", "listen address"); f.Parse(args)
	t := template.Must(template.New("jikko").Parse(pageHTML + pagesHTML))
	pages := func(r *http.Request) []*jikko.Page { q := r.URL.Query(); return open(*dir).List(jikko.Kind(q.Get("type")), q.Get("status")) }

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" { http.NotFound(w, r); return }
		if err := t.ExecuteTemplate(w, "page", pages(r)); err != nil { http.Error(w, err.Error(), 500) }
	})
	http.HandleFunc("/pages", func(w http.ResponseWriter, r *http.Request) {
		if err := t.ExecuteTemplate(w, "pages", pages(r)); err != nil { http.Error(w, err.Error(), 500) }
	})
	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher); if !ok { http.Error(w, "streaming unsupported", 500); return }
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		var previous string
		ticker := time.NewTicker(time.Second); defer ticker.Stop()
		for {
			var b bytes.Buffer
			if err := t.ExecuteTemplate(&b, "pages", pages(r)); err != nil { return }
			current := b.String()
			if current != previous {
				fmt.Fprintf(w, "data: %s\n\n", oneLine(current)); flusher.Flush(); previous = current
			}
			select { case <-r.Context().Done(): return; case <-ticker.C: }
		}
	})

	log.Printf("Jikko: http://%s", *addr); log.Fatal(http.ListenAndServe(*addr, nil))
}

func oneLine(s string) string {
	var b bytes.Buffer
	for _, line := range bytes.Split([]byte(s), []byte("\n")) { b.Write(line) }
	return b.String()
}

const pageHTML = `{{define "page"}}<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Jikko</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/basecoat-css@1.0.2/dist/basecoat.cdn.min.css">
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-live.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-sse.min.js"></script>
</head>
<body class="bg-background text-foreground">
<main class="mx-auto max-w-4xl p-6 md:p-10">
<header class="mb-8 flex items-center justify-between">
<div><h1 class="text-3xl font-semibold tracking-tight">Jikko</h1><p class="text-muted-foreground">Structured work in plain Markdown.</p></div>
<span class="text-xs text-muted-foreground" hx-live="textContent = 'live'">live</span>
</header>
<nav class="mb-6 flex gap-2" aria-label="Page filters">
<button class="btn" data-variant="outline" hx-get="/pages" hx-target="#pages">All</button>
<button class="btn" data-variant="outline" hx-get="/pages?type=task" hx-target="#pages">Tasks</button>
<button class="btn" data-variant="outline" hx-get="/pages?type=view" hx-target="#pages">Views</button>
</nav>
<section id="pages" hx-sse:connect="/events" hx-swap="innerHTML">{{template "pages" .}}</section>
</main>
</body>
</html>{{end}}`

const pagesHTML = `{{define "pages"}}<div class="item-group">{{range .}}<article class="item" data-variant="outline"><section><h3>{{.Title}}</h3><p class="text-muted-foreground">{{.Kind}} · {{.Path}}</p></section></article>{{else}}<article class="item" data-variant="outline"><section><p>No pages.</p></section></article>{{end}}</div>{{end}}`

func usage() { fmt.Println("jikko <list|show|check|serve>\n\nUse --json with list/show for deterministic machine output.") }
