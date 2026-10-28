<#
.SYNOPSIS
  Builds yessir from this source folder and installs it for the current user.

.DESCRIPTION
  For machines where GitHub release downloads are blocked: download the source
  ZIP (Code -> Download ZIP), unzip it, and run from the unzipped folder:

    powershell -ExecutionPolicy Bypass -File .\scripts\install-from-source.ps1

  Needs Go 1.21 or newer on PATH. The build uses only Go's standard library, so
  nothing is downloaded. Installs to %USERPROFILE%\.yessir\bin\yessir.exe and adds
  that folder to the user PATH.

.PARAMETER Dest
  Install folder (default: %USERPROFILE%\.yessir\bin).

.PARAMETER NoPath
  Do not touch the user PATH.
#>
param(
    [string]$Dest = (Join-Path $env:USERPROFILE '.yessir\bin'),
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $root 'go.mod'))) {
    throw "run this from the unzipped yessir source folder (go.mod not found in $root)"
}

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    Write-Host 'yessir: Go is not installed (or not on PATH).' -ForegroundColor Yellow
    Write-Host '  Install it from https://go.dev/dl/ (the .msi needs no admin with "install for me only"),'
    Write-Host '  or: winget install GoLang.Go   then open a new terminal and run this script again.'
    exit 1
}
Write-Host "yessir: using $(& go version)"

# Never let Go try to download a newer toolchain or modules: everything needed is in this folder.
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:CGO_ENABLED = '0'

$version = (Get-Content (Join-Path $root 'VERSION') -Raw).Trim()
if (-not $version) { $version = 'source' }
$version = "$version-src"

New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$exe = Join-Path $Dest 'yessir.exe'

Push-Location $root
try {
    Write-Host "yessir: building $version ..."
    & go build -trimpath -ldflags "-s -w -X main.version=$version" -o $exe ./cmd/yessir
    if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
}
finally {
    Pop-Location
}

if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $Dest)) {
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $Dest } else { "$userPath;$Dest" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "yessir: added $Dest to your user PATH (new terminals will see it)"
    }
    if (-not (($env:Path -split ';') -contains $Dest)) { $env:Path = "$env:Path;$Dest" }
}

& $exe version
if ($LASTEXITCODE -ne 0) { throw "the built binary did not run" }

Write-Host ''
Write-Host "Installed: $exe"
Write-Host 'Next:'
Write-Host '  yessir doctor          # can it work on this machine?'
Write-Host '  yessir on --global     # or: cd <repo>; yessir on'
Write-Host '  then start a new claude session'
Write-Host '(If yessir was enabled before from another location, run "yessir on" again so the hook points here.)'
