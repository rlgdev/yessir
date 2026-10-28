# Installs the latest yessir release into %USERPROFILE%\.yessir\bin and adds it to the user PATH.
#   irm https://raw.githubusercontent.com/rlgdev/yessir/main/scripts/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$repo = 'rlgdev/yessir'
$bin  = Join-Path $env:USERPROFILE '.yessir\bin'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "yessir_windows_$arch.zip"

Write-Host "yessir: looking up latest release of $repo ..."
$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'yessir-installer' }
$url = ($rel.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1).browser_download_url
if (-not $url) { throw "no asset $asset in release $($rel.tag_name)" }

New-Item -ItemType Directory -Force -Path $bin | Out-Null
$zip = Join-Path $env:TEMP $asset
Write-Host "yessir: downloading $($rel.tag_name) ($asset) ..."
Invoke-WebRequest -Uri $url -OutFile $zip -Headers @{ 'User-Agent' = 'yessir-installer' }
Expand-Archive -Path $zip -DestinationPath $bin -Force
Remove-Item $zip -Force

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $bin)) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$bin", 'User')
    $env:Path = "$env:Path;$bin"
    Write-Host "yessir: added $bin to your user PATH (open a new terminal for other shells to see it)"
}

& (Join-Path $bin 'yessir.exe') version
Write-Host ''
Write-Host 'Next:'
Write-Host '  yessir doctor          # can it work on this machine?'
Write-Host '  yessir on --global     # or: cd <repo>; yessir on'
Write-Host '  then start a new claude session'
