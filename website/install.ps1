#Requires -Version 5.1
<#
.SYNOPSIS
    Installs kute, a TUI Kubernetes console.

.DESCRIPTION
    The Windows counterpart to https://kute.dev/install.sh. Downloads the
    release archive for this machine's architecture, verifies it against the
    release's checksums.txt, and installs kute.exe.

        irm https://kute.dev/install.ps1 | iex

    Environment overrides, matching install.sh:
        $env:KUTE_VERSION       release to install (default: latest)
        $env:KUTE_INSTALL_DIR   install location
                                (default: %LOCALAPPDATA%\Programs\kute)
#>

$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 renders a progress bar for every Invoke-WebRequest
# and it costs roughly an order of magnitude in download time.
$ProgressPreference = 'SilentlyContinue'

function Install-Kute {
    $repo = 'kute-dev/kute'
    $bin = 'kute.exe'

    # 5.1 still negotiates TLS 1.0 by default, which github.com refuses.
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    # PROCESSOR_ARCHITECTURE reports the *process* architecture, so a 32-bit
    # PowerShell on 64-bit Windows says x86; PROCESSOR_ARCHITEW6432 is the
    # machine's real one and is only set in that WOW64 case. Same class of
    # lie as uname -m under Rosetta, which install.sh corrects the same way.
    $hostArch = $env:PROCESSOR_ARCHITEW6432
    if (-not $hostArch) { $hostArch = $env:PROCESSOR_ARCHITECTURE }
    switch ($hostArch) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "kute install: unsupported architecture: $hostArch" }
    }

    $version = $env:KUTE_VERSION
    if (-not $version) { $version = 'latest' }
    if ($version -eq 'latest') {
        Write-Host 'Resolving latest kute release...'
        $latest = Invoke-RestMethod -UseBasicParsing `
            -Uri "https://api.github.com/repos/$repo/releases/latest"
        $version = $latest.tag_name
    }
    if ($version -match '^\d') { $version = "v$version" }
    if ($version -notmatch '^v\d') {
        throw "kute install: could not resolve release version (got: '$version')"
    }

    $archiveVersion = $version.TrimStart('v')
    $archive = "kute_${archiveVersion}_windows_${arch}.zip"
    $baseUrl = "https://github.com/$repo/releases/download/$version"

    $installDir = $env:KUTE_INSTALL_DIR
    if (-not $installDir) {
        # Under LOCALAPPDATA rather than Program Files: writable without
        # elevation, and there is no sudo here to fall back on the way
        # install.sh does.
        $installDir = Join-Path $env:LOCALAPPDATA 'Programs\kute'
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("kute-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        Write-Host "Installing kute $version for windows/$arch..."

        $archivePath = Join-Path $tmp $archive
        $sumsPath = Join-Path $tmp 'checksums.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/$archive" -OutFile $archivePath
            Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/checksums.txt" -OutFile $sumsPath
        } catch {
            throw "kute install: download failed from ${baseUrl}: $($_.Exception.Message)"
        }

        # checksums.txt is "<sha256>  <filename>"; the binary-mode marker is
        # a "*" prefix on the name, which sha256sum writes and goreleaser
        # does not — tolerate both.
        $pattern = "\s\*?$([regex]::Escape($archive))$"
        $line = Get-Content -LiteralPath $sumsPath |
            Where-Object { $_ -match $pattern } |
            Select-Object -First 1
        if (-not $line) {
            throw "kute install: checksums.txt has no entry for $archive"
        }
        $want = ($line -split '\s+')[0]
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash
        # -ne on strings is case-insensitive, which is what we want:
        # Get-FileHash returns upper case, checksums.txt is lower.
        if ($got -ne $want) {
            throw "kute install: checksum verification failed for $archive"
        }

        Expand-Archive -LiteralPath $archivePath -DestinationPath $tmp -Force
        $staged = Join-Path $tmp $bin
        if (-not (Test-Path -LiteralPath $staged)) {
            throw "kute install: archive did not contain $bin"
        }

        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        $dest = Join-Path $installDir $bin
        $old = "$dest.old"
        # Windows locks a running executable against writes, but not against
        # a rename — so move the old one aside first. This is the same
        # upgrade-while-running case install.sh handles by replacing the
        # inode to dodge ETXTBSY.
        if (Test-Path -LiteralPath $dest) {
            Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
            Move-Item -LiteralPath $dest -Destination $old -Force
        }
        Copy-Item -LiteralPath $staged -Destination $dest -Force
        # Still locked if the old kute is running; it goes on the next run.
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue

        Write-Host "kute installed to $dest"

        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @($userPath -split ';' | Where-Object { $_ })
        if ($entries -notcontains $installDir) {
            $updated = (@($entries) + $installDir) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
            Write-Host "Added $installDir to your PATH - restart your terminal to pick it up."
        }
        # Make kute resolvable in *this* session too, so the check below and
        # anything the user types next both work without a restart.
        $env:Path = "$env:Path;$installDir"

        & $dest --version
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-Kute
