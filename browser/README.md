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
- add/configure a remote
- enumerate remote branches
- fetch one branch
- pull one branch
- push one branch
- sync the currently checked-out branch

Remote operations accept authentication from the calling application through an `onAuth` callback/object. Jikko does not store provider credentials in workspace files and does not impose provider-specific login semantics. Browser hosts must still satisfy the remote provider's CORS/authentication requirements.

Branch meaning is deliberately outside Jikko. A caller may use branches for releases, experiments, universes, timelines, or anything else; Jikko only supplies Git capability.

## Jikko primitives

The Go/WASM API mirrors the permission-aware native runtime:

- `loadOPFS(root, {identity})` / `JikkoWASM.open(files, {identity})`
- `tree(handle)`
- `read(handle, ref)`
- `readMany(handle, refs)`
- `mentions(handle)`
- `view(handle, ref)` -- evaluate a View
- `render(handle, ref)` -- the page's Markdown as safe HTML, from the same renderer as the native server
- `check(handle)` -- problems, unresolved references, and warnings, as `jikko check --json`
- `identity(handle)` -- `{mode: "local", identity}`
- `exportWorkspaceZIP(handle)`
- `downloadWorkspaceZIP(handle, filename)`

### Single-user local mode

The browser runtime has no authentication of its own. Every byte of the workspace, `.git/` included, lives in the device owner's browser storage, and whoever controls that storage can change any file, exactly as direct filesystem access bypasses Jikko permissions on a native workspace (`SECURITY.md`). A token check inside the same storage would protect nothing.

So the runtime runs in the single-user local mode the permission semantics allow. The host application, which owns authentication, binds one individual Identity when it opens the workspace; the Go core refuses an unknown Identity or a group, and every call then acts as that Identity -- permission filtering, mentions, views, and the `Jikko-Actor` trailer of browser commits. A call cannot name a different actor. Without an identity the runtime acts anonymously and sees only open pages, and browser commits are refused. `.auth.md` is never loaded into the browser.

OPFS owns durable browser bytes. Go/WASM owns Jikko parsing, references, permissions and navigation. isomorphic-git owns Git semantics. HTMX owns the UI interaction contract.

ZIP export remains independent of Git and is a first-class Jikko core operation. It contains workspace source and assets while excluding `.git/`, `.data/` and `.auth.md`, so a portable backup never depends on Git history being healthy.

The current WASM adapter hydrates workspace files into Go's WASM virtual filesystem. The public API is intentionally storage-independent; a direct lazy OPFS FileStore can replace hydration later without changing callers.


### GitHub from a static browser

GitHub does not expose Git smart-HTTP with the CORS headers needed for direct browser fetch/push. Jikko therefore also provides `GitHubRemote` in `github.js`. It uses GitHub's Git Database REST API to atomically create trees/commits and move branch refs without a CORS proxy.

This is a transport adapter, not application semantics. Callers still decide what a branch means. Credentials are supplied by the caller and are never persisted by the adapter.
