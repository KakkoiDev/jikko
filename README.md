# Jikko

**Define, discuss, track, prove, and audit work in plain Markdown — for humans and agents.**

Jikko is a human-readable workspace where humans and agents define, discuss, track, prove, and audit work together without locking the source into a particular application.

Jikko is the authoritative place for the work. Humans and agents are expected to read the relevant Jikko context before acting and to return goals, discussions, decisions, progress, and evidence to Jikko. External conversations may help, but they are not project knowledge until recorded in the workspace.

A workspace is ordinary files. YAML frontmatter adds explicit semantics, `[[links]]` express relationships, `![[embeds]]` compose files, inline comments keep unresolved review attached to source, and `@mentions` address humans, agents, and groups. Git is the reference harness substrate for audit/history and three-way text merge.

## Install

With Go 1.25 or later:

```sh
go install github.com/KakkoiDev/jikko/cmd/jikko@latest
```

With Homebrew (macOS or Linux), once the tap exists:

```sh
brew install KakkoiDev/tap/jikko
```

Or download a prebuilt binary for Linux, macOS (amd64, arm64), or Windows
(amd64) from the [Releases page](https://github.com/KakkoiDev/jikko/releases)
and verify it with `sha256sum -c checksums.txt --ignore-missing`.

`jikko version` prints the release you have. Releases are built from tags by
`.github/workflows/release.yml`; see [docs/RELEASING.md](docs/RELEASING.md).

## Quickstart

Every command below runs inside the workspace directory; pass `--dir PATH`
to run it from elsewhere. Commands that act as somebody read `JIKKO_TOKEN`
(or `--token`). The whole sequence is run by `TestQuickstart` in
`cmd/jikko/main_test.go`, so it stays true.

### 1. Create a workspace

`init` makes the directory, runs `git init`, ignores `.auth.md` and `.data/`,
writes your identity page, a welcome page, and an `open-work` view, and prints
a token for you on standard output. It never overwrites a file, so it can be
run again to complete a workspace. Omit `--identity` to use your user name.

```sh
export JIKKO_TOKEN="$(jikko init --dir my-work --identity alice)"
cd my-work
jikko tree
```

### 2. Define and track work

Pages are created, changed, and committed through Jikko so references stay
intact and history says who did what. `--commit` records each change as one
Git commit carrying `Jikko-Actor` and `Jikko-Operation` trailers.

```sh
jikko create tasks/first --type task --body '# First task' --commit
jikko set tasks/first assignee alice --commit
jikko set tasks/first status doing --commit
jikko view open-work
jikko log
```

### 3. Serve it

```sh
jikko serve --auto-commit
```

Open the address it prints (`http://127.0.0.1:8080` by default) and log in
with your token. Every page has a rendered view with its properties, backlinks,
and discussion, and those who may change it get a fields-and-body editor, a raw
source editor, and comment forms. `--auto-commit` records browser changes the
way `--commit` does on the command line. The server binds to localhost and has
no transport security of its own; put it behind a TLS-terminating reverse proxy
and pass `--behind-proxy` to reach it from elsewhere.

### 4. Bring in an agent

An agent is an ordinary individual identity with its own credential. Address it
with a mention or assign it work:

```sh
jikko create codex --type identity --body '# Codex' --commit
jikko set tasks/first assignee codex --commit
jikko comment add tasks/first 'First task' '@codex please draft the plan.' --commit
export JIKKO_TOKEN="$(jikko auth create codex)"
```

The agent's loop is: find what addresses it, read before acting, act through
semantic operations, and prove it with commits. See [agent.md](agent.md).

```sh
jikko mentions --json
jikko list --assignee me --json
jikko show tasks/first --json
jikko comment reply tasks/first c1 'Drafting now.' --commit
jikko comment resolve tasks/first c1 --commit
jikko set tasks/first status done --commit
jikko log tasks/first
```

`check` would have warned had the task been closed with the thread still
open: completion is the author's call, but what is unmet is always surfaced.

`mentions --since <rev>` limits the loop to pages changed since a commit, and
`auth list` / `auth revoke <identity> --id <id>` manage the agent's credentials.

## Working with pages

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
assignee: codex
---

# Implement session persistence

@codex follow [[session-design]].
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
jikko list --type task
jikko list --type task --status todo --json
jikko show session-design --json
jikko check
```

A view is a saved query. `jikko view` evaluates it over the pages you may read:

```md
---
type: view
filter:
  type: task
  status: [todo, doing]
view:
  layout: board
  group: status
  sort: due
---
```

```sh
jikko view open-work --json
```

Tasks can also name their responsibility, dependencies, and evidence with
`assignee`, `depends_on`, `blocked_by`, `proof`, and `requires`; `check`
validates those references and warns when a Task is done before its
dependencies or without what it requires (see specification §2.2).

`--json` is intended for agents/scripts. Markdown remains source of truth regardless of whether a human, browser, or agent edits it.

`create` takes a body on the command line, from a file, or from standard
input, and composes the frontmatter of a task, view, or identity:

```sh
jikko create notes/talos --file notes.md
printf '# Talos\n\nNever forget.\n' | jikko create memory/talos --file -
jikko create views/mine --type view --body 'filter: {type: task, assignee: alice}'
```

Authenticated callers can make structured edits. A metadata mutation rewrites
only the property it names, leaving key order, comments, YAML types, and the
Markdown body untouched:

```sh
jikko set session-design status doing
jikko perm session-design read alice maintainers
```

`set` refuses to touch `permissions`, because access-control policy is a
mapping rather than a value; `perm` edits it. Both are judged by their effect:
a change is rejected if it introduces a workspace problem, widens anyone's
access beyond what you may administer, or leaves a restricted file with no
administrator.

Pages are renamed and deleted through Jikko so references stay intact:

```sh
jikko rename session-design architecture/sessions
jikko rename session-design old --no-rewrite
jikko delete old-notes
jikko delete former-member --prune
```

`rename` rewrites every `[[link]]`, `![[embed]]`, and, for an identity, every
group membership, access policy, assignee, and `@mention` that pointed at the
page, and reports references whose meaning changes. `--no-rewrite` only moves
the file and lists what breaks. `delete` refuses to drop an identity that a
group or an access policy names unless `--prune` removes those entries, and
reports links it leaves dangling.

Files are uploaded through the same operation from the CLI and the browser,
up to a configurable maximum size (25 MiB by default):

```sh
jikko upload ./diagram.png --into architecture
jikko upload ./recording.mp4 --as media/demo.mp4 --max-upload 50M
```

Whole-page edits carry the revision they were based on. When the page changed
meanwhile, Jikko merges the two edits three ways -- frontmatter key by key, the
body line by line -- using the base revision you send or finds in Git history,
and returns a structured conflict instead of overwriting when they overlap:

```sh
jikko show plan --raw > plan.md   # prints the rev on stderr
cp plan.md plan.base.md && $EDITOR plan.md
jikko save plan --rev <rev> --file plan.md --base plan.base.md --json
```

`check` reports unresolved references together with workspace problems —
malformed frontmatter, unknown types, views carrying a body or an unusable
definition, broken work-model references, membership cycles,
and access policies naming identities that do not resolve. A problem is
reported rather than fatal, so one bad file never makes the rest of a workspace
unreadable, but a file whose access policy cannot be evaluated is denied to
everyone until it is fixed. `check` and `serve` also warn when the workspace
is not Git-backed, since history, audit, and three-way merge then have nothing
to work from.

### History

Git is the history substrate (specification §10). `jikko commit <message>`
records every current change; `--commit` on a mutating command, and
`serve --auto-commit`, record each semantic mutation as its own commit limited
to the files it touched, so an upload and the embed it inserts are one history
event. Commits carry `Jikko-Actor` and `Jikko-Operation` trailers and keep
your Git author identity; `.auth.md` is never staged. `jikko log [page]` reads
the trailers back, following a page across renames:

```sh
jikko commit 'Import the Q4 plan' --operation import
jikko upload ./chart.png --into plan --commit
jikko log plan --json
```

### Browser/WASM and portable workspaces

Jikko's Go core is also a browser target:

```sh
GOOS=js GOARCH=wasm go build -o jikko.wasm ./cmd/jikko-wasm
```

The browser adapter exposes the same permission-aware semantics (`tree`, `read`, `readMany`, `mentions`, `view`, `render`, `check`) while OPFS supplies durable browser storage. It runs in single-user local mode: the host application binds the identity it vouches for when it opens the workspace. Browser storage is an adapter concern; Markdown remains the workspace model.

Portable export is first-class in the core and CLI:

```sh
jikko export --dir /path/to/workspace --output workspace.zip
```

The ZIP contains source and assets but deliberately excludes Git internals, derived `.data`, and credentials in `.auth.md`. The browser API can produce/download the same ZIP. See [browser/README.md](browser/README.md).

> **Current limitation:** upload progress, drag and drop, and paste; Mermaid diagram rendering (source is shown), and live in-place editing of embedded pages are roadmap work. An open page or editor is told once when its page changes on disk; it is not refreshed in place.

## Agent workspace operations

Agents should discover context explicitly rather than receiving a heuristic context dump. Jikko exposes the permission-filtered workspace and lets the authenticated model decide what to read:

```sh
jikko tree --json
jikko mentions --since HEAD~5 --json
jikko list --assignee me --type task --status todo --json
jikko show identity/cassian strategy/current intelligence/malrec --json
printf '# Talos\n\nNever forget.\n' | jikko create memory/talos --file - --commit
jikko create tasks/defend-sol --type task --body '# Defend Sol' --commit
jikko comment add tasks/defend-sol 'Defend Sol' 'Which fleet?' --commit --json
jikko comment list --json
```

`tree` never includes pages the authenticated identity cannot read, so it does not disclose forbidden paths. `show` accepts one or many references for efficient batch retrieval. `mentions` derives direct and transitive group mentions from Markdown and reports the line and comment thread of each; no duplicate inbox is stored. `--since <rev>` narrows it to pages changed since a Git revision (a workspace without Git lists every mention and says so). Creation remains source-first Markdown and is validated before the workspace is refreshed.

Identity is authentication context, not an automatic prompt dump: the harness tells an agent which Identity it authenticated as, while the agent may read that Identity page when it needs its authored biography, role, or other knowledge.

Git-backed workspaces record each semantic mutation as an attributed commit with `--commit`, or a logical batch with `jikko commit`:

```sh
jikko commit --operation agent-turn 'Cassian turn 428'
```

The commit carries `Jikko-Actor` and `Jikko-Operation` trailers. Git remains optional and Markdown remains authoritative. See [agent.md](agent.md) for the full agent guide.

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

The browser harness stays server-first: HTMX 4 for requests/swaps, hx-live for tiny local behavior, SSE by default for server-to-browser updates, and Basecoat CSS for presentation. HTMX 4.0.0 and Basecoat 1.0.2 CSS are vendored/embedded, so runtime does not require CDN/npm/network access.

See [architecture-browser.md](architecture-browser.md) for browser architecture, [agent.md](agent.md) for AI-agent orientation, [ux-plan.md](ux-plan.md) for product/collaboration decisions, and [specification-v1.md](specification-v1.md) for source semantics.

## Design rule

> **The files describe what things are and explicitly relate them. The harness determines what can be done with that information.**

## Status

Jikko is an early specification and reference implementation. The design should continue to be tested against substantially different workflows before adding more primitives.
