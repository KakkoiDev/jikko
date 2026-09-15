# Jikko Specification v1

## 1. Purpose

Jikko is a filesystem-native convention for structured work in plain Markdown.

Its portable source format is intentionally small:

```text
.md files
    |
    +-- YAML metadata
    |
    +-- Markdown body
    |
    +-- [[references]]
    |
    +-- ![[embeds]]
```

Jikko separates portable authored data from runtime capabilities.

> **The files describe what things are and explicitly relate them. The harness determines what can be done with that information.**

The specification defines source semantics. It does not prescribe a particular application, database, index implementation, user interface, or agent runtime.

## 2. Design principles

1. Markdown files are the source of truth.
2. Ordinary Markdown should work without conversion.
3. Add explicit semantics only when ambiguity would otherwise matter.
4. Prefer composition over inheritance.
5. Store authored facts; derive what can be reliably derived.
6. Preserve unknown metadata.
7. Keep folders organizational rather than semantic.
8. Keep derived runtime data disposable and reproducible.
9. Do not add a core primitive until substantially different real workflows demonstrate that it is necessary.

## 3. Fundamental file semantics

Every Jikko source object is a Markdown file consisting of optional YAML frontmatter and a Markdown body.

### 3.1 Document

A Markdown file without a `type` property is a **Document**.

```md
---
tags: [architecture]
---

# Authentication

Here's how authentication works...
```

A harness MAY accept `type: document` for explicitness, but it MUST NOT require it.

Documents contain information and may compose other files through embeds.

### 3.2 Task

A Task is explicitly declared with:

```yaml
type: task
```

Example:

```md
---
type: task
status: doing
start: 2026-09-15
due: 2026-09-20
tags: [architecture, authentication]
---

# Implement session persistence

Implement the approach described in [[authentication-architecture]].
```

Task metadata is extensible. Common properties include `status`, `start`, `due`, and `tags`, but the core specification does not require a large task-management schema.

A field such as `status` MUST NOT implicitly turn a document into a task. Ordinary documents may legitimately have values such as `status: draft` or `status: approved`.

### 3.3 View

A View is explicitly declared with:

```yaml
type: view
```

A View is a pure selection/query plus optional presentation hints.

```md
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

A View MUST NOT use its Markdown body for arbitrary narrative content or stored query results.

Narrative content that composes views belongs in a Document.

## 4. Metadata

Jikko uses YAML frontmatter for structured metadata.

Properties not defined by the core specification are allowed.

```yaml
client: acme
priority: high
course: japanese
invoice: 2026-0042
species: duck
```

Harnesses SHOULD preserve unknown properties when modifying files.

The core does not define `project`, `sprint`, `milestone`, `assignee`, `estimate`, `department`, or similar workflow-specific concepts. Such concepts can be represented by ordinary properties, tags, documents, links, and views.

Tags are ordinary metadata. The core imposes no tag hierarchy, inheritance, or namespace semantics.

## 5. References

### 5.1 Links

Authored relationships use wiki-style references:

```md
[[authentication-architecture]]
```

A link is source data because the author explicitly created the relationship.

### 5.2 Embeds

Composition uses:

```md
![[target]]
```

Embedding is universal. Depending on the resolved target, a harness may render:

- a Document as document content,
- a Task as an actionable task representation,
- a View as its evaluated live result.

This makes a dashboard simply a Document composed from other objects rather than a separate core type.

```md
# Product Development

Our immediate objective is to make the product usable end-to-end.

## Design

![[design-work]]

## Engineering

![[engineering-work]]

## Important Task

![[implement-authentication]]

See [[architecture]] for broader reasoning.
```

Jikko v1 does not define embed presentation modifiers.

## 6. Reference resolution

Jikko prefers human-readable filenames and paths rather than mandatory opaque identifiers.

For example:

```md
[[design]]
```

may resolve to `design.md` when unambiguous.

When names collide, paths can disambiguate:

```md
[[client-a/design]]
```

Harnesses that rename files SHOULD update references they can safely resolve.

Mandatory UUIDs are not part of v1.

## 7. Backlinks and derived relationships

Backlinks are derived data.

If `bar.md` contains:

```md
[[foo]]
```

then the authored fact is that `bar` references `foo`. The reverse statement, that `foo` is referenced by `bar`, should be derived by the harness rather than written into `foo.md`.

The same principle applies to search indexes, graph edges inferred from links, parsed Markdown caches, and query results.

## 8. Harness

A **harness** is any runtime that interprets a Jikko workspace.

A harness may be a:

- web application,
- CLI or TUI,
- editor plugin,
- desktop or mobile application,
- AI-agent runtime,
- or another compatible environment.

A harness may provide capabilities such as:

- full-text search,
- metadata search,
- backlinks,
- reference resolution,
- autocomplete,
- file exploration,
- rendering,
- query evaluation,
- tables,
- boards,
- calendars,
- timelines,
- graph visualization,
- indexes and caches.

These capabilities are not themselves source-file semantics.

## 9. Build and derived data

A harness MAY build derived data from the Markdown workspace.

Conceptually:

```text
                SOURCE
                  │
              *.md files
                  │
          ┌───────┴───────┐
          │               │
       content         metadata
          │               │
          └───────┬───────┘
                  ↓
              BUILD STEP
                  ↓
          derived index/cache
                  ↓
              HARNESS
        ┌─────────┼─────────┐
        ↓         ↓         ↓
      search    links     views
```

A harness may choose to store this under `.data`, but `.data` is not a standardized part of the portable Jikko format.

Different harnesses may use JSON, SQLite, IndexedDB, binary indexes, in-memory structures, or no persistent index.

The required conceptual invariant is:

```text
delete derived data
+
rebuild from Markdown
=
equivalent runtime state
```

Derived data SHOULD normally be excluded from version control.

Derived storage should behave as an index/cache, not as an authoritative generated copy of the workspace.

## 10. View evaluation

A View separates selection from presentation.

```yaml
filter:
  type: task
  tags: [design]
  status: [todo, doing]

view:
  layout: board
  group: status
  sort: due
```

`filter` describes **what** is selected and is part of the portable query semantics.

`view` describes **how** a harness may present the selection. A harness MAY ignore unsupported presentation hints while still evaluating the filter.

Jikko v1 deliberately avoids SQL and a general-purpose expression language. Query semantics should expand only in response to demonstrated use cases.

View results SHOULD normally be evaluated dynamically against the harness index rather than stored in source files or pre-materialized during every build.

## 11. View purity and composition

Views remain pure queries.

Documents provide narrative composition.

Jikko v1 does not define view inheritance, `extends`, dashboards, master views, projects, or templating systems.

The preferred mechanism is composition:

```md
![[design-work]]
![[engineering-work]]
```

rather than inheritance:

```yaml
extends: architecture
```

**Composition, never inheritance**, is a v1 design constraint.

## 12. Folder semantics

Folders organize files but carry no Jikko semantics.

Both of these layouts are valid:

```text
workspace/
├── docs/
├── tasks/
├── views/
├── clients/
└── archive/
```

and:

```text
workspace/
├── website/
│   ├── architecture.md
│   ├── fix-mobile.md
│   └── open-work.md
└── accounting/
```

The meaning of a file comes from its content and metadata, not its directory.

## 13. Rendering safety

Composition can be recursive. A workspace may accidentally contain cycles:

```text
A embeds B
B embeds A
```

The source files remain valid, but a rendering harness MUST detect recursive embed cycles and stop expansion safely.

## 14. Non-goals for v1

The following are deliberately not core primitives:

- Project
- Sprint
- Dashboard
- Database
- Collection
- Milestone
- Person
- Department
- Workspace hierarchy
- View inheritance
- Tag inheritance
- Materialized view results
- Mandatory UUIDs
- A standardized `.data` format
- A general templating language
- Rich embed modifier syntax

Their absence is intentional. They may be represented through existing primitives or reconsidered only after real usage demonstrates a missing capability.

## 15. Core summary

```text
DOCUMENT = information and composition
TASK     = information with explicit actionable semantics
VIEW     = pure selection plus presentation hints

LINK     = authored relationship
BACKLINK = derived relationship
EMBED    = composition
.data    = disposable harness-specific index/cache
```

Storage-level semantics:

```text
no type       -> Document
type: task    -> Task
type: view    -> View
```

## 16. v1 design test

Before expanding the format, Jikko should be exercised against substantially different workflows, including:

- software development,
- personal task management,
- research,
- teaching/course preparation,
- sales pipeline,
- event planning,
- long-form writing,
- CRM,
- home renovation,
- recurring operational work.

A useful next step is to create a representative fixture workspace of roughly fifteen Markdown files spanning these workflows and identify concrete points of friction.

The specification should evolve from those failures rather than from speculative abstractions.
