//go:build js && wasm

package main

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
)

// Run with:
//
//	GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./cmd/jikko-wasm

var files = map[string]any{
	"alice.md":  "---\ntype: identity\n---\n# Alice\n",
	"bob.md":    "---\ntype: identity\n---\n# Bob\n",
	"crew.md":   "---\ntype: identity\nmembers: [alice]\n---\n",
	"open.md":   "# Open\n\n<b>raw</b> @alice\n",
	"secret.md": "---\npermissions:\n  admin: alice\n---\n# Secret\n",
	"board.md":  "---\ntype: view\nfilter:\n  type: identity\n---\n",
}

func call(t *testing.T, fn func(js.Value, []js.Value) any, args ...any) js.Value {
	t.Helper()
	values := make([]js.Value, len(args))
	for i, a := range args {
		values[i] = js.ValueOf(a)
	}
	return js.ValueOf(fn(js.Undefined(), values))
}

func open(t *testing.T, options ...any) int {
	t.Helper()
	r := call(t, openWorkspace, append([]any{files}, options...)...)
	if !r.Get("ok").Bool() {
		t.Fatalf("open: %s", r.Get("error").String())
	}
	return r.Get("value").Int()
}

func decoded(t *testing.T, r js.Value, into any) {
	t.Helper()
	if !r.Get("ok").Bool() {
		t.Fatalf("call failed: %s", r.Get("error").String())
	}
	if err := json.Unmarshal([]byte(r.Get("json").String()), into); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityIsBoundAtOpen(t *testing.T) {
	anonymous := open(t)
	alice := open(t, map[string]any{"identity": "alice"})
	var entries []struct{ Path string }
	decoded(t, call(t, tree, anonymous), &entries)
	for _, e := range entries {
		if e.Path == "secret.md" {
			t.Fatal("anonymous tree shows a restricted page")
		}
	}
	decoded(t, call(t, tree, alice), &entries)
	found := false
	for _, e := range entries {
		found = found || e.Path == "secret.md"
	}
	if !found {
		t.Fatal("alice cannot see her page")
	}
	// The identity cannot be switched per call any more.
	if r := call(t, tree, anonymous, "alice"); r.Get("ok").Bool() {
		t.Fatal("a per-call actor was accepted")
	}
	if r := call(t, readMany, anonymous, []any{"secret"}); r.Get("ok").Bool() {
		t.Fatal("anonymous read a restricted page")
	}
	var who map[string]string
	decoded(t, call(t, identity, alice), &who)
	if who["mode"] != "local" || who["identity"] != "alice" {
		t.Fatalf("identity = %v", who)
	}
	for _, bad := range []string{"crew", "nobody"} {
		if r := call(t, openWorkspace, files, map[string]any{"identity": bad}); r.Get("ok").Bool() {
			t.Fatalf("opened as %q", bad)
		}
	}
	if r := call(t, mentions, anonymous); r.Get("ok").Bool() {
		t.Fatal("mentions without an identity")
	}
}

func TestViewRenderAndCheck(t *testing.T) {
	alice := open(t, map[string]any{"identity": "alice"})
	var res struct{ Pages []struct{ Path string } }
	decoded(t, call(t, view, alice, "board"), &res)
	if len(res.Pages) != 3 {
		t.Fatalf("view = %+v", res)
	}
	r := call(t, render, alice, "open")
	if !r.Get("ok").Bool() || !strings.Contains(r.Get("value").String(), "&lt;b&gt;raw&lt;/b&gt;") {
		t.Fatalf("render = %v", r.Get("value"))
	}
	var report struct {
		Pages    int
		Problems []any
	}
	decoded(t, call(t, check, alice), &report)
	if report.Pages != 6 || len(report.Problems) != 0 {
		t.Fatalf("check = %+v", report)
	}
}
