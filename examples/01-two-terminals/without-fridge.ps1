# SPDX-License-Identifier: Apache-2.0
#
# The old way, in PowerShell. Several "agents" coordinate through one shared
# Markdown file, each doing read-modify-write. This is the pattern that lost
# 128 lines of work.
#
#   .\without-fridge.ps1
#
# It does not use Agent Fridge at all. That is the point.

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$work = Join-Path $here '.demo-without'
$board = Join-Path $work 'shared-development-updates.md'
$agents = 6
$notesEach = 12

if (Test-Path $work) { Remove-Item -Recurse -Force $work }
New-Item -ItemType Directory -Force -Path $work | Out-Null
Set-Content -Path $board -Value "# Shared development updates`n"

Write-Host "Several agents, one shared Markdown file, read-modify-write."
Write-Host "$agents processes x $notesEach notes each = $($agents * $notesEach) notes expected."
Write-Host ""

# Each agent reads the whole file, appends its line, and writes the whole file
# back. Exactly what an agent does when told to "update the shared file".
$body = {
    param($boardPath, $name, $count)
    for ($i = 1; $i -le $count; $i++) {
        $current = Get-Content -Raw -Path $boardPath
        $line = "- [$name] update $i"
        Set-Content -Path $boardPath -Value ($current + $line + "`n")
        Start-Sleep -Milliseconds (Get-Random -Minimum 0 -Maximum 25)
    }
}

$start = Get-Date
$jobs = 1..$agents | ForEach-Object {
    Start-Job -ScriptBlock $body -ArgumentList $board, "agent-$_", $notesEach
}
$jobs | Wait-Job | Out-Null
$jobs | Remove-Job
$elapsed = [int]((Get-Date) - $start).TotalSeconds

$expected = $agents * $notesEach
$actual = (Select-String -Path $board -Pattern '^- \[agent-' -AllMatches).Count
$lost = $expected - $actual

Write-Host "expected notes : $expected"
Write-Host "notes on disk  : $actual"
Write-Host "LOST           : $lost"
Write-Host "elapsed        : ${elapsed}s"
Write-Host ""
if ($lost -gt 0) {
    Write-Host "Work was destroyed. Nobody was told. No error was raised anywhere."
    Write-Host "Every one of those writes succeeded."
} else {
    Write-Host "Nothing lost this run. Run it again; this pattern is a race, not a rule."
    Write-Host "It fails more on a busy machine, which is when you are relying on it."
}
Write-Host ""
Write-Host "Now run .\with-fridge.ps1"
