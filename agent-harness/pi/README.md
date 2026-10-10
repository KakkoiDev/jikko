# Pi implementation

See [../spec/04-pi-mapping.md](../spec/04-pi-mapping.md) for why each piece exists.

| File | Role |
|---|---|
| `run.sh` | Runs Pi with the pinned experiment configuration. `run.sh -p "task"` for one task, `run.sh` for an interactive session. Invoke from the sandbox project directory. |
| `observer.ts` | Extension that writes per-turn telemetry (timings, context usage, tool errors) next to the session file. |
| `models.json.example` | The Ollama provider block for `~/.pi/agent/models.json`. nix-config installs the real one (`home/pi.nix`). |

Environment: `PI_MODEL` (default `ollama/gemma4:26b`), `PI_THINKING` (default `medium`).

Sessions: `../runs/pi/<timestamp>_<id>.jsonl` (with `--session-dir` Pi does not add the cwd-derived subdirectory), telemetry in `../runs/pi/telemetry/<id>.jsonl`.
Analyze with `../tools/pi_analyze.py`.
