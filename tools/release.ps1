<#
.SYNOPSIS
  Build a versioned daemon, write the update manifest, and publish a GitHub release.

.DESCRIPTION
  The daemon's built-in updater fetches
  https://github.com/HuskerMinion/techo5/releases/latest/download/manifest.json (stable) and
  installs the binary it names. This script produces both files and publishes them with gh.

.EXAMPLE
  .\tools\release.ps1 -Version v0.1.0 -Notes "First release: voice satellite on the Echo Show 5."
  .\tools\release.ps1 -Version v0.1.1 -Notes "..." -Prerelease
  .\tools\release.ps1 -Version v0.1.2 -Notes "..." -PrebuiltArm bin\echod-arm -PrebuiltArmDot bin\echod-arm-dot
#>
param(
    [Parameter(Mandatory)][ValidatePattern('^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$')][string]$Version,
    [Parameter(Mandatory)][string]$Notes,
    [switch]$Prerelease,
    [string]$Go = 'go',
    # A rootfs tarball (tools/linux/deploy-rootfs.sh builds one on the device under
    # /data/techo5-linux/); slot devices update from it, and it is published with the release.
    [string]$Rootfs = '',
    # The Echo Dot 2's rootfs tarball (techo5-dot: tools/linux/build-dot-rootfs.ps1). A Dot booting from
    # slots is offered a release only when it carries this; the Dot binary is built every time.
    [string]$DotRootfs = '',
    # The release signing key (ed25519 seed, base64). Devices take a manifest only with its signature, so
    # a release cannot be published without it. Keep it off every repository and backed up: $env:TECHO5_SIGN_KEY.
    [string]$SignKey = $env:TECHO5_SIGN_KEY,
    # A boot image built with build-image.sh --no-key, published as techo5-boot-<version>.img for new
    # units (docs/install.md). Refused if it carries an SSH key.
    [string]$Boot = '',
    # The same, built for the Echo Show 5 1st gen (checkers), published as
    # techo5-boot-checkers-<version>.img. That generation's kernel and device tree are its own, so
    # install-show.py looks for this name on a checkers unit and falls back to the newest earlier
    # release carrying one. Attach it here rather than by hand: an image uploaded any other way is
    # missing from the signed manifest, and the installer will not use a boot image the release key
    # has not vouched for.
    [string]$CheckersBoot = '',
    # The same again for the Echo Show 8 (crown), published as techo5-boot-crown-<version>.img. It
    # runs the same kernel commit as both Show 5 generations but its own configuration and device
    # trees, so it needs an image of its own; install-show.py looks for this name on a crown unit and
    # falls back to the newest earlier release carrying one. Attach it here rather than by hand, for
    # the reason above.
    [string]$CrownBoot = '',
    # Pre-built binaries from the "Build release binaries" GitHub Actions workflow run for this release's
    # tag (git tag $Version; git push origin $Version). When both are given, the local build is skipped
    # and these are signed as-is, so the release ships exactly what CI attested — but only once
    # `gh attestation verify` confirms CI built them from refs/tags/$Version: a binary built from a
    # branch or by hand is stamped with another version, and Home Assistant would then offer the
    # update forever after it was installed.
    [string]$PrebuiltArm = '',
    [string]$PrebuiltArmDot = ''
)
$ErrorActionPreference = 'Stop'
if (($PrebuiltArm -and -not $PrebuiltArmDot) -or ($PrebuiltArmDot -and -not $PrebuiltArm)) {
    throw "PrebuiltArm and PrebuiltArmDot must be given together"
}
# A root filesystem must not carry LineageOS's vendor tree: it is Amazon's and the chip makers', not ours to
# publish. Each unit mounts its own (tools/linux/rootfs/etc/techo5/boot.sh).
foreach ($t in @($Rootfs) | Where-Object { $_ }) {
    if (& tar -tzf $t | Where-Object { $_ -match '^(\./)?vendor/.' } | Select-Object -First 1) { throw "$t carries a vendor tree; build it without VENDOR_TGZ" }
}
if (-not $SignKey -or -not (Test-Path $SignKey)) { throw "no release signing key: set TECHO5_SIGN_KEY or pass -SignKey" }
$repo = 'HuskerMinion/techo5'
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$bin = Join-Path $root 'bin'
New-Item -ItemType Directory -Force $bin | Out-Null

Push-Location (Join-Path $root 'echod')
try {
    $commit = (git rev-parse --short HEAD).Trim()

    if ($PrebuiltArm) {
        Write-Host "== using prebuilt binaries (CI, tag $Version)"
        foreach ($f in @($PrebuiltArm, $PrebuiltArmDot)) {
            if (-not (Test-Path -LiteralPath $f -PathType Leaf)) { throw "prebuilt binary not found: $f" }
            & gh attestation verify $f --repo $repo --source-ref "refs/tags/$Version" | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "$f is not attested as built by CI from tag $Version" }
        }
        Copy-Item $PrebuiltArm (Join-Path $bin 'echod-arm') -Force
        Copy-Item $PrebuiltArmDot (Join-Path $bin 'echod-arm-dot') -Force
    } else {
        $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
        $pkg = 'github.com/HuskerMinion/techo5/echod/internal/layout'
        $ldflags = "-s -w -X '$pkg.Version=$Version' -X '$pkg.GitCommit=$commit' -X '$pkg.BuildDate=$date'"

        Write-Host "== building echod-arm $Version ($commit)"
        $env:GOOS = 'linux'; $env:GOARCH = 'arm'; $env:GOARM = '7'; $env:CGO_ENABLED = '0'
        & $Go build -trimpath -ldflags $ldflags -o (Join-Path $bin 'echod-arm') ./cmd/echod
        if ($LASTEXITCODE -ne 0) { throw 'build failed' }
        Write-Host "== building echod-arm-dot $Version ($commit)"
        & $Go build -tags dot -trimpath -ldflags $ldflags -o (Join-Path $bin 'echod-arm-dot') ./cmd/echod
        if ($LASTEXITCODE -ne 0) { throw 'dot build failed' }
        $env:GOOS = $null; $env:GOARCH = $null; $env:GOARM = $null; $env:CGO_ENABLED = $null
    }

    # The boot images are named in the manifest, so they have to be under their published names before
    # it is written. An image built without --no-key carries the builder's key in its initramfs; that
    # must not ship. The check is for the file entry (its name ends in a NUL), not the init script that
    # mentions it.
    $noKey = "import gzip,lzma,struct,sys; b=open(sys.argv[1],'rb').read(); ks,_,rs=struct.unpack('<3I',b[8:20]); ps=struct.unpack('<I',b[36:40])[0]; r0=ps+((ks+ps-1)//ps)*ps; r=b[r0:r0+rs]; d=gzip.decompress(r) if r[:2]==b'\x1f\x8b' else lzma.decompress(r); sys.exit(1 if b'root/.ssh/authorized_keys'+bytes(1) in d else 0)"
    $bootAssets = @()
    foreach ($img in @(@{ Path = $Boot; Name = "techo5-boot-$Version.img" },
                       @{ Path = $CheckersBoot; Name = "techo5-boot-checkers-$Version.img" },
                       @{ Path = $CrownBoot; Name = "techo5-boot-crown-$Version.img" })) {
        if (-not $img.Path) { continue }
        python -c $noKey $img.Path
        if ($LASTEXITCODE -ne 0) { throw "$($img.Path) carries an SSH key: build it with build-image.sh --no-key" }
        $named = Join-Path $bin $img.Name
        Copy-Item $img.Path $named -Force
        $bootAssets += $named
    }

    Write-Host "== manifest"
    $from = "https://github.com/$repo/releases/download/$Version"
    $mk = @('run', './cmd/mkmanifest', '-version', $Version, '-title', "TECHO5 $Version", '-notes', $Notes,
        '-release-url', "https://github.com/$repo/releases/tag/$Version",
        '-from', $from, '-arm', (Join-Path $bin 'echod-arm'), '-arm-dot', (Join-Path $bin 'echod-arm-dot'),
        '-out', (Join-Path $bin 'manifest.json'), '-sign-key', $SignKey)
    if ($Rootfs) { $mk += @('-rootfs-arm', $Rootfs) }
    if ($DotRootfs) { $mk += @('-rootfs-arm-dot', $DotRootfs) }
    # Under the signature, not just in SHA256SUMS: install-show.py writes a boot image to a unit, and
    # nothing signs SHA256SUMS, so whatever could serve a substituted list could serve the image too.
    foreach ($a in $bootAssets) { $mk += @('-asset', $a) }
    & $Go @mk
    if ($LASTEXITCODE -ne 0) { throw 'mkmanifest failed' }
    Get-Content (Join-Path $bin 'manifest.json')
} finally { Pop-Location }

Write-Host "== release $Version"
$args = @('release', 'create', $Version, (Join-Path $bin 'echod-arm'), (Join-Path $bin 'echod-arm-dot'), (Join-Path $bin 'manifest.json'),
    (Join-Path $bin 'manifest.json.sig'), '--repo', $repo, '--title', $Version, '--notes', $Notes)
if ($Rootfs) { $args += $Rootfs }
if ($DotRootfs) { $args += $DotRootfs }
$args += $bootAssets
# SHA256SUMS: for checking a download by hand. No installer reads it — the signed manifest covers
# every file one of them fetches, and an unsigned list of checksums is no check against whoever served
# the files it describes.
$files = @($args | Where-Object { $_ -is [string] -and (Test-Path -LiteralPath $_ -PathType Leaf) })
$sums = $files | ForEach-Object { "$((Get-FileHash -Algorithm SHA256 $_).Hash.ToLower())  $(Split-Path -Leaf $_)" }
$sumsFile = Join-Path $bin 'SHA256SUMS'
[IO.File]::WriteAllText($sumsFile, ($sums -join "`n") + "`n")
$args = @($args[0..2]) + $sumsFile + @($args[3..($args.Count - 1)])
# A version with a suffix (-rc.1, -beta) is a prerelease whether or not -Prerelease was given: GitHub
# otherwise makes it /releases/latest, which is what the installers and every unit's updater follow.
if ($Prerelease -or $Version -match '-') { $args += '--prerelease' }
& gh @args
if ($LASTEXITCODE -ne 0) { throw 'gh release create failed' }

# Every boot image published has to be named in the signed manifest. An installer will not use a boot
# image the manifest does not cover, so one that is missing from it is published but unusable — which
# is exactly what happened to the 1st gen Show's boot image on v0.7.6, uploaded by hand after the
# release was made. The names come back from the release itself, so an upload by hand is caught by
# re-running this. SHA256SUMS is checked too, so what people verify by hand stays complete.
$published = @(& gh release view $Version --repo $repo --json assets -q '.assets[].name')
$named = @((Get-Content (Join-Path $bin 'manifest.json') -Raw | ConvertFrom-Json).assets.PSObject.Properties.Name)
$unsigned = @($published | Where-Object { $_ -like 'techo5-boot-*' -and $named -notcontains $_ })
if ($unsigned) {
    throw "published, but these boot images are not named in the signed manifest and no installer will use them: $($unsigned -join ', ')"
}
$listed = @(Get-Content $sumsFile | ForEach-Object { ($_ -split '\s+', 2)[1].Trim() })
$missing = @($published | Where-Object { $_ -ne 'SHA256SUMS' -and $listed -notcontains $_ })
if ($missing) {
    throw "published, but these assets have no checksum in SHA256SUMS: $($missing -join ', ')"
}
Write-Host "published: https://github.com/$repo/releases/tag/$Version"
