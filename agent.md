# Jikko Agent Guide

Read this first if you are an AI agent working on Jikko. For format details see `specification-v1.md`; `identity-permissions.md` is normative for the newer identity/authorization design and supersedes the older Group/namespaced-identity sections until the main specification is consolidated. Browser/product details live in `architecture-browser.md` and `ux-plan.md`.

## Core model

Jikko is an executable Markdown workspace for humans and agents. Markdown files are the source of truth. The Go harness exposes the same semantics through CLI and browser.

```text
no type        -> Document
type: task     -> Task
type: view     -> View
type: identity -> Identity
```

Document = information, composition, and durable discussion surface. Task = explicit actionable semantics. View = pure query + presentation hints. Identity = an addressable individual or group.

> The files describe what things are and explicitly relate them. The harness determines what can be done with that information.

Never create a second source of truth.

## Identity

There are no separate Human, Agent, Group, Team, Person, or Role primitives.

An Identity without `members` is an individual. An Identity with `members` is a group of identities. Groups may contain groups; membership is transitive and cycles must be detected.

```md
---
type: identity
members:
  - alice
  - codex
---

# Engineering
```

Human/agent/admin classifications can themselves be ordinary Identity groups. Reverse membership is derived; do not duplicate it onto member files.

Identity is not authentication. Credentials and secrets do not belong in Identity Markdown merely to prove caller identity. The harness authenticates a caller and maps it to a Jikko Identity.

## Mentions and responsibility

Address identities directly:

```md
@alice
@codex
@engineering
```

Use a path when a name is ambiguous. A mention means **attention**. `assignee:` means **responsibility**. Do not treat them as equivalent. Group mentions retain the group reference rather than being rewritten to current members.

Mention indexes/inboxes and notification-delivery state are derived runtime behavior.

## Permissions

Any Markdown file may define:

```yaml
permissions:
  read: customers
  comment: humans
  write: engineering
  admin: admins
```

A value may be one Identity or a list. Transitive membership satisfies grants.

An absent permission rule imposes no Jikko-level restriction. No `permissions` means unrestricted by Jikko, though filesystem/Git/server access may still restrict access.

The v1 capabilities are:

```text
read     read/render
comment  semantic comment operations without arbitrary content editing
write    ordinary content/metadata mutation
admin    access-control changes and explicit administrative operations
```

`write` must not silently grant the ability to change `permissions`. Identity membership changes that alter effective authorization are also authorization-sensitive. Never allow an actor to self-escalate by editing an ACL-bearing group Identity.

Do not add deny rules, folder inheritance, role hierarchy, permission inheritance, or implicit ACL propagation without demonstrated need.

Jikko ACLs govern harness operations; they do not sandbox actors with direct filesystem write access.

## Source and derived state

Authored/source data includes Markdown, YAML, `[[links]]`, `![[embeds]]`, Identity membership, permissions, inline unresolved comments, and `@mentions`.

Backlinks, reverse identity membership, search indexes, mention indexes/inboxes, rendered views, caches, notification-delivery state, and similar runtime data are derived. Persistent `.data`, if used, must remain disposable/reproducible.

Git is the reference harness substrate for audit/history/diff/three-way merge. Git does not replace Jikko semantic operations.

## Comments

Comments are unresolved review state inside the Markdown they discuss, not separate Comment/Message files. Current provisional source shape:

```md
This should <!--comment:c17-->update references<!--/comment:c17-->.

<!--comment-thread:c17
@codex: Please verify links and embeds.
-->
```

The exact serialization is not frozen until parser compatibility is tested. Resolving a comment removes active markup/thread after incorporating the decision; Git retains historical discussion. Do not silently delete or orphan unresolved comments.

## Go harness and mutations

The Go core owns parsing, indexing, reference resolution, backlinks, identity membership, mentions, authorization, queries, safe mutations, uploads, rename rewriting, comments, and conflict semantics. CLI and HTTP are adapters.

Prefer semantic operations and deterministic `--json`. Direct Markdown edits remain valid for complex content, followed by `jikko check`.

A mutation should carry authenticated actor context. Do not automatically write `updated_by` into documents. Audit attribution belongs in Git/history unless identity is part of authored meaning.

Rename through Jikko should update safely resolvable `[[links]]` and `![[embeds]]`. Uploads are ordinary workspace files and should use the same core operation from browser and CLI. Respect the configured maximum upload size.

## Concurrency

Use optimistic revision checks. If source changed after read, use a three-way merge where clean; otherwise return a structured conflict. Do not blindly overwrite another actor's edit. Do not add CRDT/OT infrastructure without demonstrated need.

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

Use HTML/CSS first, then `hx-live`; add browser runtime only when accessibility/correctness justifies it.

## Development bias

Use composition, not inheritance. Views stay pure. Folders have no semantic meaning. Prefer readable paths over mandatory UUIDs. Preserve unknown metadata.

Do not add Project, Sprint, Dashboard, Human, Agent, Group, Team, Role, Message, Chat, Comment, database, daemon, WebSocket, persistent index, CRDT, LFS, or another primitive/dependency without a concrete use case. Identity is the single actor/group primitive.
