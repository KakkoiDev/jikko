package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	jikko "github.com/KakkoiDev/jikko"
	jikkoweb "github.com/KakkoiDev/jikko/web"
)

func main() {
	if len(os.Args) < 2 { usage(); return }
	switch os.Args[1] {
	case "list": list(os.Args[2:])
	case "show": show(os.Args[2:])
	case "set": set(os.Args[2:])
	case "auth-create": authCreate(os.Args[2:])
	case "serve": serve(os.Args[2:])
	case "check": check(os.Args[2:])
	default: usage()
	}
}

func open(dir string) *jikko.Workspace { w, err := jikko.Open(dir); if err != nil { log.Fatal(err) }; return w }

func list(args []string) {
	f := flag.NewFlagSet("list", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); typ := f.String("type", "", "document, task, view, or identity"); status := f.String("status", "", "status metadata"); asJSON := f.Bool("json", false, "JSON output"); f.Parse(args)
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

func set(args []string) {
	f := flag.NewFlagSet("set", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); token := f.String("token", os.Getenv("JIKKO_TOKEN"), "bearer token (or JIKKO_TOKEN)"); f.Parse(args)
	if f.NArg() != 3 { log.Fatal("usage: jikko set [--token TOKEN] <reference> <property> <value>") }
	if *token == "" { log.Fatal("authentication required: use --token or JIKKO_TOKEN") }
	if err := open(*dir).SetMetadataWithToken(*token, f.Arg(0), f.Arg(1), f.Arg(2)); err != nil { log.Fatal(err) }
}

func authCreate(args []string) {
	f := flag.NewFlagSet("auth-create", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); f.Parse(args)
	if f.NArg() != 1 { log.Fatal("usage: jikko auth-create <identity>") }
	token, err := open(*dir).CreateCredential(f.Arg(0)); if err != nil { log.Fatal(err) }
	fmt.Println(token)
}

func check(args []string) {
	f := flag.NewFlagSet("check", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); f.Parse(args)
	w := open(*dir); broken := 0
	if err := w.ValidatePermissions(); err != nil { fmt.Println(err); broken++ }
	for _, p := range w.Pages { for _, ref := range append(append([]string{}, p.Links...), p.Embeds...) { if _, ok := w.Resolve(ref); !ok { fmt.Printf("%s: unresolved %q\n", p.Path, ref); broken++ } } }
	if broken > 0 { os.Exit(1) }; fmt.Printf("ok: %d pages\n", len(w.Pages))
}

type pageData struct { Pages []*jikko.Page; Query string }

func serve(args []string) {
	f := flag.NewFlagSet("serve", flag.ExitOnError); dir := f.String("dir", ".", "workspace"); addr := f.String("addr", "127.0.0.1:8080", "listen address"); f.Parse(args)
	t := template.Must(template.New("jikko").Parse(pageHTML + pagesHTML))
	data := func(r *http.Request) pageData { q := r.URL.Query(); filters := url.Values{}; if v := q.Get("type"); v != "" { filters.Set("type", v) }; if v := q.Get("status"); v != "" { filters.Set("status", v) }; query := ""; if encoded := filters.Encode(); encoded != "" { query = "?" + encoded }; return pageData{Pages: open(*dir).List(jikko.Kind(q.Get("type")), q.Get("status")), Query: query} }
	renderPages := func(r *http.Request) (string, error) { var b bytes.Buffer; err := t.ExecuteTemplate(&b, "pages", data(r)); return b.String(), err }
	http.Handle("/assets/", http.FileServer(http.FS(jikkoweb.Assets)))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { if r.URL.Path != "/" { http.NotFound(w, r); return }; if err := t.ExecuteTemplate(w, "page", data(r)); err != nil { http.Error(w, err.Error(), 500) } })
	http.HandleFunc("/pages", func(w http.ResponseWriter, r *http.Request) { if err := t.ExecuteTemplate(w, "pages", data(r)); err != nil { http.Error(w, err.Error(), 500) } })
	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) { flusher, ok := w.(http.Flusher); if !ok { http.Error(w, "streaming unsupported", 500); return }; w.Header().Set("Content-Type", "text/event-stream"); w.Header().Set("Cache-Control", "no-cache"); previous, err := renderPages(r); if err != nil { return }; ticker := time.NewTicker(time.Second); defer ticker.Stop(); for { select { case <-r.Context().Done(): return; case <-ticker.C: }; current, err := renderPages(r); if err != nil { return }; if current != previous { fmt.Fprintf(w, "data: %s\n\n", oneLine(current)); flusher.Flush(); previous = current } } })
	log.Printf("Jikko: http://%s", *addr); log.Fatal(http.ListenAndServe(*addr, nil))
}

func oneLine(s string) string { return string(bytes.ReplaceAll([]byte(s), []byte("\n"), nil)) }

const pageHTML = `{{define "page"}}<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Jikko</title><link rel="stylesheet" href="/assets/basecoat.min.css"><script src="/assets/htmax.min.js"></script></head><body class="bg-background text-foreground"><main class="mx-auto max-w-4xl p-6 md:p-10"><header class="mb-8 flex items-center justify-between"><div><h1 class="text-3xl font-semibold tracking-tight">Jikko</h1><p class="text-muted-foreground">Structured work in plain Markdown.</p></div><span class="text-xs text-muted-foreground">live</span></header><nav class="mb-6 flex gap-2"><button class="btn" data-variant="outline" hx-get="/pages" hx-target="#pages" hx-swap="outerHTML">All</button><button class="btn" data-variant="outline" hx-get="/pages?type=task" hx-target="#pages" hx-swap="outerHTML">Tasks</button><button class="btn" data-variant="outline" hx-get="/pages?type=view" hx-target="#pages" hx-swap="outerHTML">Views</button><button class="btn" data-variant="outline" hx-get="/pages?type=identity" hx-target="#pages" hx-swap="outerHTML">Identities</button></nav>{{template "pages" .}}</main></body></html>{{end}}`
const pagesHTML = `{{define "pages"}}<section id="pages" hx-sse:connect="/events{{.Query}}" hx-swap="outerHTML"><div class="item-group">{{range .Pages}}<article class="item" data-variant="outline"><section><h3>{{.Title}}</h3><p class="text-muted-foreground">{{.Kind}} · {{.Path}}</p></section></article>{{else}}<article class="item" data-variant="outline"><section><p>No pages.</p></section></article>{{end}}</div></section>{{end}}`

func usage() { fmt.Println("jikko <list|show|set|auth-create|check|serve>\n\nMutations authenticate with --token or JIKKO_TOKEN.") }
