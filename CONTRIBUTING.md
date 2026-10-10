# Contributing to Jikko

This is the guide for changing Jikko itself: the Go core, the CLI, the HTTP
server, the browser runtime, and the specifications. If you are an agent
working *in* a Jikko workspace rather than *on* Jikko, read
[agent.md](agent.md) instead.

Normative documents: [specification-v1.md](specification-v1.md) for the
source format and semantics, [identity-permissions.md](identity-permissions.md)
for identity, authentication boundaries, and authorization,
[permission-semantics-v1.md](permission-semantics-v1.md) for capability
meanings, [architecture-browser.md](architecture-browser.md) and
[ux-plan.md](ux-plan.md) for the browser and product decisions, and
[implementation-v1.md](implementation-v1.md) for what the reference harness
implements today.

## Core model

Jikko is an executable Markdown workspace for humans and agents. Markdown
files are the source of truth; the Go harness exposes the same semantics
through the CLI, the HTTP server, and the browser runtime.

```text
no type        -> Document
type: task     -> Task
type: view     -> View
type: identity -> Identity
```

Document = information, composition, and durable discussion surface. Task =
explicit actionable semantics. View = pure query + presentation hints.
Identity = an addressable individual or group.

> The files describe what things are and explicitly relate them. The harness
> determines what can be done with that information.

Never create a second source of truth. Claim, checkpoint, evidence, decision,
and event are useful concepts, but they are not additional primitives: express
them through Task metadata and content, comments, references, and history.

## Identity

There are no separate Human, Agent, Group, Team, Person, or Role primitives.
An Identity without `members` is an individual; one with `members` is a group
of identities. Groups may contain groups; membership is transitive and cycles
must be detected. Reverse membership is derived; do not duplicate it onto
member files.

Identity is not authentication. Credentials and secrets never go into Identity
Markdown. The harness authenticates a caller (today: a bearer token whose hash
is in `.auth.md`) and maps it to exactly one individual Identity. A group can
never be the authenticated caller.

`@mention` means attention; `assignee:` means responsibility. A group mention
stays a reference to the group and is never expanded into its members.
Mention indexes, inboxes, and delivery state are derived runtime behaviour.

## Permissions

Any page may carry a `permissions` mapping of `read`, `comment`, `write`, and
`admin` to one Identity or a list. Transitive membership satisfies grants. No
`permissions` means unrestricted by Jikko. A policy that is present but cannot
be evaluated denies everyone until it is fixed.

`write` must not silently grant the ability to change `permissions`. Every
mutation is judged by its effect: it is rejected if it introduces a workspace
problem, raises anyone's effective capability on a file the actor may not
administer, removes a restricted file's policy without admin, or leaves a
restricted file with no administrator. Do not add deny rules, folder
inheritance, role hierarchy, permission inheritance, or implicit ACL
propagation without demonstrated need.

Jikko ACLs govern harness operations; they do not sandbox actors with direct
filesystem or Git write access.

## Source and derived state

Authored data: Markdown, YAML frontmatter, `[[links]]`, `![[embeds]]`,
Identity membership, permissions, inline unresolved comments, `@mentions`.

Derived data: backlinks, reverse membership, search and mention indexes,
rendered views, caches, notification-delivery state. Persistent `.data`, if
used, must remain disposable and reproducible. `.auth.md` is harness-local
security state: neither source nor disposable.

Git is the history, diff, and three-way-merge substrate. Git does not replace
Jikko semantic operations: rename rewriting, task mutation, comment handling,
uploads, and views stay in the core.

## Comments

Comments are unresolved review state inside the Markdown they discuss:

```md
This should <!--comment:c17-->update references<!--/comment:c17-->.

<!--comment-thread:c17
@codex: Please verify links and embeds.
-->
```

Resolving a comment removes its markup and thread after the decision is
incorporated; Git keeps the discussion. Never silently delete or orphan an
unresolved comment. The semantic operations (`AddComment`, `ReplyComment`,
`ResolveComment`) are the only writers of this markup.

## Mutations

The Go core owns parsing, indexing, reference resolution, backlinks,
membership, mentions, authorization, queries, safe mutations, uploads, rename
rewriting, comments, merge and conflict semantics, and history. CLI, HTTP,
and WASM are adapters and must not grow semantics of their own.

Every mutation goes through `stage`: the proposed workspace is built in
memory with the changed files in place, judged against the current one, and
only then written atomically. A mutation names its operation (`set-metadata`,
`comment-add`, `rename`, ...), carries the actor, and, when `AutoCommit` is
on, is recorded as one Git commit limited to the files it touched with
`Jikko-Actor` and `Jikko-Operation` trailers. The Git author identity is the
user's; the actor lives in the trailer. `.auth.md` is never staged.

Mutations use optimistic revision checks: a page that changed after it was
read is refused, or, for whole-page saves, merged three ways with a structured
conflict when regions overlap. Do not add CRDT/OT infrastructure without
demonstrated need. Do not write `updated_by` into pages.

## Browser

```text
Go       = state + semantics
Markdown = durable data
HTML     = interactions
HTMX 4   = requests + swaps
SSE      = default server -> browser live updates
hx-live  = tiny local-only browser behaviour
Basecoat = presentation
```

Editing is source-first, not WYSIWYG: render when reading, expose real
Markdown in the active edit context. Embedded Markdown is visibly bordered
with its `![[file]]` anchor exposed. Use HTML/CSS first, then `hx-live`; add
browser runtime only when accessibility or correctness justifies it. The
Markdown renderer never passes raw HTML through.

## Development rules

- Composition, not inheritance. Views stay pure. Folders have no semantic
  meaning. Prefer readable paths over mandatory UUIDs. Preserve unknown
  metadata and the exact bytes of everything a mutation does not touch.
- Do not add Project, Sprint, Dashboard, Human, Agent, Group, Team, Role,
  Message, Chat, Comment, database, daemon, WebSocket, persistent index, CRDT,
  LFS, or another primitive or dependency without a concrete use case.
- Every feature comes with tests, including its denial paths: the wrong
  capability, an anonymous caller, a stale revision, a path outside the
  workspace.
- Human-readable output is the default; machine-facing commands provide
  deterministic `--json`. Keep documented output stable, and update the docs
  and the tests together when it changes. `TestQuickstart` runs the README's
  quickstart, so changing one means changing both.
- Errors name what was wrong without disclosing what the caller may not know:
  a forbidden page fails exactly like a missing one.

Before every commit:

```sh
gofmt -l .                       # must print nothing
go vet ./...
go test -race ./...
GOOS=js GOARCH=wasm go build -o /dev/null ./cmd/jikko-wasm
```

Commit messages have a short imperative subject and a body that explains why.
