# Jikko Specification v1

## 1. Purpose

Jikko is a filesystem-native convention for structured work in plain Markdown.

Its portable source format is intentionally small:

```text
.md files
    |
    +-- YAML metadata
    +-- Markdown body
    +-- [[references]]
    +-- ![[embeds]]
    +-- inline comments and @mentions
```

Jikko separates portable authored data from runtime capabilities.

> **The files describe what things are and explicitly relate them. The harness determines what can be done with that information.**

## 2. Design principles

1. Markdown files are the source of truth.
2. Ordinary Markdown should work without conversion.
3. Add explicit semantics only when ambiguity matters.
4. Prefer composition over inheritance.
5. Store authored facts; derive what can be reliably derived.
6. Preserve unknown metadata.
7. Keep folders organizational rather than semantic.
8. Keep derived runtime data disposable and reproducible.
9. Git provides audit/history and text merge for the reference harness; Git does not replace Jikko semantics.
10. Do not add a primitive until substantially different workflows demonstrate that it is necessary.
11. Jikko is the authoritative workspace for both humans and agents; relevant work and discussion MUST be recorded in Jikko to become durable project knowledge.

## 2.1 Work model and runtime contract

Jikko work is:

- **Definable:** goals, requirements, acceptance criteria, constraints, and responsibilities can be represented in Documents and Tasks.
- **Discussable:** Documents and Tasks carry durable discussion through comments. Discussion inside Jikko is project knowledge.
- **Trackable:** Task state, responsibility, dependencies, blockers, and progress can be queried and projected through Views.
- **Provable:** claims of completion can cite commits, tests, files, measurements, screenshots, external references, or explicit review approval.
- **Auditable:** authored changes, semantic operations, actor attribution, and historical discussion can be reconstructed from Jikko source plus the reference harness history.

Jikko is the go-to workspace, not an export target for a separate conversation system. Humans and agents MUST read relevant Jikko context before acting and MUST record material goals, questions, decisions, progress, blockers, and evidence in Jikko. An external conversation is not authoritative project state until its material outcome is recorded in a Jikko Document, Task, or comment.

This model does not introduce Claim, Checkpoint, Evidence, Decision, Event, Message, or Chat file types. These are semantics expressed through Task metadata, Document/Task content, comments, links, embeds, and harness history.

The harness MUST expose semantic operations and validation that preserve this model. It MUST keep comments attached to their context, preserve actor attribution, make task state and responsibility queryable, validate resolvable proof references where possible, and surface unmet configured completion requirements. Enforcement MAY be a warning when hard rejection would make ordinary Markdown unusable, but it MUST be deterministic and available to both browser and machine interfaces.

## 2.2 Work-model fields (reference harness reading)

The reference harness reads a few Task properties as references so the work model can be checked without new primitives. They are ordinary metadata: any harness may ignore them, and a Document carrying them is not checked.

```md
---
type: task
status: done
assignee: alice
depends_on: [schema-migration]
blocked_by: "[[vendor-contract]]"
proof:
  - "[[load-test-results]]"
  - screenshots/dashboard.png
  - https://ci.example.com/runs/812
  - commit:1a2b3c4d
requires: [proof, review]
---
```

- `assignee` names one or more Identities (responsibility).
- `depends_on` names Tasks that must be done first.
- `blocked_by` names pages, usually Tasks, that block this one.
- `proof` names evidence: a page, a workspace file, a URL, or `commit:<id>`.
- `requires` lists completion requirements: `assignee`, `proof`, and `review` (no unresolved comments).

A reference may be bare or wrapped as `"[[target]]"`. `jikko check` reports, as failing reference problems, an assignee that is not an Identity, a dependency that is not a Task, an unresolved blocker or proof, a `commit:` proof that names no commit of the workspace's Git history (when there is one to ask), and an unknown requirement. It reports, as warnings that never fail the check or refuse a change, a Task that is `doing` with no assignee and a Task that is `done` while a dependency or blocking Task is not, or while one of its requirements is unmet. `jikko set` prints the warnings for the page it changed. Rename rewrites these references like links (§7.1).

## 3. Fundamental file semantics

Every Jikko source object is a Markdown file with optional YAML frontmatter and a Markdown body.

```text
no type        -> Document
type: task     -> Task
type: view     -> View
type: identity -> Identity
```

### 3.1 Document

A Markdown file without `type` is a Document. A harness MAY accept `type: document`, but MUST NOT require it.

Documents hold information, compose other files, and may contain inline review/discussion. A document can therefore also serve as a durable coordination/message board without introducing Chat or Message primitives.

### 3.2 Task

A Task is explicitly declared with `type: task`.

```md
---
type: task
status: doing
start: 2026-09-15
due: 2026-09-20
tags: [architecture]
assignee: codex
---

# Implement session persistence

Implement the approach described in [[authentication-architecture]].
```

Task metadata is extensible. Common properties include `status`, `start`, `due`, and `tags`. A field such as `status` MUST NOT implicitly turn a document into a task.

`assignee` is responsibility/ownership; an `@mention` is attention. They are not equivalent.

### 3.3 View

A View is explicitly declared with `type: view`. A View is a pure selection/query plus optional presentation hints.

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

A View MUST NOT use its body for arbitrary narrative content or stored query results. Narrative composition belongs in a Document.

#### 3.3.1 Reference harness reading

`jikko view <reference> [--json]` evaluates a View over the pages the caller may read; the View itself must be readable. Where this section leaves room the harness reads a View as follows:

- **`filter`** is a mapping of property to a value or a list of values. A page matches when, for every property, any of its values equals any listed value. `type` matches the page's type, with `document` matching untyped pages; `title` and `path` match the page's own; any other key matches frontmatter, where a list such as `tags` holds several values. Values compare as written text (a date as `2026-09-20`), and two references to the same page also match, so `assignee: alice` selects a Task assigned to `people/alice`. A View with no `filter` selects every readable page but itself.
- **`view.sort`** is a property or a list of them, each optionally prefixed `-` for descending. Numbers compare numerically, dates chronologically, anything else as text; pages without the property come last; ties fall back to the path.
- **`view.group`** is one property. A page with a list value appears in each of its groups. Values the `filter` lists for that property come first, in the filter's order and even when empty, so a board keeps its columns; other values follow in sort order, and pages without the property come last.
- **`view.layout`** and other keys under `view` are presentation hints. The harness keeps unknown hints and does not validate their values.
- **Validation.** A `filter` that is not a mapping, a filter value that is a mapping, an empty value list, an unknown `type`, a `view` that is not a mapping, and a `group` or `sort` of the wrong shape are workspace problems, so a mutation cannot introduce one.

### 3.4 Identity

An Identity is explicitly declared with `type: identity`. Identity is the single actor primitive and is normative in [identity-permissions.md](identity-permissions.md).

An Identity without `members` represents an individual. An Identity with `members` represents a group of identities:

```md
---
type: identity
members:
  - alice
  - codex
---

# Maintainers
```

There are no separate Human, Agent, Group, Team, Person, or Role primitives. Membership is transitive, reverse membership is derived, and cycles or dangling members MUST be reported by the harness.

Identities are addressed directly, such as `@alice`, `@codex`, or `@maintainers`. Identity is not authentication; the harness authenticates a caller and maps it to one individual Identity.

Authorization semantics are defined in [identity-permissions.md](identity-permissions.md). A mutation MUST NOT allow an actor to escalate its own effective authorization.

## 4. Metadata

Jikko uses YAML frontmatter for structured metadata. Unknown properties are allowed and SHOULD be preserved by harnesses.

Tags are ordinary metadata. The core imposes no tag hierarchy, inheritance, or namespace semantics.

Do not store information twice when it can be reliably derived. For example, `@codex` in authored Markdown is sufficient for the harness to derive a mention index; a duplicate `mentions:` property is not required.

## 5. References and embeds

### 5.1 Links

Authored relationships use `[[target]]`.

### 5.2 Universal embeds

Composition uses `![[target]]`. Embeds are file-oriented, not media-type-oriented.

Examples:

```md
![[child.md]]
![[diagram.png]]
![[demo.mp4]]
![[meeting.mp3]]
![[paper.pdf]]
```

A harness chooses presentation from the resolved target. Markdown renders as document/task/view/identity content as appropriate; common media render natively; unknown types fall back to a file card/link.

Embedded Markdown remains a reference to the source file, never a copied representation.

## 6. Mermaid

Fenced Mermaid is supported as source Markdown:

````md
```mermaid
flowchart LR
  Markdown --> Go
  Go --> HTML
```
````

The diagram source remains canonical. Browser rendering MUST use a reviewed security configuration. Editing exposes source, not a generated image as canonical data.

## 7. References, rename integrity, and resolution

Jikko prefers readable filenames/paths over mandatory UUIDs. Bare names may resolve when unambiguous; paths disambiguate duplicates.

Renames through the harness are semantic operations. The default rename SHOULD:

1. resolve inbound `[[links]]` and `![[embeds]]`;
2. rename the target;
3. rewrite safely resolvable authored references;
4. run integrity checks and report unresolved/ambiguous references.

A no-rewrite/filesystem-only mode MAY exist, but MUST warn which known references will break.

External renames cannot always be inferred safely. `jikko check` MUST report broken references rather than guess.

Backlinks remain derived data.

### 7.1 Reference harness reading

The reference harness implements rename and deletion as `jikko rename <reference> <new path> [--no-rewrite]` and `jikko delete <reference> [--prune]`. Where this section leaves room it takes the simplest safe reading:

- **What is a reference.** `[[links]]` and `![[embeds]]` in prose (never in code), `@mentions` and comment attributions, and the reference-valued frontmatter keys: `members`, `assignee`, the subjects of `permissions`, and the work-model keys `depends_on`, `blocked_by`, and `proof` (§2.2).
- **Same target afterwards.** A rename rewrites every reference that resolved before so that it resolves to the same page or file afterwards. That includes references to *other* pages that the move would make ambiguous, which are rewritten to their full path, and relative asset embeds in the moved page, which are rewritten from the workspace root. A rewritten reference keeps its style: a bare name stays bare when the new bare name is unambiguous, a `.md` suffix and an `|alias` are kept. A rename that would leave some page addressable by no reference at all is refused.
- **Meaning changes are reported.** A reference that resolved to nothing and would start resolving is reported as captured. With `--no-rewrite`, references that would stop resolving are reported as broken.
- **Authority.** A rename needs `write` on the page. Rewriting a reference in another page is an edit of that page and needs `write` on it, and `admin` where its `permissions` change. A rename that cannot make every rewrite is refused as a whole, naming the readable pages in the way and counting the unreadable ones, rather than half done. A no-rewrite rename is still refused if it would break a membership or an access policy.
- **Identity credentials follow a renamed Identity,** so its tokens keep working and a new Identity given the old name cannot claim them. Browser sessions bound to the old name end. Credentials of a deleted Identity are revoked.
- **Deletion** needs `write`. It is refused while the page has unresolved comments, and, for an Identity named by `members` or `permissions`, unless `--prune` removes those entries; pruning is judged like any other change of membership or policy. Links, embeds, assignees, and mentions of a deleted page are left in place and reported as broken.
- **Only Markdown pages** are renamed or deleted this way; assets are ordinary files.

## 8. Inline comments and review

Comments are not a separate core file type. Unresolved comments are authored review state inside the Markdown file being discussed.

The design direction is explicit HTML-comment markers around the anchored source plus an in-file thread. The exact serialization MUST be parser-tested before being frozen, but clarity is preferred over abbreviations. The provisional shape is:

```md
The harness should <!--comment:c17-->automatically update references<!--/comment:c17-->
when a file is renamed.

<!--comment-thread:c17
@alice: Should normal links update too?

@codex: Yes, links and embeds.
-->
```

Requirements:

- the anchor physically moves with the Markdown it annotates;
- raw Markdown remains understandable in ordinary editors;
- comments and replies are visible to Git history/diff;
- duplicate comment IDs are detectable;
- deleting commented content requires explicitly dealing with its comment markers/thread rather than silently orphaning discussion;
- the browser presents threads in a sidebar while preserving source-first editing;
- unresolved comments can be queried by the CLI/harness;
- completing a Task with unresolved comments SHOULD warn initially rather than being unconditionally forbidden.

Resolving a comment removes its active markers/thread from current Markdown after the decision is incorporated. Git history retains the historical discussion for audit/review.

Current Markdown therefore contains current unresolved discussion; Git contains historical discussion.

### 8.1 Reference harness reading

Where this section leaves room, the reference harness takes the simplest reading. These choices are provisional with the serialization itself:

- **Scope of an id.** Comment ids are unique within one file, not across the workspace. The harness assigns `c<n>`, one above the highest numbered id in the file, so the id of a resolved comment may be used again later; Git history disambiguates.
- **One anchor, one thread.** Every comment has exactly one anchor (`<!--comment:ID-->…<!--/comment:ID-->`) and exactly one thread (`<!--comment-thread:ID` … `-->`). There are no page-level comments without an anchor. A thread without an anchor is reported as orphaned discussion, and so is an anchor without a thread, an anchor that never ends, and an id used twice. These are workspace problems, so a mutation that would create one, such as deleting commented text, is rejected.
- **Anchors.** `jikko comment add` anchors on an excerpt that occurs exactly once in the page's prose, on one line, outside code, without splitting a `[[reference]]`.
- **Thread placement.** A new thread goes after the paragraph that holds its anchor, separated by blank lines, or at the end of the body.
- **Messages.** Each message is one paragraph that starts `@<identity>: `, written by the harness from the authenticated actor. Messages are separated by blank lines, so a message cannot contain one, nor a code fence, nor `<!--`, `-->`, or `--!>`. The attributing `@<identity>:` is not a mention; an `@mention` inside a message is.
- **Code is documentation.** Comment syntax inside fenced code blocks or inline code is not a comment.
- **Who may resolve.** Resolving needs the `comment` capability, as [permission-semantics-v1.md](permission-semantics-v1.md) states. Resolving removes the anchor markers, keeping the text they surround, and the whole thread.
- **Completing a Task.** A Task whose `status` is `done` while it has comments is reported by `jikko check` as a warning that does not fail the check, and `jikko set` prints the same warning. Neither refuses the change.

## 9. Identity, mentions, assignment, and notifications

Canonical mention syntax is source-native:

```md
@alice
@codex
@maintainers
```

Namespaces keep human, agent, and group identities unambiguous. A UI MAY render friendlier labels while preserving canonical source.

Mention semantics:

```text
@mention = attention
assignee = responsibility
```

Mentions are authored facts. Mention indexes, inboxes, notification badges, and delivery state are derived by the harness.

The harness SHOULD expose current mentions to browser, CLI, and agents, for example conceptually:

```sh
jikko mentions --for agent:codex --json
jikko watch --for agent:codex
```

Notification delivery is not Markdown source. Browser notifications, SSE, polling, OS notifications, or future adapters are harness capabilities. Disposable runtime state MAY remember which mention event/revision was already delivered; deleting that state may cause duplicate notifications but MUST NOT lose workspace information.

Groups are resolved as groups, not expanded by rewriting source mentions into individual mentions. Group membership changes therefore do not alter the historical authored intent of `@group:name`.

## 10. Git-backed history and audit

The reference harness SHOULD use real Git rather than implement a proprietary history store, diff engine, or three-way merge algorithm.

Git provides:

- immutable historical snapshots/commits,
- diffs,
- rename history,
- revert capability,
- audit history,
- and mature three-way text merge behavior.

Jikko continues to own semantic operations such as reference resolution, rename rewriting, task mutation, comment handling, uploads, and views.

A Jikko workspace remains valid Markdown without Git, but history, audit, historical review, and automatic three-way conflict handling may be unavailable. The reference harness SHOULD make Git initialization easy and warn when history guarantees are unavailable.

Successful semantic mutations SHOULD be commit-able as one logical transaction. For example, uploading an asset and inserting its embed should form one history event rather than unrelated changes.

Actor attribution SHOULD be carried by the mutation context and may be stored in structured Git commit trailers such as `Jikko-Actor` and `Jikko-Operation`. Do not automatically inject `updated_by` into every Markdown file.

## 11. Concurrent edits

Do not introduce CRDT/OT infrastructure until real simultaneous character-level coediting demonstrates a need.

Use optimistic concurrency around file/revision identity:

```text
read base revision A
       |
actor edits
       |
save against A
       |
current still A? -- yes --> save/commit
       |
       no
       v
three-way merge using base + actor edit + current
       |
clean? -- yes --> save/commit
       |
       no
       v
structured conflict for human/agent resolution
```

Git should provide the mature three-way merge machinery where practical; Jikko provides browser/CLI conflict UX. The browser should not expose raw conflict markers by default. Agents should receive deterministic structured conflict output.

SSE may notify open browser sessions when a source file changes externally, reducing avoidable conflicting saves.

### 11.1 Reference harness reading

- **Revisions.** A page's revision is the SHA-256 of its exact bytes, exposed as `rev` by `jikko show --json` (and on stderr by `jikko show --raw`, which prints the exact source). `jikko save <reference> --rev <rev>` saves an edited source against the revision it was based on; the browser editor does the same.
- **Finding the base.** When the page has changed since `rev`, the harness needs the base text. It uses, in order, a copy of the source as read that the caller sends with the edit (`--base`, or the browser form) and that is accepted only if its fingerprint is `rev`, then the page's Git history: the index and the last 200 commits that touched the file. A base found neither way means no three-way merge, as §10 anticipates for workspaces without Git: every difference from the current page is reported as a conflict, marked `base_known: false`.
- **Merging.** Frontmatter merges per key: a key changed by one side takes that side's value (a deletion included), a key changed identically by both takes it once, and a key changed differently by both is a field conflict. The body merges per line with the diff3 rule: a region changed by one side takes that side, and a region changed differently by both, adjacent lines included, conflicts. The current page's frontmatter bytes are kept unless a key changes. Frontmatter that cannot be read as a mapping is merged as part of the text.
- **Merge machinery.** §10 prefers Git's merge machinery. The reference harness takes the base from Git but performs the diff3 merge itself, in the Go core, because the browser runtime has no Git process and the specification asks for enforcement that is deterministic and identical for browser and machine interfaces (§2.1). The algorithm is the standard diff3 rule over a longest-common-subsequence line match, not a new one; for very large changed regions the match is skipped, which can only turn a merge into a conflict.
- **Results.** A clean merge is saved like any other edit, staged and authorized, and is reported as `merged`; recording it in Git remains `jikko commit`. A conflict writes nothing and returns, as JSON, the path, both revisions, whether the base was known, each conflicting key with its base, current, and yours values (YAML text, `null` when absent), and each conflicting body region with the line where it starts in the current body. Raw conflict markers are never written. Changing `permissions` in an edit needs `admin`.
- **Settling a conflict.** Each conflict has an id, `field:<key>` or `body:<n>` counting body regions from 0. Saving the same edit again with resolutions -- each id settled as `current` or `yours` (`jikko save --resolve body:0=yours --against <current rev>`, or the browser's per-conflict choice) -- produces the merge with those sides taken. Ids describe the page as it was when the conflict was reported, so resolutions apply only while the page is still at that revision; otherwise the conflicts are reported afresh. A conflict left unsettled is reported again.
- **Line endings.** An edit is converted to the line endings of the current file, since browsers submit text with CRLF.

## 12. Uploads and asset size

Uploads are ordinary workspace files and, by default, participate in Git history. Browser upload entry points include drag/drop, clipboard paste, `+` insertion, and accessible file picker. Show real byte progress when available and an indeterminate state otherwise.

CLI/agent upload must perform the same core operation, e.g. conceptually:

```sh
jikko upload ./diagram.png
jikko upload ./diagram.png --into architecture.md --json
```

The reference harness MUST impose a configurable maximum upload size to avoid accidentally placing impractically large binary files into ordinary Git history. The initial limit should be chosen from testing rather than premature optimization.

### 12.1 Reference harness reading

`jikko upload <file> [--into <page>] [--as <path>]` and the browser's file picker on a page perform the same core operation. The file becomes an ordinary workspace file, by default named as uploaded and placed next to the page it is embedded into, or at the root. With a page, `![[file]]` is appended to its body as a paragraph of its own in the same staged operation, which needs `write` on the page; a bare name is used when nothing else answers to it, the path otherwise. Without a page, an authenticated individual may upload. An existing file is never overwritten, and Markdown pages, dotfiles, harness state, paths outside the workspace, and names containing `[`, `]`, or `|` are refused. The maximum upload size defaults to 25 MiB -- well under the 50 MB at which GitHub warns and the 100 MB at which it refuses a file -- and is configured with `--max-upload` or `JIKKO_MAX_UPLOAD` (for example `10M`), for `jikko upload` and `jikko serve` alike. Upload progress, drag and drop, paste, and `+` insertion are not implemented yet.

**Open design question:** large binary asset storage. Git LFS or another local/remote large-file mechanism may eventually be useful, but it is deliberately not a v1 dependency. Any future design must consider local hosting, portability, deletion/garbage collection, and the complexity of introducing a second storage system.

## 13. Harness and derived data

A harness interprets a Jikko workspace and may provide search, backlinks, reference resolution, autocomplete, rendering, query evaluation, views, notifications, history UI, indexes, and caches.

Derived data MAY be stored under `.data`, but `.data` is not portable source and is not standardized.

Required invariant:

```text
delete derived data
+
rebuild from Markdown (+ Git history where the feature explicitly concerns history)
=
equivalent runtime state
```

Derived data SHOULD normally be excluded from version control.

## 14. View purity and composition

Views remain pure queries. Documents provide narrative composition. Do not add view inheritance, dashboards, master views, projects, or templating systems as core primitives.

Composition uses `![[...]]`; composition, never inheritance, remains a design constraint.

## 15. Folder semantics

Folders organize files but carry no Jikko semantics. Meaning comes from content and metadata, not directory placement.

## 16. Rendering safety

A rendering harness MUST detect recursive embed cycles and stop expansion safely.

### 16.1 Reference harness reading

The reference harness renders Markdown with its own small renderer in the Go core, safe by construction: every character of source is escaped and the only elements in the output are those the renderer writes. Raw HTML in a page is shown as text, never passed through; the specification nowhere asks for HTML passthrough. Links take only `http`, `https`, `mailto`, and same-site targets. `[[links]]` and `![[embeds]]` render as links or expansions only for pages the reader may read; an embed of a forbidden page renders exactly like a missing one. Embedded Markdown expands inside a bordered container, an embed of a page already being expanded stops with a visible note, and nesting stops at five levels. Images, video, and audio embed natively and other files as a download card. A workspace file is served only to a reader of some page that links or embeds it (or cites it as `proof`), and never when it is harness state, a symbolic link, or a dotfile. Mermaid source is shown as source; no diagram script runs. Comment anchors render as highlights linked to their thread, and threads are left out of the body for a sidebar.

## 17. Non-goals for v1

The following are deliberately not separate core primitives:

- Project
- Sprint
- Dashboard
- Database
- Collection
- Milestone
- Person
- Agent
- Message
- Chat
- Claim
- Checkpoint
- Evidence
- Decision
- Event
- Comment
- Department
- Workspace hierarchy
- View inheritance
- Tag inheritance
- Materialized view results
- Mandatory UUIDs
- standardized `.data`
- general templating language
- rich embed modifier syntax
- CRDT/OT collaboration
- Git LFS/large-file subsystem

`Identity` is the single actor primitive. Individual and group actors are expressed through Identity membership rather than separate Human, Agent, Group, Team, Person, or Role types.

## 18. Core summary

```text
DOCUMENT = information, composition, and durable discussion surface
TASK     = information with explicit actionable semantics
VIEW     = pure selection plus presentation hints
IDENTITY = individual or group actor for addressing, assignment, and authorization

LINK     = authored relationship
BACKLINK = derived relationship
EMBED    = composition
COMMENT  = authored durable discussion/review state inside Markdown
MENTION  = authored request for attention
ASSIGNEE = authored responsibility
GIT      = reference harness history/audit/merge substrate
.data    = disposable harness-specific index/cache/delivery state
```

## 19. Design test

Exercise the format against substantially different workflows before expanding it further: software development, personal tasks, research, teaching, sales, events, long-form writing, CRM, home renovation, and recurring operations. Add primitives only when real failures demonstrate missing semantics.
