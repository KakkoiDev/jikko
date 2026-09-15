package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

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
	t := template.Must(template.New("index").Parse(pageHTML))
	h := func(w http.ResponseWriter, r *http.Request) { ws := open(*dir); q := r.URL.Query(); data := ws.List(jikko.Kind(q.Get("type")), q.Get("status")); if err := t.Execute(w, data); err != nil { http.Error(w, err.Error(), 500) } }
	http.HandleFunc("/", h); http.HandleFunc("/pages", h)
	log.Printf("Jikko: http://%s", *addr); log.Fatal(http.ListenAndServe(*addr, nil))
}

const pageHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Jikko</title><script src="https://unpkg.com/htmx.org@2.0.8"></script><style>body{font:16px system-ui;max-width:900px;margin:3rem auto;padding:0 1rem}nav{display:flex;gap:.5rem;margin-bottom:2rem}button{padding:.5rem .8rem}li{margin:.5rem 0}small{opacity:.6}</style></head><body><h1>Jikko</h1><nav><button hx-get="/pages" hx-target="#pages">All</button><button hx-get="/pages?type=task" hx-target="#pages">Tasks</button><button hx-get="/pages?type=view" hx-target="#pages">Views</button></nav><ul id="pages">{{range .}}<li><strong>{{.Title}}</strong> <small>{{.Kind}} · {{.Path}}</small></li>{{else}}<li>No pages.</li>{{end}}</ul></body></html>`

func usage() { fmt.Println("jikko <list|show|check|serve>\n\nUse --json with list/show for deterministic machine output.") }
