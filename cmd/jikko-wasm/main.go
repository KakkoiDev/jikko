//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall/js"

	jikko "github.com/KakkoiDev/jikko"
)

// The browser runtime is single-user local mode (permission-semantics-v1.md,
// "Authentication bootstrap"). All bytes live in the device owner's own
// browser storage, which is the security boundary, exactly as direct
// filesystem access is for a native workspace. The host application may bind
// one individual Identity when it opens a workspace -- that is the injected
// authentication of architecture-browser.md -- and every call then acts as
// that Identity. Without one, calls act anonymously and see only open pages.
// A call cannot name another actor: the identity is fixed per handle.
var (
	mu         sync.Mutex
	next       int
	workspaces = map[int]*jikko.Workspace{}
	roots      = map[int]string{}
	identities = map[int]string{}
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
	bind("view", view)
	bind("render", render)
	bind("check", check)
	bind("identity", identity)
	bind("exportZIP", exportZIP)
	js.Global().Set("JikkoWASM", js.ValueOf(api))
	select {}
}

// open accepts an object whose keys are relative workspace paths and values are
// Uint8Array/string file contents, and optionally {identity: "<name>"}: the
// individual Identity the host vouches for. OPFS synchronization stays in
// browser JS; all Jikko semantics stay in the Go core.
func openWorkspace(_ js.Value, args []js.Value) any {
	if len(args) < 1 || len(args) > 2 {
		return fail("open(files, {identity}) requires the files object")
	}
	actor := ""
	if len(args) == 2 && args[1].Type() == js.TypeObject {
		if v := args[1].Get("identity"); v.Type() == js.TypeString {
			actor = v.String()
		}
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
	if actor != "" {
		p, found := w.ResolveIdentity(actor)
		if !found || len(p.Members) != 0 {
			_ = os.RemoveAll(root)
			return fail("identity " + actor + " is not an individual Identity of this workspace")
		}
		actor = strings.TrimSuffix(p.Path, ".md")
	}
	mu.Lock()
	next++
	id := next
	workspaces[id] = w
	roots[id] = root
	identities[id] = actor
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
	delete(identities, id)
	mu.Unlock()
	_ = os.RemoveAll(root)
	return ok(true)
}

func tree(_ js.Value, args []js.Value) any {
	w, actor, err := session(args, 1)
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(w.Tree(actor))
}

func readMany(_ js.Value, args []js.Value) any {
	w, actor, err := session(args, 2)
	if err != nil {
		return fail(err.Error())
	}
	pages, err := w.ReadMany(actor, stringsFromJS(args[1])...)
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(pages)
}

func mentions(_ js.Value, args []js.Value) any {
	w, actor, err := session(args, 1)
	if err != nil {
		return fail(err.Error())
	}
	if actor == "" {
		return fail("mentions need an identity: open the workspace with {identity}")
	}
	return jsonOK(w.Mentions(actor))
}

// view(handle, ref) evaluates a View for the bound identity.
func view(_ js.Value, args []js.Value) any {
	w, actor, err := session(args, 2)
	if err != nil {
		return fail(err.Error())
	}
	res, err := w.EvaluateView(actor, args[1].String())
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(res)
}

// render(handle, ref) renders a page's Markdown to safe HTML for the bound
// identity, with the same renderer as the native server.
func render(_ js.Value, args []js.Value) any {
	w, actor, err := session(args, 2)
	if err != nil {
		return fail(err.Error())
	}
	pages, err := w.ReadMany(actor, args[1].String())
	if err != nil {
		return fail(err.Error())
	}
	return ok(string(w.Renderer(actor, jikko.DefaultLinks).Page(pages[0])))
}

// check(handle) reports workspace problems, unresolved references, and
// warnings, as `jikko check --json` does.
func check(_ js.Value, args []js.Value) any {
	w, _, err := session(args, 1)
	if err != nil {
		return fail(err.Error())
	}
	unresolved, err := w.UnresolvedReferences()
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(map[string]any{
		"pages":    len(w.Pages),
		"problems": append(append([]jikko.Problem{}, w.Problems...), unresolved...),
		"warnings": w.Warnings(),
	})
}

// identity(handle) reports the mode and the bound identity.
func identity(_ js.Value, args []js.Value) any {
	_, actor, err := session(args, 1)
	if err != nil {
		return fail(err.Error())
	}
	return jsonOK(map[string]string{"mode": "local", "identity": actor})
}

// session resolves the handle and its bound identity, and checks the call
// passed exactly the arguments it takes, so a caller still using the old
// per-call actor argument fails loudly instead of being silently ignored.
func session(args []js.Value, want int) (*jikko.Workspace, string, error) {
	if len(args) != want {
		return nil, "", fmt.Errorf("expected %d argument(s); the identity is bound when the workspace is opened", want)
	}
	w, _, id, err := lookup(args)
	if err != nil {
		return nil, "", err
	}
	mu.Lock()
	defer mu.Unlock()
	return w, identities[id], nil
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
