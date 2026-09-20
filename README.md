# Jikko

**Define, discuss, track, prove, and audit work in plain Markdown — for humans and agents.**

Jikko is a human-readable workspace where humans and agents define, discuss, track, prove, and audit work together without locking the source into a particular application.

Jikko is the authoritative place for the work. Humans and agents are expected to read the relevant Jikko context before acting and to return goals, discussions, decisions, progress, and evidence to Jikko. External conversations may help, but they are not project knowledge until recorded in the workspace.

A workspace is ordinary files. YAML frontmatter adds explicit semantics, `[[links]]` express relationships, `![[embeds]]` compose files, inline comments keep unresolved review attached to source, and `@mentions` address humans, agents, and groups. Git is planned as the reference harness substrate for audit/history and three-way text merge.

## Quickstart tutorial

Jikko is pre-release, so the current development install requires Go. A project-owned Homebrew tap is planned for the first tagged release.

```sh
go run ./cmd/jikko list --dir /path/to/workspace
go run ./cmd/jikko check --dir /path/to/workspace
go run ./cmd/jikko serve --dir /path/to/workspace
```

Open the address printed by `serve` (currently `http://127.0.0.1:8080` by default).

A normal document needs no type:

```md
# Authentication

Sessions are described in [[session-design]].
```

A task is explicit:

```md
---
type: task
status: todo
due: 2026-09-20
assignee: agent:codex
---

# Implement session persistence

@agent:codex follow [[session-design]].
```

An identity may represent an individual or a group:

```md
---
type: identity
members:
  - alice
  - codex
---

# Maintainers
```

Mention it with `@maintainers`.

Compose another file with an embed:

```md
# Product development

![[open-work]]
```

Then inspect the workspace:

```sh
go run ./cmd/jikko list --dir /path/to/workspace --type task
go run ./cmd/jikko list --dir /path/to/workspace --type task --status todo --json
go run ./cmd/jikko show --dir /path/to/workspace session-design --json
go run ./cmd/jikko check --dir /path/to/workspace
```

`--json` is intended for agents/scripts. Markdown remains source of truth regardless of whether a human, browser, or agent edits it.

> **Current limitation:** the implementation can inspect and serve a workspace, but the source-first browser editor, comments/mentions/groups implementation, Git history/merge integration, uploads, media embeds, Mermaid, semantic rename, and mutation commands are roadmap work.

## Core model

```text
no type        -> Document
type: task     -> Task
type: view     -> View
type: identity -> Identity
```

- **Document** — information, composition, and durable discussion surface.
- **Task** — explicit actionable semantics.
- **View** — pure selection plus optional presentation hints.
- **Identity** — an addressable individual or group used for mentions, assignment, and authorization.

Document, Task, and View are the three work-content primitives. Identity is the single actor primitive. Comments, Messages, Chats, Humans, Agents, and Groups are deliberately not additional file types. Inline comments live in the Markdown they discuss. Identities are addressed directly, such as `@alice` or `@maintainers`; `@mention` means attention while `assignee:` means responsibility.

## Work model

Jikko keeps its conceptual model small. Claim, checkpoint, evidence, decision, and event are capabilities or conventions around Documents and Tasks, not additional content types. The runtime interprets and enforces their semantics through structured mutations, validation, history, and views.

- **Definable** — goals, requirements, acceptance criteria, constraints, and responsibilities can be written clearly.
- **Discussable** — humans and agents hold persistent discussions through Documents and comments. Discussion is workspace content, not disposable chat.
- **Trackable** — Tasks expose their state, assignee, dependencies, blockers, and progress.
- **Provable** — completion can be supported by commits, tests, files, measurements, screenshots, external references, or review approval.
- **Auditable** — Git history and Jikko activity/history expose who changed what, when, and why.

The runtime must help users satisfy this model without multiplying primitives: validate structured fields and references, expose unresolved discussion and incomplete work, preserve actor attribution, require authorization for mutations, and warn or reject completion when configured proof requirements are unmet.

> **If work matters to the project, it belongs in Jikko.**

## Source-first collaboration direction

Editing will not use a WYSIWYG/block database. Render Markdown while reading and expose the real Markdown source in the active editing context. Embedded Markdown is visibly bordered; selecting it reveals its `![[file]]` anchor and controls to open/edit the source file.

Universal embeds use the same syntax for Markdown and assets:

```md
![[architecture.md]]
![[diagram.png]]
![[demo.mp4]]
![[paper.pdf]]
```

Mermaid remains fenced text source. Browser uploads will support drag/drop, paste, `+` insertion, a picker, and real progress; agents will get the same operation through the CLI. Ordinary Git history motivates a configurable maximum upload size. Large-file/LFS storage is an open design question rather than a v1 dependency.

Inline comment serialization is still provisional pending parser tests, but the direction is explicit source markers such as `<!--comment:c17-->...<!--/comment:c17-->` plus an in-file thread. Current Markdown contains unresolved discussion; resolving it cleans the current source while Git retains the historical discussion.

## Reference harness

```text
                    Markdown workspace
                           |
                     +-----v-----+
                     |  Go core  |
                     +-----+-----+
                           |
              +------------+------------+
              v                         v
             CLI                  Go HTTP server
        human + agents                   |
                                  HTML + HTMX 4
                                  hx-live + SSE
                                  Basecoat CSS
                                         |
                                      browser
```

The CLI is Jikko's initial machine interface. Semantic mutations must remain structured edits of the same files rather than creating a second data model. Human-readable output is default; machine-facing commands should provide deterministic JSON.

The core currently scans Markdown directly into memory. There is deliberately no database or persistent `.data` index yet.

The browser harness stays server-first: HTMX 4 for requests/swaps, hx-live for tiny local behavior, SSE by default for server-to-browser updates, and Basecoat CSS for presentation. HTMAX 4.0.0 and Basecoat 1.0.2 CSS are vendored/embedded, so runtime does not require CDN/npm/network access.

See [architecture-browser.md](architecture-browser.md) for browser architecture, [agent.md](agent.md) for AI-agent orientation, [ux-plan.md](ux-plan.md) for product/collaboration decisions, and [specification-v1.md](specification-v1.md) for source semantics.

## Design rule

> **The files describe what things are and explicitly relate them. The harness determines what can be done with that information.**

## Status

Jikko is an early specification and reference implementation. The design should continue to be tested against substantially different workflows before adding more primitives.
