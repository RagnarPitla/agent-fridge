#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The old way. Two "agents" coordinate through one shared Markdown file, each
# doing read-modify-write. This is the pattern that lost 128 lines of work.
#
#   ./without-fridge.sh
#
# It does not use Agent Fridge at all. That is the point.

set -uo pipefail
cd "$(dirname "$0")"

WORK=".demo-without"
BOARD="$WORK/shared-development-updates.md"
AGENTS=6
NOTES_EACH=12

rm -rf "$WORK"
mkdir -p "$WORK"
printf '# Shared development updates\n\n' > "$BOARD"

echo "Two-plus agents, one shared Markdown file, read-modify-write."
echo "$AGENTS processes x $NOTES_EACH notes each = $((AGENTS * NOTES_EACH)) notes expected."
echo

# Each agent: read the whole file, append its line, write the whole file back.
# Exactly what an agent does when you tell it to "update the shared file".
agent() {
  local name="$1"
  local i
  for i in $(seq 1 "$NOTES_EACH"); do
    local current
    current="$(cat "$BOARD")"                  # read
    local line="- [$name] update $i"           # modify
    printf '%s\n%s\n' "$current" "$line" > "$BOARD"   # write back, whole file
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
ACTUAL=$(grep -c '^- \[agent-' "$BOARD" || true)
LOST=$((EXPECTED - ACTUAL))

echo "expected notes : $EXPECTED"
echo "notes on disk  : $ACTUAL"
echo "LOST           : $LOST"
echo "elapsed        : $((END - START))s"
echo
if [ "$LOST" -gt 0 ]; then
  echo "Work was destroyed. Nobody was told. No error was raised anywhere."
  echo "Every one of those writes exited 0."
else
  echo "Nothing lost this run. Run it again; this pattern is a race, not a rule."
  echo "It fails more on a busy machine, which is exactly when you are relying on it."
fi
echo
echo "Now run ./with-fridge.sh"
