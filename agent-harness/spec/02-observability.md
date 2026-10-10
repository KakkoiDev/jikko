# 02 — Observability

## What must be recorded

For every run, in order, with timestamps:

| Item | Why |
|---|---|
| The system prompt as sent, and every later change to it | Prompt experiments are meaningless without the exact text. |
| The task text and any attached files or images | The input. |
| Every message the model received, including tool results verbatim | "What context was it working on" is a question about these. |
| The model's thinking for every turn, separately from its answer | The main thing a human wants to read afterwards. |
| Every tool call: name, arguments, result, error flag, duration | Where runs go wrong. |
| Token usage per turn: input, output, reasoning share, context size after the turn | Context fill and cost of thinking. |
| Wall-clock per turn and per run | Speed is a first-class experiment variable. |
| Model id, thinking level, harness version, working directory | Reproducibility. |
| Compactions, context edits, branches | If the harness changes what the model sees, the log must say so. |

Nothing is truncated in the primary record. Analyzers truncate for display.

Caveat found in the first Pi run: Ollama reports only *uncached* prompt tokens
as input usage, so after the first turn the per-turn input figure is the delta,
not the context size. Context size must come from the harness (the observer's
`context` field), or be reconstructed by re-tokenizing the transcript.

## Storage

- One **session file** per run, JSONL, append-only, written by the agent
  harness. It is the primary record and is complete on its own.
- One **telemetry file** per run, JSONL, written by the observer, holding
  derived and timing data that the session file lacks. It is optional: an
  analyzer must work with only the session file, at reduced precision.
- Both live under `agent-harness/runs/<harness>/`, grouped the way the harness
  groups them. They are plain files; committing them is a per-experiment
  decision (see [05-experiments](05-experiments.md)).
- Retention: keep everything. Runs are small (tens of KB to a few MB) and the
  point of goal 3 is analysis later.

## Canonical event model

The analysis tools reason in these terms regardless of the harness's own file
format. A port must be able to produce them from its native log.

```
run        id, harness, model, thinking_level, cwd, task, started_at
turn       run, index, thinking, text, tool_calls[], usage{input,output,reasoning,total}, context_after, wall_ms, stop_reason
tool_call  run, turn, name, arguments, result, is_error, wall_ms
prompt     run, turn, system_prompt (full text on first turn, deltas after)
context_change   run, turn, kind (compaction|edit|branch), summary
run_end    run, turns, wall_ms, outcome (completed|aborted|error|max_turns)
```

The Python prototype in [../runs/prototype](../runs/prototype) wrote exactly this
shape. Pi's session format carries the same information under different
names; [04-pi-mapping](04-pi-mapping.md) gives the mapping and
`tools/pi_analyze.py` implements it.

## Analysis questions the logs must answer

The standard summary row per run:

```
session  model  turns  tool_calls  tool_err  in_tok  out_tok  think_chars  last_ctx  wall_s  stop
```

And the questions behind the experiments:

1. Did it finish, and did the result pass its own tests? (outcome, last tool results)
2. How much of the output was thinking versus action? (think_chars vs text, reasoning tokens vs output tokens)
3. Where did it waste turns? (consecutive turns with no tool call, re-reading the same file, re-running passing tests)
4. Which tool calls failed and what did the model do next? (tool_call.is_error followed by the next turn's thinking)
5. How fast did context fill, and did compaction happen? (context_after per turn, context_change events)
6. Same task, two models: every column above side by side.

A finding from the first prototype run, as an example of what to look for:
the model spent one 845-token turn manually counting words in its thinking to
verify a result the tool had already produced correctly.

## What is deliberately not logged

- Nothing is sent anywhere. Logs stay on disk.
- No screenshots or terminal recordings. The session file is the record.
- Thinking is not written into Jikko ([03-jikko-integration](03-jikko-integration.md)).
