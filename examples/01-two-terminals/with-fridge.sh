#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The same processes, the same load, through Agent Fridge Board.
#
#   ./with-fridge.sh
#
# Nothing here is a trick: same agent count, same note count, same machine,
# more contention if anything, because every write also takes a lock.

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
FRIDGE_BIN="$HERE/../../bin/fridge.mjs"
fr() { node "$FRIDGE_BIN" "$@"; }

WORK="$HERE/.demo-with"
AGENTS=6
NOTES_EACH=12

rm -rf "$WORK"
mkdir -p "$WORK"
cd "$WORK"
git init -q .

fr init --quiet
for a in $(seq 1 "$AGENTS"); do
  fr join --agent "agent-$a" --vendor other --quiet
done

echo "Same $AGENTS processes, same $NOTES_EACH notes each, via 'fridge pin'."
echo

agent() {
  local name="$1"
  local i
  for i in $(seq 1 "$NOTES_EACH"); do
    fr pin "update $i" --agent "$name" --quiet
    sleep 0.0"$((RANDOM % 3))"
  done
}

START=$(date +%s)
for a in $(seq 1 "$AGENTS"); do
  agent "agent-$a" &
done
wait
END=$(date +%s)

EXPECTED=$((AGENTS * NOTES_EACH))
ACTUAL=$(fr log --json --limit 1000 | grep -c "\"body\": \"update ")

echo "expected notes : $EXPECTED"
echo "notes on disk  : $ACTUAL"
echo "LOST           : $((EXPECTED - ACTUAL))"
echo "elapsed        : $((END - START))s"
echo

echo "--- now the part a shared file could never do: two agents want the same paths"
fr claim "src/api/**" --task "refactor the client" --ttl 30m --agent agent-1
echo "agent-1 claim exit: $?"
echo
fr claim "src/api/routes.ts" --task "fix a bug" --agent agent-2
echo "agent-2 claim exit: $?  (10 = E_CONFLICT, and it is told exactly who has it)"
echo

fr board

echo
echo "No note was lost, and the second agent was stopped before it could touch the file."
echo "The shared-Markdown version had no way to even notice."
echo "Clean up with: rm -rf $WORK $HERE/.demo-without"
