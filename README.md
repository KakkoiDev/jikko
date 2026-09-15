# Jikko

**Structured work in plain Markdown — for humans and agents.**

Jikko is a filesystem-native format for turning Markdown knowledge into actionable work without locking the source into a particular application.

A Jikko workspace is ordinary Markdown. YAML frontmatter adds explicit semantics where needed, `[[links]]` express relationships, and `![[embeds]]` compose documents, tasks, and live views.

The source stays portable. The reference harness is a small Go program that exposes the same workspace semantics to a CLI and a server-rendered HTMX browser interface.

## Core model

```text
Markdown file
├── YAML frontmatter
└── Markdown body
```

```text
no type       → Document
type: task    → Task
type: view    → View
```

Documents hold information and compose other files. Tasks add explicit actionable semantics. Views are pure queries plus optional presentation hints.

## Reference harness

```text
                    Markdown workspace
                           │
                     ┌─────▼─────┐
                     │  Go core  │
                     └─────┬─────┘
                           │
              ┌────────────┴────────────┐
              ▼                         ▼
             CLI                    HTTP/HTMX
        human + agents                browser
```

The CLI is also Jikko's initial machine interface. Commands that mutate a workspace must remain structured edits of the same Markdown source rather than creating a second data model. Human-readable output is the default; machine-facing commands should provide deterministic JSON.

Current first slice:

```sh
go run ./cmd/jikko list
go run ./cmd/jikko list --type task
go run ./cmd/jikko list --type task --status todo --json
go run ./cmd/jikko show authentication --json
go run ./cmd/jikko check
go run ./cmd/jikko serve
```

The core currently scans Markdown directly into memory. There is deliberately no database or persistent `.data` index yet; persistent indexing should be added only if measurements justify it.

## Example

```md
---
type: task
status: doing
due: 2026-09-20
tags: [architecture]
---

# Implement session persistence

Implement the approach described in [[authentication-architecture]].
```

A view selecting work:

```yaml
---
type: view
filter:
  type: task
  tags: [architecture]
  status: [todo, doing]
view:
  layout: board
  group: status
  sort: due
---
```

And a normal document can compose both:

```md
# Product Development

## Engineering

![[engineering-work]]

## Important task

![[implement-session-persistence]]
```

## Design rule

> The files describe what things are and explicitly relate them. The harness determines what can be done with that information.

See [specification-v1.md](specification-v1.md) for the proposed v1 format.

## Status

Jikko is an early specification and reference implementation. The v1 design should be tested against substantially different real-world workflows before adding more primitives.
