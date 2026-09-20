# Browser Harness Architecture

Jikko's reference browser harness is deliberately server-first and small.

## Mental model

```text
Go owns application state and Jikko semantics
Markdown owns durable workspace data
HTML describes interactions
HTMX moves server-rendered HTML
SSE pushes server-rendered HTML
hx-live handles tiny local browser behavior
Basecoat provides the visual/component layer
```

The browser is a presentation adapter around the same Go core used by the CLI. Do not build a second application model in JavaScript.

## Request flow

Browser-to-server actions use ordinary HTTP/HTMX requests. For example:

```html
<button
  hx-get="/pages?type=task"
  hx-target="#pages"
  hx-swap="outerHTML">
  Tasks
</button>
```

Read this literally: clicking the button asks Go for the task pages; Go returns HTML; HTMX replaces `#pages` with that HTML.

Do not introduce a JSON API, `fetch()` calls, or client-side state merely to implement an interaction that can return HTML.

## Live updates: SSE by default

Server-to-browser updates default to Server-Sent Events.

```text
Browser ----------------------> Go /events
             connection stays open

Markdown changes
      |
      v
Go observes/re-evaluates workspace
      |
      v
Go renders HTML
      |
Browser <---------------------- HTML over SSE
      |
      v
HTMX swaps the affected DOM
```

Use ordinary HTTP/HTMX for browser -> server and SSE for server -> browser. Do not add WebSockets unless a concrete feature requires a long-lived bidirectional channel.

The current implementation intentionally rescans the workspace rather than adding a filesystem-watcher dependency. Optimize only after measurement.

## Local browser behavior: hx-live

Prefer this order for browser-only behavior:

```text
Does it change application/workspace state?
  yes -> server request
  no  -> can HTML/CSS do it?
           yes -> HTML/CSS
           no  -> hx-live
```

`hx-live` is the escape hatch for small ephemeral UI behavior: toggling visibility, focus, disclosure state, transient UI values, and similar interactions that do not belong in Markdown or server state.

Do not slowly recreate a SPA inside `hx-live`.

### Basecoat JavaScript policy

Basecoat is primarily the visual/component layer. When a Basecoat component appears to require JavaScript, first try to implement the necessary local interaction with HTML/CSS or `hx-live` rather than adding Basecoat's JavaScript runtime or another client framework.

Only add third-party component JavaScript when `hx-live` cannot reasonably express the behavior or when reproducing the component's accessibility/interaction semantics ourselves would be less correct than using its supported runtime.

In other words: **prefer hx-live, but do not sacrifice accessibility or correctness merely to avoid a small justified dependency.**

## Assets and distribution

The reference binary embeds its browser assets with Go `embed.FS`. Runtime must not depend on npm, a CDN, or an internet connection.

Pinned assets currently include:

- HTMX 4.0.0 via the vendored `htmx.min.js` bundle, including the capabilities used for `hx-live` and `hx-sse`.
- Basecoat 1.0.2 CSS.

They are served locally under `/assets/` by `jikko serve`.

Vendored upstream assets should not be hand-edited. Upgrade them deliberately, keep versions pinned, and test that the embedded files are present.

## Architectural boundary

The Go core interprets Jikko. Interfaces adapt it:

```text
                 Jikko Go core
                      |
          +-----------+-----------+
          |                       |
         CLI                 HTTP/templates
          |                       |
     humans/agents             HTMX + SSE
                                  |
                               browser
```

The CLI and browser must share the same workspace semantics. A browser feature should not invent semantics that the core cannot represent, and a CLI mutation must remain a structured edit of the Markdown source rather than a second data model.

## What not to add by default

Do not add React/Vue/Svelte, a frontend build system, a client-side store, a JSON API, WebSockets, a database, or another browser scripting library because they are conventional. Each must be justified by a concrete requirement that the simpler stack cannot satisfy.
