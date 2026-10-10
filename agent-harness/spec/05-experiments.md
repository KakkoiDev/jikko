# 05 — Experiments

## Protocol

An experiment is one question, one task set, one or more configurations, and
the summary rows that answer it.

1. **State the question** in one sentence. Examples: "Does gpt-oss at low
   reasoning finish the task set as reliably as gemma4 at medium?", "Does
   pre-loading the Jikko task page reduce turns compared to letting the model
   fetch it?"
2. **Pick tasks** from the corpus below, or add one to it. A task is a page in
   the Jikko workspace with a design document it links to, so the same text is
   used every time.
3. **Fix everything else.** Same model, thinking level, context length,
   system prompt, tool set, and a fresh sandbox directory per run. `pi/run.sh`
   disables context files, skills, and prompt templates for this reason.
4. **Run each configuration at least three times.** Local models are sampled;
   one run proves nothing.
5. **Record** the summary rows and the answer in a Jikko document that links
   the task pages and names the session files.

## Task corpus

Small, bounded, self-verifying. Every task ends with "run the tests".

| Task | Shape | Exercises |
|---|---|---|
| wc CLI | single file plus tests | baseline: write, run, report |
| CSV to JSON converter with a schema check | two files, error handling | reading a spec document, edge cases |
| Fix a failing test in a provided 5-file package | given code, one bug | reading before writing, not over-editing |
| Add a subcommand to an existing CLI | given code, extension | consistency with existing style |
| Small HTTP service with two endpoints and tests | 3 to 4 files, a dependency | multi-file coherence, running a server in tests |

Hard limits that make a task unsuitable: more than roughly 10 files to read,
dependencies that need network at run time, anything needing more than 64K of
context.

## Metrics

Primary, per run:

- **outcome**: completed with passing tests / completed with failing tests / aborted / hit the turn limit
- **turns** and **tool calls**
- **wall seconds**

Secondary:

- thinking characters and reasoning tokens as a share of output
- tool errors and what followed them
- context size at the last turn, and whether compaction happened
- wasted turns (definition in [02-observability](02-observability.md))

Comparisons use medians across the repeated runs, with the individual rows
kept.

## Configurations worth testing first

1. gemma4:26b medium thinking (the default) versus gpt-oss:20b medium: the
   two models that pass the tool test today.
2. gemma4:26b with thinking on versus off: does thinking buy correctness or
   only tokens on these tasks?
3. qwen3-coder:30b, once it passes the tool round-trip test, on the two
   coding-heavy tasks.
4. Jikko context pre-loaded in the first message versus fetched by the model
   via tools, once [03-jikko-integration](03-jikko-integration.md) is
   implemented.

## Record so far

| Date | Harness | Model | Task | Outcome | Turns | Wall | Session |
|---|---|---|---|---|---|---|---|
| 2026-10-10 | python prototype | gemma4:26b | wc CLI | tests pass | 7 | 88 s | runs/prototype/20261010-180158-74baf6.jsonl |
| 2026-10-10 | pi 1.1 | gemma4:26b medium | wc CLI | tests pass (4) | 15 | 104 s | runs/pi/2026-10-10T10-17-03-523Z_01a12550-….jsonl |
| 2026-10-10 | pi 1.1 + jikko | gemma4:26b medium | wc CLI via `jikko identity gemma start` | tests pass (7); plan + result comments, status done, 1 commit | 28 | 172 s | runs/pi/…_01a12573-….jsonl |

First comparison between harnesses on the same task: Pi's run used twice the turns, two of them tool errors (`cat -A` does not exist on macOS; running the CLI with no arguments), and spent more turns verifying by hand. Same outcome. The prototype's system prompt told the model to stop once tests pass; Pi's generic prompt does not. Candidate for a prompt experiment.

First Jikko-integrated run: the model read the task and the linked design with
`jikko_show` before anything else, posted a plan comment, fixed a failing test,
posted a result comment with the test output, set the status to done, and
resolved its own plan comment. Six tool errors: two comments rejected for
containing blank lines (the tool now folds them), two `edit` mismatches, one
intentional failing command, one failing test it then fixed. It skipped setting
`doing` at the start; the prompt now says so explicitly.
