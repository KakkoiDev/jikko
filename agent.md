# Jikko Agent Guide

Read this first if you are an AI agent working on Jikko. It is intentionally short. For format details see `specification-v1.md`; for browser/product details see `architecture-browser.md` and `ux-plan.md`.

## What Jikko is

Jikko is an executable Markdown workspace for humans and agents. Markdown files are the source of truth. The Go harness exposes the same semantics through CLI and browser.

```text
no type       -> Document
type: task    -> Task
type: view    -> View
type: group   -> Group
```

Document = information, composition, and durable discussion surface. Task = explicit actionable semantics. View = pure query + presentation hints. Group = named actor membership used for addressing and future authorization.

> The files describe what things are and explicitly relate them. The harness determines what can be done with that information.

Never create a second source of truth.

## Source and derived state

Authored/source data includes Markdown, YAML, `[[links]]`, `![[embeds]]`, inline unresolved comments, and `@mentions`.

Backlinks, search indexes, mention indexes/inboxes, rendered views, caches, notification-delivery state, and similar runtime data are derived. Persistent `.data`, if used, must remain disposable/reproducible.

Git is the reference harness substrate for audit/history/diff/three-way merge. Git does not replace Jikko semantic operations.

## Identity and collaboration

Canonical identities:

```text
human:alice
agent:codex
group:maintainers
```

Canonical mentions:

```md
@human:alice
@agent:codex
@group:maintainers
```

A mention means **attention**. `assignee:` means **responsibility**. Do not treat them as equivalent and do not duplicate mentions into frontmatter merely for indexing.

Agents should query unresolved mentions/tasks through deterministic CLI/JSON interfaces. Notification delivery is derived runtime behavior, not source.

## Comments

Comments are unresolved review state inside the Markdown they discuss, not separate Comment/Message files. Current provisional source shape:

```md
This should <!--comment:c17-->update references<!--/comment:c17-->.

<!--comment-thread:c17
@agent:codex: Please verify links and embeds.
-->
```

The exact serialization is not frozen until parser compatibility is tested. Use explicit `comment` naming, not opaque shorthand.

Resolving a comment removes active comment markup/thread after incorporating the decision. Git retains historical discussion. Do not silently delete or orphan unresolved comments.

## Go harness and mutations

The Go core owns parsing, indexing, reference resolution, backlinks, mentions, groups, queries, safe mutations, uploads, rename rewriting, comments, and conflict semantics. CLI and HTTP are adapters.

Prefer CLI semantic operations and deterministic `--json`. Direct Markdown edits remain valid for complex content, followed by `jikko check`.

A mutation should carry actor context. Do not automatically write `updated_by` into documents. Audit attribution belongs in Git/history unless identity is part of the authored meaning (for example `assignee: agent:codex`).

Rename through Jikko should update safely resolvable `[[links]]` and `![[embeds]]`. Never assume a filesystem rename preserved references.

Uploads are ordinary workspace files and should use the same core operation from browser and CLI. Respect the configured maximum upload size; large-file storage/LFS is deliberately unresolved.

## Concurrency

Use optimistic revision checks. If the source changed after you read it, use a three-way merge where clean; otherwise return/resolve a structured conflict. Do not blindly overwrite another actor's edit.

Do not add CRDT/OT infrastructure without demonstrated need.

## Browser rules

```text
Go       = state + semantics
Markdown = durable data
HTML     = interactions
HTMX 4   = requests + swaps
SSE      = default server -> browser live updates
hx-live  = tiny local-only browser behavior
Basecoat = presentation
```

Editing is source-first, not WYSIWYG. Render when reading; expose real Markdown in the active edit context. Embedded Markdown is visibly bordered; selecting it reveals its `![[file]]` anchor and actions to open/edit the source file.

Use HTML/CSS first, then `hx-live`; add browser runtime only when accessibility/correctness justifies it. HTMAX/Basecoat assets are embedded in the Go binary.

## Composition and development bias

Use composition, not inheritance. Views stay pure. Folders have no semantic meaning. Prefer readable paths over mandatory UUIDs. Preserve unknown metadata.

Do not add Project, Sprint, Dashboard, Agent, Message, Chat, Comment, database, daemon, WebSocket, persistent index, CRDT, LFS, or another primitive/dependency without a concrete use case. `Group` is the current exception that earned primitive status through addressing and authorization needs.
