# Agent harness

Experiments in using a local model as a tool-using helper, with full observability
and Jikko as the durable workspace.

Vocabulary: in this directory **agent harness** means the loop that drives a model
with tools (today: [Pi](https://pi.dev)). It is distinct from the **Jikko runtime**,
the Go program the rest of this repository calls "the harness".

```
agent-harness/
  spec/      stack-agnostic specifications. The implementation follows them;
             a port to another agent harness starts from them.
  pi/        the Pi implementation: extension, run scripts, config templates.
  tools/     analysis and benchmarking scripts (Python, no dependencies).
  runs/      run logs. prototype/ holds the pre-Pi Python prototype's logs.
```

## Quick start

Requirements: Ollama with `gemma4:26b` pulled, Pi 1.1+ (`npm i -g @earendil-works/pi-coding-agent`),
Python 3, and `~/.pi/agent/models.json` from [pi/models.json.example](pi/models.json.example).

```sh
# one task, non-interactive, full event log to stdout and a session file under runs/pi/
agent-harness/pi/run.sh -p "Create wc.py with tests and run them"

# interactive session with the same configuration
agent-harness/pi/run.sh

# summarize or trace runs
python3 agent-harness/tools/pi_analyze.py agent-harness/runs/pi/*.jsonl
python3 agent-harness/tools/pi_analyze.py --trace <session.jsonl>
```

Run the agent against a Jikko workspace (see spec 03):

```sh
jikko web --dir ~/Code/jikko-workspace                      # UI in the browser
jikko identity gemma start --dir ~/Code/jikko-workspace \
  --runner agent-harness/pi/run-task.sh                     # pick up todo tasks assigned to gemma
```

Read the specs in order: [spec/00-overview.md](spec/00-overview.md).
