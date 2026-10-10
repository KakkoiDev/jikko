#!/bin/sh
# Runner contract for `jikko identity <name> start` (spec/03-jikko-integration.md).
# Jikko calls this once per task with:
#   cwd              a project directory for the task (created by jikko)
#   JIKKO_DIR        workspace directory
#   JIKKO_TOKEN      a credential for the identity, valid for this run
#   JIKKO_IDENTITY   the identity name
#   JIKKO_TASK       the task page reference, e.g. tasks/wc-cli
#   JIKKO_BIN        the jikko binary that started the runner
# Exit status is reported by jikko; the run's record is in Jikko itself.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
exec "$HERE/run.sh" -p "Work on the Jikko task $JIKKO_TASK. The task page and its linked pages are in your first message."
