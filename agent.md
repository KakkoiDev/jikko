# Jikko Agent Guide

Read this first if you are an AI agent working on Jikko. It is intentionally short. For format details see `specification-v1.md`; for browser details see `architecture-browser.md`.

## What Jikko is

Jikko is an executable Markdown workspace for humans and agents. Markdown files are the source of truth. The Go harness interprets them and exposes the same semantics through a CLI and a server-rendered browser UI.

```text
.md files
  |
  +-- YAML frontmatter
  +-- Markdown body
  +-- [[links]]
  +-- ![[embeds]]
```

Semantic convention:

```text
no type       -> Document
type: task    -> Task
type: view    -> View
```

Documents contain information and compose things. Tasks are documents with explicit actionable semantics. Views are pure queries plus optional presentation hints.

## The invariant

> The files describe what things are and explicitly relate them. The harness determines what can be done with that information.

Never create a second source of truth.

## Source vs derived state

Authored/source data belongs in Markdown. Backlinks, search indexes, parsed Markdown, query results, graph data, caches, rendered views, and autocomplete are derived by the harness.

If persistent `.data` is introduced, it must remain disposable and reproducible from Markdown. Do not standardize a `.data` format as part of Jikko compatibility.

## Go harness

The Go core owns parsing, indexing, reference resolution, backlinks, queries, and other Jikko semantics. CLI and HTTP are adapters around that core.

For agents, prefer the CLI for semantic operations and deterministic `--json` output. Direct Markdown edits are fine for complex content, followed by `jikko check`.

Do not make the CLI a second data model: a CLI mutation should only perform a safe structured edit of Markdown.

## Browser rules

```text
Go       = state + semantics
Markdown = durable data
HTML     = interactions
HTMX 4   = requests + swaps
SSE      = default server -> browser live updates
hx-live  = tiny local-only browser behavior
Basecoat = CSS/component presentation
```

Browser -> server: ordinary HTTP/HTMX.

Server -> browser: SSE by default.

For local interaction, use HTML/CSS first, then `hx-live`. If a Basecoat component needs JS, first see whether `hx-live` can provide the behavior correctly. Do not add Basecoat JS or another browser framework by reflex. Accessibility/correctness wins if a supported component runtime is genuinely necessary.

HTMX/HTMAX and Basecoat CSS are vendored and embedded in the Go binary. Jikko should run without CDN/npm/network access.

## Composition

Use links and embeds, not inheritance:

```md
[[architecture]]
![[open-work]]
```

A document may embed documents, tasks, or views. Views remain pure: no arbitrary Markdown body and no stored/generated results. Do not add `extends`, dashboard/project/sprint entity types, or template machinery without demonstrated need.

## Metadata

YAML properties are open-ended. Preserve unknown properties. Core task metadata should stay small (`status`, `start`, `due`, `tags`). Project, sprint, priority, assignee, etc. are not core merely because another work-management system has them.

Folders organize files but have no semantic meaning. Prefer readable filenames/paths over mandatory UUIDs. Rename tooling should update references.

## Development bias

Choose the simplest layer that works. Do not add a framework, database, daemon, persistent index, WebSocket, JSON API, inheritance system, or new core primitive until a concrete use case demonstrates the need.

When uncertain, preserve Markdown portability and keep semantics in the Go core rather than an interface.
