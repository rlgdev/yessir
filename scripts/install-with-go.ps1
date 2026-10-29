# yessir: build + install from the source ZIP, fetching a portable Go if needed.
#
# Works two ways:
#   * where scripts may run:   powershell -ExecutionPolicy Bypass -File .\scripts\install-with-go.ps1
#   * where scripts are blocked ("is not digitally signed"): open this file in Notepad,
#     select all, copy, and paste it into a PowerShell window that is already in the
#     unzipped yessir-main folder, then press Enter.
#
# What it does:
#   1. uses `go` from PATH if it is 1.21+, otherwise downloads the official portable Go
#      zip for Windows from go.dev (checksum verified), no installer and no admin rights;
#   2. builds yessir.exe with no network access (standard library only) into
#      %USERPROFILE%\.yessir\bin and adds that folder to your user PATH;
#   3. removes the temporary Go again (set $keepGo = $true to keep it).
#
# Settings: edit these three lines before pasting if needed.
$keepGo = $false   # keep the downloaded Go in %USERPROFILE%\.yessir\go afterwards
$goZip  = ''       # path to a go1.xx.windows-amd64.zip you downloaded yourself (if go.dev is blocked)
$dest   = "$env:USERPROFILE\.yessir\bin"

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with the progress bar on PS 5.1

    # --- 0. are we in the source folder?
    $src = if ($PSScriptRoot) { Split-Path -Parent $PSScriptRoot } else { (Get-Location).Path }
    if (-not (Test-Path (Join-Path $src 'go.mod')) -or -not (Test-Path (Join-Path $src 'cmd\yessir'))) {
        Write-Host "yessir: run this from the unzipped yessir-main folder (no go.mod in $src)." -ForegroundColor Red
        Write-Host '        e.g.  cd $env:USERPROFILE\Downloads\yessir-main   and paste again.'
        return
    }

    # --- 1. find or fetch Go
    $go = $null
    $minor = 0
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd -and $env:YESSIR_FORCE_GO_DOWNLOAD -ne '1') {
        $v = & $cmd.Source version 2>$null
        if ($v -match 'go1\.(\d+)') { $minor = [int]$Matches[1] }
        if ($minor -ge 21) { $go = $cmd.Source; Write-Host "yessir: using installed $v" }
        else { Write-Host "yessir: installed Go is too old ($v), fetching a portable one" }
    }

    $goRoot = Join-Path $env:USERPROFILE '.yessir\go'
    $fetched = $false
    if (-not $go -and (Test-Path (Join-Path $goRoot 'bin\go.exe'))) {
        $go = Join-Path $goRoot 'bin\go.exe'
        Write-Host "yessir: using portable Go from an earlier run: $(& $go version)"
    }

    if (-not $go) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        try { [Net.WebRequest]::DefaultWebProxy.Credentials = [Net.CredentialCache]::DefaultNetworkCredentials } catch {}
        $arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
        $tmp = Join-Path $env:TEMP ("yessir-go-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
        New-Item -ItemType Directory -Force $tmp | Out-Null
        $sha = $null

        if ($goZip) {
            if (-not (Test-Path $goZip)) { Write-Host "yessir: `$goZip not found: $goZip" -ForegroundColor Red; return }
            $zip = (Resolve-Path $goZip).Path
            Write-Host "yessir: using Go archive $zip"
        }
        else {
            Write-Host 'yessir: looking up the latest stable Go on go.dev ...'
            try {
                $rel = Invoke-RestMethod -UseBasicParsing -Uri 'https://go.dev/dl/?mode=json'
            }
            catch {
                Write-Host "yessir: cannot reach go.dev ($($_.Exception.Message))." -ForegroundColor Red
                Write-Host "        Download go1.xx.windows-$arch.zip in your browser from https://go.dev/dl/ (or https://golang.google.cn/dl/),"
                Write-Host '        set $goZip = ''<path to that zip>'' at the top of this script, and paste it again.'
                return
            }
            $file = $rel | Where-Object { $_.stable } | ForEach-Object { $_.files } |
                Where-Object { $_.os -eq 'windows' -and $_.arch -eq $arch -and $_.kind -eq 'archive' } |
                Select-Object -First 1
            if (-not $file) { Write-Host "yessir: no Windows/$arch archive listed on go.dev" -ForegroundColor Red; return }
            $zip = Join-Path $tmp $file.filename
            $sha = $file.sha256
            $ok = $false
            foreach ($u in @("https://go.dev/dl/$($file.filename)", "https://dl.google.com/go/$($file.filename)")) {
                try {
                    Write-Host "yessir: downloading $($file.version) ($([math]::Round($file.size / 1MB)) MB) from $u ..."
                    Invoke-WebRequest -UseBasicParsing -Uri $u -OutFile $zip
                    $ok = $true; break
                }
                catch { Write-Host "        failed: $($_.Exception.Message)" -ForegroundColor Yellow }
            }
            if (-not $ok) {
                Write-Host 'yessir: Go download blocked. Download it in your browser and set $goZip (see top of script).' -ForegroundColor Red
                return
            }
        }

        if ($sha) {
            $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
            if ($got -ne $sha.ToLower()) { Write-Host "yessir: checksum mismatch for $zip (got $got, want $sha)" -ForegroundColor Red; return }
            Write-Host 'yessir: checksum ok'
        }

        Write-Host "yessir: unpacking Go to $goRoot ..."
        if (Test-Path $goRoot) { Remove-Item -Recurse -Force $goRoot }
        $unpack = Join-Path $tmp 'unpack'
        New-Item -ItemType Directory -Force $unpack | Out-Null
        $tar = Join-Path $env:SystemRoot 'System32\tar.exe'
        if (Test-Path $tar) { & $tar -xf $zip -C $unpack; if ($LASTEXITCODE -ne 0) { throw 'tar failed' } }
        else { Expand-Archive -Path $zip -DestinationPath $unpack -Force }   # slow, but works without tar.exe
        New-Item -ItemType Directory -Force (Split-Path $goRoot) | Out-Null
        Move-Item (Join-Path $unpack 'go') $goRoot
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        $go = Join-Path $goRoot 'bin\go.exe'
        $fetched = $true
        Write-Host "yessir: $(& $go version)"
    }

    # --- 2. build (offline)
    $cache = Join-Path $env:TEMP ("yessir-gocache-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
    $env:GOTOOLCHAIN = 'local'
    $env:GOPROXY = 'off'
    $env:GOFLAGS = ''
    $env:CGO_ENABLED = '0'
    $env:GOCACHE = $cache
    if ($fetched -or $go -like "$goRoot*") { $env:GOROOT = $goRoot }

    $ver = 'source'
    $vf = Join-Path $src 'VERSION'
    if (Test-Path $vf) { $ver = (Get-Content $vf -Raw).Trim() }
    New-Item -ItemType Directory -Force $dest | Out-Null
    $exe = Join-Path $dest 'yessir.exe'
    Write-Host "yessir: building $ver-src ..."
    Push-Location $src
    try {
        & $go build -trimpath -ldflags "-s -w -X main.version=$ver-src" -o $exe ./cmd/yessir
        $code = $LASTEXITCODE
    }
    finally { Pop-Location }
    Remove-Item -Recurse -Force $cache -ErrorAction SilentlyContinue
    Remove-Item Env:GOROOT -ErrorAction SilentlyContinue
    if ($code -ne 0) { Write-Host "yessir: go build failed (exit $code)" -ForegroundColor Red; return }

    # --- 3. tidy up Go we fetched
    if (($fetched -or $go -like "$goRoot*") -and -not $keepGo) {
        Remove-Item -Recurse -Force $goRoot -ErrorAction SilentlyContinue
        Write-Host 'yessir: removed the temporary Go (set $keepGo = $true to keep it next time)'
    }

    # --- 4. PATH + check
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $dest)) {
        $new = if ([string]::IsNullOrEmpty($userPath)) { $dest } else { "$userPath;$dest" }
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        Write-Host "yessir: added $dest to your user PATH"
    }
    if (-not (($env:Path -split ';') -contains $dest)) { $env:Path = "$env:Path;$dest" }

    & $exe version
    if ($LASTEXITCODE -ne 0) {
        Write-Host 'yessir: the built program did not start. If Windows says it was blocked by group policy,' -ForegroundColor Red
        Write-Host '        your organisation blocks unsigned programs (AppLocker/WDAC); yessir must be approved by IT.'
        return
    }
    Write-Host ''
    Write-Host "Installed: $exe"
    Write-Host 'Next:  yessir doctor    then    yessir on --global   (or: cd <repo>; yessir on)'
    Write-Host '       and start a new claude session.'
}
