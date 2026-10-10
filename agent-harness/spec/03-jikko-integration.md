# 03 — Jikko integration

Status: implemented (Pi extension `pi/jikko.ts`, runner `pi/run-task.sh`,
Jikko commands `web` and `identity`). Checked against the seed workspace in
`~/Code/jikko-workspace` on 2026-10-10.

## Principle

Jikko's [agent guide](../../agent.md) already says what an agent must do:
read the relevant pages before acting, keep discussion in comments and
documents, record goals, decisions, progress, blockers and evidence as the work
proceeds, and never create a second source of truth. The agent harness is the
thing the guide calls "the harness": it authenticates the model as an Identity,
exposes workspace operations as tools, and makes sure the run's outcome lands in
the workspace.

Two records exist and they do not overlap:

| | Jikko workspace | Run logs ([02](02-observability.md)) |
|---|---|---|
| Audience | Humans and the next agent | The experimenter |
| Content | Task, plan, progress, decisions, evidence, result | Thinking, every message, every tool call, tokens, timings |
| Volume per run | A few comments and one commit | Tens of KB to MB |
| Link between them | The commit message names the session id | The telemetry `session_start` event names the workspace and task page |

Thinking never goes into Jikko. It is noise to a human reader and it is not a
decision until the model acts on it.

## Identity and authentication

- The model runs as one Identity page, for example `identity/gemma.md`, a
  member of the `identity/agents.md` group. The page carries the model's
  authored role description; the harness does not inject it into the prompt.
- The harness holds a credential created with `jikko auth create <identity>`
  and passes it as `JIKKO_TOKEN` to every CLI call. Tokens live in the
  workspace's `.auth.md`, which is gitignored.
- The harness tells the model only *which* identity it is. The guide is
  explicit that identity is authentication context, not a prompt dump.

## Context loading

The system prompt is small and static. Per run, the harness sends a first user
message that contains:

1. "You are `<identity>` in the Jikko workspace at `<dir>`."
2. The task page, verbatim, from `jikko show <task> --json` (body, metadata,
   links).
3. Every page the task links to, verbatim, up to a budget of roughly 8K tokens.
   Beyond that, titles only, and the model fetches with the show tool.
4. A condensed version of the agent guide's rules: read before acting, comment
   as you go, record decisions, finish with evidence, do not invent file types.

Why pre-load rather than let the model discover everything: a 26B model will
skip reading if reading is optional. Experiment 4 in
[05-experiments](05-experiments.md) tests this choice.

The model may fetch more at any time with the tree, show, mentions and view
tools. `tree` and `show` are permission-filtered by the Jikko runtime, so the
model only ever sees what its identity may read.

## Tools exposed to the model

Thin wrappers over the CLI, always with `--json` where the command supports it.
Arguments mirror the CLI exactly so the mapping is obvious and portable.

| Tool | CLI | Purpose |
|---|---|---|
| `jikko_tree` | `jikko tree --json` | Discover readable pages. |
| `jikko_show` | `jikko show <ref>... --json` | Read one or more pages. |
| `jikko_mentions` | `jikko mentions --json` | The identity's inbox. |
| `jikko_view` | `jikko view <ref> --json` | Evaluate a saved query. |
| `jikko_comment` | `jikko comment add\|reply\|list ...` | Plan, progress, questions, result. Resolving is the reviewer's act and is not exposed. |
| `jikko_set` | `jikko set <ref> <prop> <value>` | Task status transitions. |
| `jikko_create` | `jikko create <path> --type --body` | New document or task, when the task calls for one. |
| `jikko_save` | `jikko save <ref> --file - --base` | Edit a page body with Jikko's merge, not the harness's file-write tool. |

Not exposed: `perm`, `auth`, `delete`, `rename`, `commit`. The first three are
administrative. `commit` is called by the harness at the end of the run, not by
the model, so every run produces exactly one commit.

Eight workspace tools plus the harness's four code tools is twelve. The
prototype handled eleven schemas without confusion; the tool-error column in
the run summary will show if this count is too many for a given model.

## What goes back, and when

| Moment | Operation |
|---|---|
| Run start | `set <task> status doing` |
| After reading, before writing | A comment on the task with the plan: files to create, tests to write, open questions addressed to the task's author with an `@mention`. |
| Each material step | A comment: what was done, what the tests said. Short. |
| Blocked | A comment with the question, `set <task> status blocked`, and the run ends. The inbox loop picks it up again when someone replies. |
| Done | A comment with the result and evidence: test output, file list, how to run. `set <task> status done`. |
| Run end (harness, not model) | `jikko commit --operation agent-turn "<identity>: <task title> (session <id>)"` carrying the `Jikko-Actor` and `Jikko-Operation` trailers. `jikko check` afterwards; a problem is reported in the run log and left for a human. |

The sandbox project directory where the code is written is not inside the
workspace. The result comment links to it by path; a later iteration can
`upload` artifacts into the workspace when that is useful.

## Inbox loop: `jikko identity <name> start`

The loop lives in the Jikko runtime so that starting an agent is one command:

```sh
jikko identity gemma start --dir <workspace> --runner agent-harness/pi/run-task.sh
jikko identity gemma status --dir <workspace>     # what start would run
jikko web --dir <workspace>                       # serve and open the browser
```

`start` lists task pages with `status: todo` whose `assignee` is the identity
or a group it belongs to, and runs the **runner** once per task, one at a time,
then sleeps `--interval` (default one minute) and looks again; `--once` does a
single pass. It mints a credential for the identity unless `--token` is given.

The runner contract is the only coupling between Jikko and an agent harness.
The runner is any executable, started with the task's project directory as
working directory (`<sandbox>/<task stem>`, default sandbox
`<workspace>/.data/work/<identity>`) and the environment:

| Variable | Value |
|---|---|
| `JIKKO_DIR` | workspace directory |
| `JIKKO_TOKEN` | credential for the identity |
| `JIKKO_IDENTITY` | identity name |
| `JIKKO_TASK` | task reference, e.g. `tasks/wc-cli` |
| `JIKKO_BIN` | the jikko binary that started it |

`pi/run-task.sh` is the Pi runner: it calls `pi/run.sh -p` with a one-line
prompt; the extension does the rest. A port to another harness replaces this
one script.

This makes Jikko a durable queue between a human and the local model. A task
blocked on a question resumes when the human replies in the comment thread and
sets the status back to `todo`.

## Constraints found while validating

- `jikko serve` renders pages at `/p/<path>`, so result comments can link to
  `http://127.0.0.1:8080/p/tasks/wc-cli` for a human reader.
- Credentials are per workspace. A second workspace needs its own
  `auth create`.
- `commit` requires a Git-backed workspace; initialize experiment workspaces
  with `git init` and commit the seed pages before the first run.
- The Jikko binary is a plain Go build. Until Go is on the system profile
  (declared in nix-config, pending a rebuild), build it with
  `nix shell nixpkgs#go --command go build -o jikko ./cmd/jikko`.
