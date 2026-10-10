#!/bin/sh
# Run Pi with the experiment configuration (spec/04-pi-mapping.md).
#
#   run.sh -p "task text"     one task, non-interactive, exits when done
#   run.sh                    interactive session, same configuration
#
# Environment:
#   PI_MODEL      provider/model   default ollama/gemma4:26b
#   PI_THINKING   off|minimal|low|medium|high   default medium
#
#   JIKKO_DIR, JIKKO_TOKEN, JIKKO_IDENTITY, JIKKO_TASK, JIKKO_BIN
#                 when JIKKO_DIR and JIKKO_TOKEN are set the Jikko extension is
#                 active: workspace tools, task pre-load, commit at run end
#                 (spec/03-jikko-integration.md). `jikko identity <name> start`
#                 sets them and calls this script once per task.
#
# Pi works in the current directory, so cd into the project sandbox first.
# Session files land in agent-harness/runs/pi/, telemetry beside them.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
RUNS=$(cd "$HERE/../runs" && pwd)/pi
mkdir -p "$RUNS"

MODEL=${PI_MODEL:-ollama/gemma4:26b}
THINKING=${PI_THINKING:-medium}

# --no-context-files: AGENTS.md/CLAUDE.md discovery would add uncontrolled prompt
#   text and make runs incomparable.
# --no-extensions + -e: load only the observer, not whatever is in ~/.pi.
# --no-skills/--no-prompt-templates: same reason.
JIKKO_ARGS=""
if [ -n "${JIKKO_DIR:-}" ] && [ -n "${JIKKO_TOKEN:-}" ]; then
  JIKKO_ARGS="-e $HERE/jikko.ts --append-system-prompt $HERE/jikko-prompt.md"
fi

# shellcheck disable=SC2086
set -- pi \
  --model "$MODEL" \
  --thinking "$THINKING" \
  --session-dir "$RUNS" \
  --no-extensions -e "$HERE/observer.ts" $JIKKO_ARGS \
  --no-context-files --no-skills --no-prompt-templates --no-approve \
  "$@"

exec "$@"
