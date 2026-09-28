# Jikko in the browser

The browser runtime is a deployment target for the same Go core, not a second Jikko implementation.

## Build

```sh
GOOS=js GOARCH=wasm go build -o jikko.wasm ./cmd/jikko-wasm
```

Load Go's standard `wasm_exec.js`, instantiate `jikko.wasm`, then import `browser/jikko.js`.

## Storage

OPFS is the durable browser backing store. The adapter hydrates Jikko's Go/WASM core from OPFS; parsing, references, permissions and navigation remain implemented by the same Go package used by the native CLI.

The initial browser primitives intentionally mirror the agent/native runtime:

- `tree(handle, actor)`
- `read(handle, actor, ref)`
- `readMany(handle, actor, refs)`
- `mentions(handle, actor)`
- `exportWorkspaceZIP(handle)`
- `downloadWorkspaceZIP(handle, filename)`

ZIP export is a core Jikko operation. It contains workspace source and assets while excluding `.git/`, `.data/` and `.auth.md`. Browser and native callers therefore share the same portable backup format.

The current adapter performs a hydration copy from OPFS into the Go WASM virtual filesystem. This deliberately proves API/semantic parity first. The next storage optimization is an OPFS-backed FileStore so large workspaces can be accessed lazily without changing the public browser primitives.
