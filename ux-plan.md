# Jikko UX and Collaboration Plan

This document captures product decisions and open questions. It is a plan, not a promise that every item belongs in the core format.

## Editing model

Jikko is source-first. **No WYSIWYG document model.** Render Markdown when reading; when editing, expose the real Markdown source in the active context, inspired by Obsidian Live Preview.

- Markdown remains canonical at all times.
- Direct editing mutates source Markdown through the Go harness.
- `+` and `/` are insertion affordances, not evidence of an internal block database.
- Browser and CLI mutations share Go semantics.
- Raw/source editing is always available.

### Embedded Markdown

`![[file.md]]` renders inline with a visible border/container so users know it is embedded content.

When selected, reveal the literal `![[file.md]]` anchor and controls to open the file or open a modal editor for that source file. The modal edits `file.md` directly. SSE updates all rendered instances.

## Universal embeds and Mermaid

Use `![[path]]` for Markdown, images, video, audio, PDF, and arbitrary files. The harness selects presentation by file type; unknown types render as file cards.

Mermaid uses fenced `mermaid` source. Render diagrams in read mode and expose the real Mermaid source in edit mode. Review Mermaid security configuration before enabling workspace-authored diagrams.

## Inline comments: document as review surface and message board

Comments stay in the Markdown they discuss rather than becoming detached database records or a new `type: comment`.

Provisional source form (parser compatibility still must be tested):

```md
The harness should <!--comment:c17-->automatically update references<!--/comment:c17-->
when a file is renamed.

<!--comment-thread:c17
@human:alice: Should normal links update too?

@agent:codex: Yes, links and embeds.
-->
```

Use `comment`, not opaque shorthand such as `j`, for raw-source clarity.

Browser UX:

- highlighting text + Add comment inserts the anchor/thread markers;
- highlighted text remains ordinary visible Markdown;
- the thread appears in a document sidebar and is selectable from the anchor;
- replies update the same source file;
- unresolved comments are queryable and visibly count toward unfinished review state;
- resolving removes active comment markup/thread after the decision is incorporated;
- Git retains the historical discussion for audit.

This means an ordinary Document can also be a durable collaboration/message board. A dedicated chat/message primitive is unnecessary initially. Master coordination documents can compose Tasks/Documents with `![[...]]` and carry inline discussions/mentions.

A Task marked done while unresolved comments remain should warn initially rather than hard-fail.

## Mentions, assignment, and notifications

Canonical source mentions:

```md
@human:alice
@agent:codex
@group:maintainers
```

`@mention` means attention. `assignee:` means responsibility.

Do not duplicate authored mentions into frontmatter. The harness derives mention indexes and views just as it derives backlinks.

Notification delivery is a harness concern:

- browser badge/sidebar;
- SSE for connected clients;
- `jikko mentions --for agent:codex --json` for deterministic querying;
- eventual `jikko watch --for agent:codex` for long-running agents;
- OS/email/push integrations may be adapters later.

Disposable runtime state may record that a particular mention/revision has already been delivered. Losing that state may duplicate a notification but must never lose workspace meaning.

## Groups

Group is now a core primitive:

```md
---
type: group
members:
  - human:alice
  - agent:codex
---

# Maintainers
```

Groups serve mentions, identity organization, possible group assignment, and future admin/authorization roles. Keep the authored `@group:name` intact rather than expanding it into individual mentions.

Do not define dynamic/query-derived groups yet. If a real workflow needs them, evaluate whether Views can participate without broadening View into an overly generic query-object abstraction.

Authorization schema remains open. Any group used for authorization must be protected against self-escalation through harness operations. Jikko permissions cannot protect against actors who already have unrestricted filesystem/Git write access.

## Rename integrity

`jikko rename old new` should rewrite safely resolvable inbound `[[links]]` and `![[embeds]]`, then run integrity checks. An explicit no-rewrite mode may exist but must list/warn about known breakage. External filesystem renames are reported by `jikko check`; do not guess when identity is ambiguous.

## Git-backed audit/history and concurrency

Use real Git as the reference harness history/diff/three-way-merge substrate rather than inventing a Jikko history database.

Markdown/assets remain current state; Git provides historical state. Jikko semantic mutations should be commit-able as logical transactions and carry actor/operation attribution in structured commit metadata/trailers rather than writing `updated_by` into every file.

Concurrency starts with optimistic revision checks and Git-backed three-way text merge. Do not add CRDT/OT until simultaneous character-level coediting proves necessary.

Conflict UX should show current/yours/merge choices in the browser and structured deterministic conflict data to agents. Do not expose raw conflict markers by default.

SSE should notify open editors when a file changes externally.

## Uploads

Browser: drag/drop, clipboard paste, `+` -> upload, and accessible file picker. Show actual byte progress where available, otherwise indeterminate progress. Upload then inserts `![[path]]` at the selected location unless attachment-only was chosen.

CLI/agents use the same core operation:

```sh
jikko upload ./diagram.png
jikko upload ./diagram.png --into architecture.md --json
```

Uploads are ordinary workspace files and participate in Git history.

### Large files: deliberate v1 limit

Do not optimize for large-video edge cases yet. The harness will impose a configurable maximum upload size because ordinary Git history becomes impractical with sufficiently large binaries. Choose the default after testing.

**Open design question:** large binary storage. Git LFS is not a v1 dependency. Before adopting it, evaluate local hosting, portability, deletion/garbage collection, and the cost of adding a second storage system.

## UX references

- **Obsidian:** strongest model for source-faithful Live Preview editing and file embeds.
- **Notion:** steal `+`, slash insertion, direct manipulation, drag/reorder, uploads, contextual actions; do not copy its block database/WYSIWYG storage model.
- **Outline:** useful server-first reference for polished document editing, Mermaid, attachments, keyboard UX, and agent access.
- Continue evaluating Anytype, AFFiNE, Logseq, and other local-first/block editors only for interactions that survive cleanly when Markdown files are canonical.

## Distribution / Homebrew

First target a project-owned Homebrew tap.

Required work:

1. add an open-source license;
2. create stable tagged release (e.g. `v0.1.0`);
3. generate/commit `go.sum` and ensure reproducible dependencies;
4. add CI: `go test ./...`, `go vet ./...`, formatting check, functional CLI test;
5. create `KakkoiDev/homebrew-tap` with `Formula/jikko.rb`;
6. build/install the tagged Go source;
7. add a functional Homebrew formula test;
8. test/audit source installation;
9. document the one-command install in README.

Consider `homebrew/core` later after stable releases, real users, and maintenance history.

## Near-term dependency order

```text
CI + reproducible Go build
        |
Git integration + actor context
        |
safe Markdown mutation primitives
        +--> rename + reference rewrite/check
        +--> comments + mentions + groups
        +--> upload + attachment policy/size limit
        +--> optimistic concurrency + merge/conflict output
        |
universal renderer
        +--> Markdown embeds
        +--> media/PDF/file cards
        +--> Mermaid
        |
browser UX
        +--> source-first Markdown editing
        +--> embedded-document border/source/modal edit
        +--> comment sidebar + highlighting
        +--> + / slash insertion
        +--> drag/drop + paste + upload progress
        +--> mention notification UI
        |
agent UX
        +--> deterministic JSON mutations
        +--> mentions query/watch
        +--> upload/comment/reply/resolve
```

Keep workspace semantics in the Go core so browser and AI/CLI interfaces cannot drift apart.
