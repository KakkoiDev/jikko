//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall/js"

	jikko "github.com/KakkoiDev/jikko"
)

var (
	mu         sync.Mutex
	next       int
	workspaces = map[int]*jikko.Workspace{}
	roots      = map[int]string{}
	keep       []js.Func
)

func main() {
	api := map[string]any{}
	bind := func(name string, fn func(js.Value, []js.Value) any) {
		f := js.FuncOf(fn)
		keep = append(keep, f)
		api[name] = f
	}
	bind("open", openWorkspace)
	bind("close", closeWorkspace)
	bind("tree", tree)
	bind("readMany", readMany)
	bind("mentions", mentions)
	bind("exportZIP", exportZIP)
	js.Global().Set("JikkoWASM", js.ValueOf(api))
	select {}
}

// open accepts an object whose keys are relative workspace paths and values are
// Uint8Array/string file contents. OPFS synchronization stays in browser JS;
// all Jikko semantics stay in the Go core.
func openWorkspace(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return fail("open(files) requires one object")
	}
	root, err := os.MkdirTemp("", "jikko-wasm-*")
	if err != nil {
		return fail(err.Error())
	}
	keys := js.Global().Get("Object").Call("keys", args[0])
	for i := 0; i < keys.Length(); i++ {
		name := keys.Index(i).String()
		if !safe(name) {
			_ = os.RemoveAll(root)
			return fail("unsafe path: " + name)
		}
		v := args[0].Get(name)
		var b []byte
		if v.Type() == js.TypeString {
			b = []byte(v.String())
		} else {
			b = make([]byte, v.Get("byteLength").Int())
			js.CopyBytesToGo(b, v)
		}
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			_ = os.RemoveAll(root)
			return fail(err.Error())
		}
		if err := os.WriteFile(full, b, 0644); err != nil {
			_ = os.RemoveAll(root)
			return fail(err.Error())
		}
	}
	w, err := jikko.Open(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return fail(err.Error())
	}
	mu.Lock()
	next++
	id := next
	workspaces[id] = w
	roots[id] = root
	mu.Unlock()
	return ok(id)
}

func closeWorkspace(_ js.Value, args []js.Value) any {
	w, root, id, err := lookup(args)
	_ = w
	if err != nil {
		return fail(err.Error())
	}
	mu.Lock()
	delete(workspaces, id)
	delete(roots, id)
	mu.Unlock()
	_ = os.RemoveAll(root)
	return ok(true)
}

func tree(_ js.Value, args []js.Value) any {
	w, _, _, err := lookup(args)
	if err != nil {
		return fail(err.Error())
	}
	actor := arg(args, 1)
	return jsonOK(w.Tree(actor))
}

func readMany(_ js.Value, args []js.Value) any {
	w, _, _, err := lookup(args)
	if err != nil {
		return fail(err.Error())
	}
	if len(args) < 3 {
		return fail("readMany(handle, actor, refs[])")
	}
	refs := stringsFromJS(args[2])
	pages, err := w.ReadMany(arg(args, 1), refs...)
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(pages)
}

func mentions(_ js.Value, args []js.Value) any {
	w, _, _, err := lookup(args)
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(w.Mentions(arg(args, 1)))
}

func exportZIP(_ js.Value, args []js.Value) any {
	w, _, _, err := lookup(args)
	if err != nil {
		return fail(err.Error())
	}
	b, err := w.ExportZIP()
	if err != nil {
		return fail(err.Error())
	}
	u := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(u, b)
	o := js.Global().Get("Object").New()
	o.Set("ok", true)
	o.Set("value", u)
	return o
}

func lookup(args []js.Value) (*jikko.Workspace, string, int, error) {
	if len(args) < 1 {
		return nil, "", 0, fmt.Errorf("workspace handle required")
	}
	id := args[0].Int()
	mu.Lock()
	defer mu.Unlock()
	w := workspaces[id]
	if w == nil {
		return nil, "", id, fmt.Errorf("unknown workspace")
	}
	return w, roots[id], id, nil
}

func arg(a []js.Value, i int) string {
	if len(a) > i {
		return a[i].String()
	}
	return ""
}

func stringsFromJS(v js.Value) []string {
	out := make([]string, v.Length())
	for i := range out {
		out[i] = v.Index(i).String()
	}
	return out
}

func safe(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == filepath.FromSlash(p) && filepath.Clean(p) != ".." && !filepath.IsLocal(p) == false
}

func jsonOK(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return fail(err.Error())
	}
	o := js.Global().Get("Object").New()
	o.Set("ok", true)
	o.Set("json", string(b))
	return o
}

func ok(v any) any {
	o := js.Global().Get("Object").New()
	o.Set("ok", true)
	o.Set("value", v)
	return o
}

func fail(s string) any {
	o := js.Global().Get("Object").New()
	o.Set("ok", false)
	o.Set("error", s)
	return o
}
