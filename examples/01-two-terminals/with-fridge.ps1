# SPDX-License-Identifier: Apache-2.0
#
# The same processes, the same load, through Agent Fridge. In PowerShell.
#
#   .\with-fridge.ps1
#
# Nothing here is a trick: same agent count, same note count, same machine,
# more contention if anything, because every write also takes a lock.

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$fridgeBin = Join-Path $here '..\..\bin\fridge.mjs'
$fridgeBin = (Resolve-Path $fridgeBin).Path
$work = Join-Path $here '.demo-with'
$agents = 6
$notesEach = 12

function fr { node $fridgeBin @args }

if (Test-Path $work) { Remove-Item -Recurse -Force $work }
New-Item -ItemType Directory -Force -Path $work | Out-Null
Push-Location $work
git init -q .

fr init --quiet
1..$agents | ForEach-Object { fr join --agent "agent-$_" --vendor other --quiet }

Write-Host "Same $agents processes, same $notesEach notes each, via 'fridge pin'."
Write-Host ""

$body = {
    param($bin, $root, $name, $count)
    Set-Location $root
    for ($i = 1; $i -le $count; $i++) {
        node $bin pin "update $i" --agent $name --quiet
        Start-Sleep -Milliseconds (Get-Random -Minimum 0 -Maximum 25)
    }
}

$start = Get-Date
$jobs = 1..$agents | ForEach-Object {
    Start-Job -ScriptBlock $body -ArgumentList $fridgeBin, $work, "agent-$_", $notesEach
}
$jobs | Wait-Job | Out-Null
$jobs | Remove-Job
$elapsed = [int]((Get-Date) - $start).TotalSeconds

$expected = $agents * $notesEach
$actual = ((fr log --json --limit 1000) -join "`n" |
    Select-String -Pattern '"body": "update ' -AllMatches).Matches.Count

Write-Host "expected notes : $expected"
Write-Host "notes on disk  : $actual"
Write-Host "LOST           : $($expected - $actual)"
Write-Host "elapsed        : ${elapsed}s"
Write-Host ""

Write-Host "--- now the part a shared file could never do: two agents want the same paths"
fr claim "src/api/**" --task "refactor the client" --ttl 30m --agent agent-1
Write-Host "agent-1 claim exit: $LASTEXITCODE"
Write-Host ""
fr claim "src/api/routes.ts" --task "fix a bug" --agent agent-2
Write-Host "agent-2 claim exit: $LASTEXITCODE  (10 = E_CONFLICT, and it says who has it)"
Write-Host ""

fr board

Pop-Location
Write-Host ""
Write-Host "No note was lost, and the second agent was stopped before it touched the file."
Write-Host "The shared-Markdown version had no way to even notice."
Write-Host "Clean up with: Remove-Item -Recurse -Force '$work'"
