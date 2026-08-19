# SPDX-License-Identifier: Apache-2.0
<#
.SYNOPSIS
  Agent Fridge Board installer for Windows PowerShell.

.DESCRIPTION
  Downloads one static binary, verifies its checksum, and puts it on your PATH.
  No runtime, no package manager, no post-install script, nothing written into
  your repository. Read it before you pipe it to iex; it is short on purpose.

.EXAMPLE
  irm https://github.com/RagnarPitla/agent-fridge/releases/latest/download/install.ps1 | iex

.EXAMPLE
  .\install.ps1 -Version v0.2.0 -Dir C:\tools
#>
[CmdletBinding()]
param(
  [string]$Version = 'latest',
  [string]$Dir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = 'RagnarPitla/agent-fridge'
$bin = 'fridge'

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { $null }
}
if (-not $arch) {
  Write-Error "Unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'. Build from source: go install github.com/$repo/cmd/$bin@latest"
}

$asset = "${bin}_windows_${arch}.exe"
$base = if ($Version -eq 'latest') {
  "https://github.com/$repo/releases/latest/download"
} else {
  "https://github.com/$repo/releases/download/$Version"
}

if (-not $Dir) { $Dir = Join-Path $env:LOCALAPPDATA 'Programs\agent-fridge' }
New-Item -ItemType Directory -Force -Path $Dir | Out-Null

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("agent-fridge-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$dl = Join-Path $tmp "$bin.exe"

try {
  Write-Host "Agent Fridge Board: downloading $asset ($Version)"
  try {
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $dl -UseBasicParsing
  } catch {
    Write-Error "Download failed. Check that a release exists at https://github.com/$repo/releases"
  }

  # Checksum verification is best effort: if the .sha256 is missing we say so
  # loudly rather than pretending we verified something.
  $sumFile = "$dl.sha256"
  $verified = $false
  try {
    Invoke-WebRequest -Uri "$base/$asset.sha256" -OutFile $sumFile -UseBasicParsing
    $want = ((Get-Content $sumFile -Raw) -split '\s+')[0].Trim().ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $dl).Hash.ToLower()
    if ($want -ne $got) {
      Write-Error "CHECKSUM MISMATCH. Refusing to install.`n  expected $want`n  got      $got"
    }
    $verified = $true
  } catch {
    if (-not $verified) { Write-Warning "No published checksum for $asset, skipping verification" }
  }
  if ($verified) { Write-Host 'Agent Fridge Board: checksum verified' }

  $target = Join-Path $Dir "$bin.exe"
  Move-Item -Force -Path $dl -Destination $target
  Write-Host "Agent Fridge Board: installed to $target"

  & $target version

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if ($userPath -notlike "*$Dir*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$Dir", 'User')
    $env:Path = "$env:Path;$Dir"
    Write-Host ''
    Write-Host "  Added $Dir to your user PATH. Open a new terminal to pick it up."
  }

  Write-Host ''
  Write-Host "  Next:  cd your-repo; $bin init"
  Write-Host "  Check: $bin conform"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
