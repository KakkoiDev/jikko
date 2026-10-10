# Using a Jikko workspace as an agent

This guide is for an AI agent that has been given access to a Jikko workspace
and is expected to do work in it. If you are changing Jikko itself, read
[CONTRIBUTING.md](CONTRIBUTING.md) instead.

A Jikko workspace is a directory of plain Markdown. It is the authoritative
record of the work: goals, discussion, decisions, progress, and evidence live
there, not in your context window. You read it before acting, you change it
through `jikko` commands, and you leave an attributed history behind. Another
human or agent must be able to continue from the workspace alone.

```text
no type        -> Document   information and durable discussion
type: task     -> Task       status, assignee, dependencies, proof
type: view     -> View       a saved query over pages
type: identity -> Identity   an individual (you) or a group
```

Every command below takes `--json` for deterministic output and `--dir PATH`
when you are not inside the workspace. `jikko help <command>` prints each
command's forms and flags.

## 1. Get a token

You act as an Identity. A human creates your identity page and a credential,
and gives you the token once:

```sh
jikko create codex --type identity --body '# Codex' --commit
jikko auth create codex
```

Keep it in `JIKKO_TOKEN` (or pass `--token`). The token maps to one individual
identity; groups cannot authenticate. Your identity page may hold a biography,
role, or standing instructions; read it with `jikko show codex` when you need
them. Never write the token into a page: `.auth.md` holds only hashes and is
kept out of Git.

```sh
export JIKKO_TOKEN=jk_...
jikko tree --json          # every page you may read: path, title, type
```

`tree` is permission-filtered, so a page it does not list does not exist for
you; asking for it fails exactly like asking for a missing page.

## 2. Find your work

Two queries drive the loop. Mentions are requests for your attention; the
assignee is responsibility.

```sh
jikko mentions --json
jikko mentions --since <rev> --json
jikko list --assignee me --type task --status todo --json
jikko list --assignee me --type task --status doing --json
```

`mentions` lists every readable page that addresses you or a group you belong
to, with each matching line (`line` is the line in the page's source) and the
id of the comment thread it is in, so you can reply to the right thread. With
`--since <rev>` only pages changed since that Git revision are listed; keep the
revision of your last turn (`jikko log --limit 1 --json`) and pass it on the
next. A workspace without Git cannot narrow the list: `--since` is then
ignored, every mention is listed, and a warning says so on standard error.

Views are the team's own queries:

```sh
jikko view open-work --json
```

## 3. Read before acting

```sh
jikko show tasks/first --json                  # title, metadata, body, links, backlinks, comments, rev
jikko show tasks/first design/plan --json      # several pages in one call
jikko show tasks/first --raw > first.md        # exact source; the rev is printed on stderr
jikko comment list tasks/first --json
jikko log tasks/first --json                   # who changed it, when, through which operation
```

Follow `[[links]]` and `![[embeds]]` that matter. Read the unresolved comments:
they are the open questions. Task metadata tells you what completion needs
(`requires`), what blocks it (`blocked_by`, `depends_on`), and what evidence it
already has (`proof`).

## 4. Act through semantic operations

Change pages through `jikko`, never with an editor, `mv`, or `rm`. The
operations keep references intact, preserve the rest of the file byte for
byte, check your permission, and refuse a change that would widen anyone's
access, introduce a workspace problem, or overwrite an edit you have not seen.

```sh
jikko set tasks/first status doing --commit
jikko set tasks/first proof 'commit:1a2b3c4d, [[load-test-results]]' --commit
jikko comment reply tasks/first c1 'Done; see [[results]].' --commit
jikko comment add design/plan 'retry budget' 'Three retries or five?' --commit
jikko comment resolve design/plan c2 --commit
jikko create notes/findings --type document --file - --commit < findings.md
jikko create tasks/follow-up --type task --body '# Follow up' --commit
jikko upload ./chart.png --into notes/findings --commit
jikko rename notes/findings reports/q4-findings --commit
jikko delete notes/scratch --commit
```

Whole-page edits are made against the revision you read, and merged three
ways if the page changed meanwhile:

```sh
jikko show design/plan --raw > plan.md         # note the rev on stderr
cp plan.md plan.base.md && edit plan.md
jikko save design/plan --rev <rev> --file plan.md --base plan.base.md --json --commit
```

A conflict writes nothing and returns, as JSON, each disagreeing field and
body region with `base`, `current`, and `yours`. Re-read, decide, and save
again with `--against <current rev> --resolve <id>=current|yours`. Never
overwrite another actor's edit blindly.

Capabilities: `read` lets you see a page, `comment` lets you add, reply to,
and resolve comments, `write` lets you change content and metadata, `admin`
lets you change `permissions` (with `jikko perm`). A refusal names what you
lack; ask a human for the grant rather than working around it.

Conventions that keep the workspace honest:

- Discuss in comments on the page concerned, not in a separate note.
- Record decisions, blockers, and evidence as you go, not only at the end.
- `@mention` for attention, `assignee` for responsibility. Do not rewrite a
  group mention into its members.
- Do not add `updated_by` or timestamps to pages; history carries attribution.
- Resolve a comment only after its decision is incorporated. `jikko check`
  warns when a Task is done while comments, dependencies, or requirements
  remain open.

## 5. Prove it

Every `--commit` records the change as one Git commit limited to the files it
touched, with `Jikko-Actor: <you>` and `Jikko-Operation: <operation>`
trailers. Use it on each mutation, or batch a turn with `jikko commit`:

```sh
jikko commit 'Draft the Q4 plan' --operation agent-turn
jikko log --limit 5
```

Cite evidence in the Task: a `proof` entry may be a page, a workspace file, a
URL, or `commit:<hash>`, and `jikko check` verifies that each resolves. Then
close the work:

```sh
jikko set tasks/first status done --commit
jikko check
```

## Command reference

```text
jikko tree [--json]                                   what you may read
jikko mentions [--since REV] [--json]                 what addresses you
jikko list [--type T] [--status S] [--assignee ID|me] [--json]
jikko view <view> [--json]
jikko show <ref>... [--json] | show --raw <ref>
jikko comment add|reply|resolve|list ... [--commit] [--json]
jikko set <ref> <property> <value> [--commit]
jikko create <path> [--type T] [--body MD | --file PATH|-] [--commit] [--json]
jikko save <ref> --rev REV [--file PATH|-] [--base PATH] [--commit] [--json]
jikko upload <file> [--into PAGE] [--as PATH] [--commit] [--json]
jikko rename <ref> <new path> [--commit] [--json]
jikko delete <ref> [--prune] [--commit] [--json]
jikko commit <message> [--operation NAME] [--json]
jikko log [page] [--limit N] [--json]
jikko check [--json]
```

Exit status 0 is success, 1 a refused or failed command, 2 a usage error. The
formats of the pages are defined in [specification-v1.md](specification-v1.md);
identity, authentication, and permissions in
[identity-permissions.md](identity-permissions.md).
