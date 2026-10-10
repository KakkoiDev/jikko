# 04 — Pi mapping

How the requirements in specs 01 to 03 are met with Pi 1.1. This is the only
spec that would be rewritten for another agent harness.

## Why Pi

- System prompt under 1,000 tokens and four built-in tools (read, write, edit,
  bash). Little hidden context, so a run's prompt is what we say it is.
- Sessions are JSONL with every message, thinking block, tool call, and usage
  record, kept in full even after compaction. Most of
  [02-observability](02-observability.md) comes for free.
- A TypeScript extension API with events for every point we need to observe
  and a `registerTool` call for the Jikko tools.
- Runs against any OpenAI-compatible endpoint via `models.json`, so Ollama
  needs no adapter.
- Installable from a Nix flake (`github:earendil-works/pi/stable`), which is
  how nix-config declares it.

## Model runtime (spec 01)

`~/.pi/agent/models.json`, managed by nix-config (`home/pi.nix`):

```json
{ "providers": { "ollama": {
    "baseUrl": "http://127.0.0.1:11434/v1", "api": "openai-completions", "apiKey": "ollama",
    "models": [ { "id": "gemma4:26b", "reasoning": true },
                { "id": "gpt-oss:20b", "reasoning": true },
                { "id": "qwen3-coder:30b" } ] } } }
```

Models are addressed as `ollama/<id>`. `reasoning: true` makes Pi send a
thinking level and store the `reasoning` field Ollama returns as thinking
blocks. Pi shows these models with a 128K context and 16K max output by
default; Ollama's own `num_ctx` still decides the real window, so the server
is started with `OLLAMA_CONTEXT_LENGTH` when a run needs more than 32K.

## Observability (spec 02)

| Requirement | Pi source |
|---|---|
| System prompt and changes | `message` entries with `role: system`, carrying `sections`, `toolsAdded`, `toolsRemoved`. Replay in order. |
| Task and messages | `message` entries, `role: user` / `assistant` / `toolResult`. |
| Thinking, separately | `assistant.content[]` blocks with `type: "thinking"`. |
| Tool calls and results | `type: "toolCall"` blocks (`id`, `name`, `arguments`) and `toolResult` messages (`toolCallId`, `toolName`, `content`, `isError`). |
| Token usage | `assistant.usage` (`input`, `output`, `reasoning`, `totalTokens`). |
| Context changes | `compaction`, `context_edit`, `branch_summary` entries. |
| Wall-clock, context as seen by the harness, tool errors as they happen | **Not in the session file.** Added by `pi/observer.ts`. |

The observer subscribes to `session_start`, `before_agent_start`,
`turn_start`, `turn_end`, `tool_result`, and `agent_end`, and appends to
`<session-dir>/telemetry/<session-id>.jsonl`. It changes nothing.

Canonical event mapping, implemented in `tools/pi_analyze.py`:

| Canonical | Pi |
|---|---|
| run | session header plus first user message; model from the first assistant message |
| turn | one assistant message and the toolResult messages that follow it |
| tool_call | one `toolCall` block joined to its `toolResult` by `toolCallId` |
| run_end | telemetry `run_end`, else last assistant `stopReason` |

Session files are kept under `agent-harness/runs/pi/` via `--session-dir`,
not in `~/.pi`, so experiments stay with the specs.

## Run configuration

`pi/run.sh` fixes the configuration per [05-experiments](05-experiments.md):

```
pi --model ollama/gemma4:26b --thinking medium
   --session-dir agent-harness/runs/pi
   --no-extensions -e agent-harness/pi/observer.ts
   --no-context-files --no-skills --no-prompt-templates
   [-p "task"]
```

`-p` runs one task and exits; without it the same configuration opens an
interactive session. Pi works in the current directory, so the script is
invoked from the sandbox project directory.

## Jikko integration (spec 03)

Planned as a second extension, `pi/jikko.ts`:

- `pi.registerTool()` for each of the eight workspace tools, each one
  spawning the `jikko` binary with `--dir`, `--json`, and `JIKKO_TOKEN` from
  the environment, returning stdout as the tool's text content and throwing on
  a non-zero exit so Pi marks the result as an error.
- `before_agent_start` to build the first message from the task page and its
  links, returning it as `message` so it is part of the transcript.
- `agent_end` to run `jikko commit` and `jikko check`.
- The inbox loop is a shell script around `pi -p`, not an extension, so that
  each task is one session file.

Alternative considered: exposing Jikko as an MCP server and using Pi's
built-in MCP support (`.pi/mcp.json`). Rejected for now because Jikko has no
MCP server and the CLI wrapper is a few dozen lines. If Jikko grows one, the
tool names and schemas in spec 03 should map one to one.

## What a port must reproduce

1. A provider configuration for Ollama.
2. The observer's six events, or equivalents, writing the telemetry schema.
3. An analyzer that maps the harness's session format to the canonical model.
4. The eight Jikko tools with the CLI-mirroring argument names.
5. A run script that pins model, thinking, session location, and disables
   every source of uncontrolled prompt text.
