<!-- GENERATED FILE. Edit src/core/errors.mjs, then run: npm run gen -->
# FridgeBoard exit codes (protocol wcp/0.1)

Exit codes are the public API of `fridge`. A script may branch on them.
They are stable for the whole 0.x line: numbers are never reused or renumbered,
only added.

Rules:

1. `0` means the operation happened. Nothing else means it happened.
2. Every non-zero code is a deliberate, documented refusal. There is no silent
   fallback and no partial success.
3. All codes are below `126`, so they never collide with the shell's
   "command not found" (127) or "killed by signal" (128+N) range.
4. `--json` prints the same information on stdout as
   `{"ok":false,"error":{"code":"E_...","exit":N,...}}`, so callers that cannot
   read `$?` portably can parse instead.

| Exit | Code | Meaning |
| ---: | ---- | ------- |
| `0` | `OK` | Success. |
| `1` | `E_INTERNAL` | Unexpected internal error (a bug). Re-run with --verbose for a stack trace. |
| `2` | `E_USAGE` | Bad arguments: unknown flag, missing required flag, invalid duration. |
| `3` | `E_NOT_INITIALIZED` | No .fridge/ found from the current directory upward. Run: fridge init |
| `4` | `E_PROTOCOL_VERSION` | .fridge/VERSION is a protocol version this binary does not support. |
| `5` | `E_STATE_CORRUPT` | A record is unparseable or invalid, or a write could not be completed. |
| `6` | `E_PERMISSION` | Permission denied or read-only filesystem under .fridge/. |
| `7` | `E_NO_SESSION` | No actor/session could be resolved. Run: fridge join --agent <name> |
| `10` | `E_CONFLICT` | The requested scope overlaps a claim held by someone else. |
| `11` | `E_NOT_FOUND` | No such claim, message, actor, or queue entry. |
| `12` | `E_NOT_OWNER` | You do not hold the token for that claim. |
| `13` | `E_LEASE_EXPIRED` | Your claim already expired and was reaped. |
| `14` | `E_OUT_OF_SCOPE` | That path is not covered by any claim you hold. |
| `15` | `E_ALREADY_EXISTS` | Already exists (workspace, actor, or record). |
| `20` | `E_MUTEX_TIMEOUT` | Could not acquire the registry mutex before the deadline. |
| `21` | `E_WAIT_TIMEOUT` | Wait deadline reached. |
| `22` | `E_QUEUE_ABANDONED` | The queue entry expired or was cancelled. |
| `30` | `E_DRIFT` | A --check found a problem: doctor findings, unrendered door, or stale adapter block. |
| `40` | `E_PATH_INVALID` | Path rejected: traversal, escape, reserved location, or unsupported glob. |
| `41` | `E_FOREIGN_HOST` | That claim belongs to another host. Pass --allow-multihost to override. |

## Reading them from a shell

```bash
if fridge claim "src/api/**" --task "refactor"; then
  : # the chore is yours
elif [ $? -eq 10 ]; then
  echo "someone else has it; pick another chore"
else
  exit 1 # a real error, do not guess
fi
```

## Reading them from PowerShell

```powershell
fridge claim "src/api/**" --task "refactor"
switch ($LASTEXITCODE) {
  0  { "the chore is yours" }
  10 { "someone else has it" }
  default { throw "fridge failed with $LASTEXITCODE" }
}
```
