# Browser Harness Architecture

Jikko has two browser execution modes with one UI philosophy: HTML + HTMX remains the frontend contract and the Go core remains the authority for Jikko semantics.

## Native/server mode

```text
HTMX frontend
     |
    HTTP
     |
Go HTTP harness
     |
Jikko Go core
     |
OS filesystem + system Git
```

## Serverless/offline mode

```text
HTMX frontend
     |
offline transport
     |
Jikko Go/WASM
     |
OPFS-backed filesystem
     |
isomorphic-git
```

The offline mode has no application server. It treats WASM as the local backend. A thin JavaScript transport maps HTMX actions to local backend calls and swaps rendered fragments. It must not contain Jikko semantics.

This split is deliberate: HTMX is retained as the frontend layer while transport changes from HTTP to local WASM calls.

## Durable browser state

The browser workspace lives in OPFS through an asynchronous Node-compatible filesystem adapter. The async surface works without cross-origin-isolation headers, keeping static hosting practical.

The same browser filesystem contains ordinary workspace files and `.git/`. isomorphic-git provides local status, commits, history, branches, checkout and merge. Git is an adapter: Jikko source semantics never depend on a particular Git implementation.

ZIP export is an independent first-class backup/portability path and excludes `.git/`, `.data/` and `.auth.md`.

## Future synchronization

Browser and native workspaces both use Git-compatible history, so future synchronization can build on Git fetch/push or an explicit Jikko sync service rather than inventing a second history model. Remote browser Git is not enabled by default because CORS and credential handling must be designed explicitly.

## HTMX boundary

Native HTMX requests go to HTTP routes. Offline HTMX actions are intercepted by the offline transport and dispatched to `OfflineBackend`. Templates and interaction intent should stay shared; transport-specific code must not become application state.

SSE remains useful in server mode. Offline mode does not emulate SSE: local mutations can trigger the same fragment refresh directly.

## Core boundary

```text
                    Jikko Go core
                         |
              +----------+----------+
              |                     |
         native adapters       browser adapters
        CLI/HTTP/system Git    WASM/OPFS/isomorphic-git
              |                     |
              +----------+----------+
                         |
                  HTML + HTMX UI
```

Do not add a client-side application model. Do not make OPFS, isomorphic-git, HTMX, HTTP, or system Git part of Jikko's source semantics.
