# Jikko

**Structured work in plain Markdown.**

Jikko is a filesystem-native format for turning Markdown knowledge into actionable work without locking the source into a particular application.

A Jikko workspace is ordinary Markdown. YAML frontmatter adds explicit semantics where needed, `[[links]]` express relationships, and `![[embeds]]` compose documents, tasks, and live views.

The source stays portable. A harness provides search, backlinks, indexing, rendering, queries, and interactive views.

## Core model

```text
Markdown file
├── YAML frontmatter
└── Markdown body
```

Three semantic forms are sufficient for v1:

```text
no type       → Document
type: task    → Task
type: view    → View
```

Documents hold information and compose other files. Tasks add explicit actionable semantics. Views are pure queries plus optional presentation hints.

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

Jikko is an early specification and experiment. The v1 design should be tested against substantially different real-world workflows before adding more primitives.
