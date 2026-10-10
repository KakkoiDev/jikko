# 00 — Overview

## Purpose

Use a local model as a tool-using helper for small software projects, with
enough observability to understand what it did and why, and with Jikko as the
durable record of the work.

These specs describe the system independently of the agent harness that runs
it. The current implementation uses Pi ([../pi/README.md](../pi/README.md)).
Replacing Pi means re-implementing [04-pi-mapping](04-pi-mapping.md) and
nothing else.

## Vocabulary

| Term | Meaning here |
|---|---|
| agent harness | The loop that sends context to a model, executes the tools it calls, and repeats until the model stops. Pi today. |
| Jikko runtime | The Go program in this repository. The rest of the repo calls it "the harness"; these specs never do. |
| model runtime | The process serving the model over HTTP. Ollama today. |
| run | One task given to the agent harness, from first prompt to final answer. Has one session file. |
| turn | One model response plus the tool calls and results it produced. |
| identity | A Jikko Identity the agent authenticates as. |

## Goals

1. **Experiment cheaply.** One command runs a task against a chosen model and
   produces a complete log. Comparing two models on one task is two commands.
2. **See everything.** Every run records the system prompt, the context sent to
   the model, the model's thinking, each tool call with its arguments and
   result, token usage, and timings. Nothing is summarized away.
3. **Analyze later.** Logs are plain JSONL with a documented schema, readable
   without the harness that wrote them.
4. **Durable work record.** Tasks come from Jikko and plans, progress, decisions
   and results go back to Jikko, so another human or agent can continue from
   the workspace without the agent's context window.
5. **Fully local.** Model, harness, workspace, and logs all run on this machine
   with no network dependency once installed.

## Non-goals

- Competing with cloud coding agents on large codebases. The target is small,
  well-bounded projects where a 20 to 30B-class model is reliable.
- Multi-agent orchestration. One identity, one task, one run at a time.
- Sandboxing the model from the machine. The model runs shell commands in a
  working directory with the user's permissions. Experiments assume a machine
  where that is acceptable.

## Document map

| Spec | Covers |
|---|---|
| [01-model-runtime](01-model-runtime.md) | What the model endpoint must provide, which models qualify, measured baselines. |
| [02-observability](02-observability.md) | What is logged, the event schema, retention, analysis questions. |
| [03-jikko-integration](03-jikko-integration.md) | Identity, context loading, what goes back to Jikko, the inbox loop. |
| [04-pi-mapping](04-pi-mapping.md) | How each requirement above maps onto Pi's configuration and extension API. |
| [05-experiments](05-experiments.md) | The experiment protocol: task corpus, metrics, comparison procedure. |

## Status

| Area | State (2026-10-10) |
|---|---|
| Model runtime | Working. gemma4:26b and gpt-oss:20b pass the tool round-trip test on this machine. |
| Observability | Prototype proven in Python ([../runs/prototype](../runs/prototype)); Pi implementation in progress. |
| Jikko integration | Implemented: `pi/jikko.ts` tools and pre-load, `jikko identity <name> start` loop, `jikko web`. First end-to-end run on 2026-10-10. |
| Experiments | Protocol drafted; one prototype run recorded. |
