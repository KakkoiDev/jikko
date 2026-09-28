# Jikko in the browser

The browser runtime is a serverless deployment target for the same Go core, not a second Jikko implementation.

## Architecture

```text
same HTMX frontend contract
        |
  offline transport
        |
  Jikko Go/WASM
        |
  OPFS workspace
        |
  isomorphic-git
```

Native mode keeps the existing HTMX -> HTTP -> Go server path. Offline mode keeps HTMX as the UI and maps application requests to the local WASM backend. There is no application server in offline mode.

## Build

```sh
GOOS=js GOARCH=wasm go build -o jikko.wasm ./cmd/jikko-wasm
cd browser\nnpm install\nnpm run build
```

Load Go's standard `wasm_exec.js`, instantiate `jikko.wasm`, then load the bundled modules from `browser/dist/`. isomorphic-git and the OPFS adapter are bundled into these files, so the deployed offline runtime has no CDN or npm dependency.

## Storage and Git

`@componentor/fs` supplies an OPFS-backed Node-compatible filesystem. Jikko uses its asynchronous API, so static hosting does not require SharedArrayBuffer or COOP/COEP headers. isomorphic-git stores a normal `.git` directory in the same browser filesystem.

Browser Git currently exposes local repository operations:

- status
- add/remove during commit
- commit with `Jikko-Actor` and `Jikko-Operation` trailers
- log
- branches
- branch creation
- checkout
- merge

Remote fetch/push is deliberately deferred because browser Git hosting has authentication and CORS policy concerns. The local repository format remains Git-compatible, so sync can be added without changing Jikko's workspace model.

## Jikko primitives

The Go/WASM API mirrors the permission-aware native runtime:

- `tree(handle, actor)`
- `read(handle, actor, ref)`
- `readMany(handle, actor, refs)`
- `mentions(handle, actor)`
- `exportWorkspaceZIP(handle)`
- `downloadWorkspaceZIP(handle, filename)`

OPFS owns durable browser bytes. Go/WASM owns Jikko parsing, references, permissions and navigation. isomorphic-git owns Git semantics. HTMX owns the UI interaction contract.

ZIP export remains independent of Git and is a first-class Jikko core operation. It contains workspace source and assets while excluding `.git/`, `.data/` and `.auth.md`, so a portable backup never depends on Git history being healthy.

The current WASM adapter hydrates workspace files into Go's WASM virtual filesystem. The public API is intentionally storage-independent; a direct lazy OPFS FileStore can replace hydration later without changing callers.
