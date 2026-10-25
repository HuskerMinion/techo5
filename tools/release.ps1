<#
.SYNOPSIS
  Build a versioned daemon, write the update manifest, and publish a GitHub release.

.DESCRIPTION
  The daemon's built-in updater fetches
  https://github.com/HuskerMinion/techo5/releases/latest/download/manifest.json (stable) and
  installs the binary it names. This script produces both files and publishes them with gh.

.EXAMPLE
  .\tools\release.ps1 -Version v0.1.0 -Notes "First release: voice satellite on the Echo Show 5." -NoAgents
  .\tools\release.ps1 -Version v0.1.1 -Notes "..." -Prerelease -NoAgents
  .\tools\release.ps1 -Version v0.1.2 -Notes "..." -PrebuiltArm bin\echod-arm -PrebuiltArmDot bin\echod-arm-dot -Agents bin
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
    # The Echo Show 8 2nd gen (athena) rootfs tarball: deploy-rootfs.sh --out with
    # DEVICE_OVERLAY=tools/linux/athena/overlay EXTRA_PACKAGES=busybox-extras. athena runs the Show's
    # binary but boots this rootfs of its own, the same on both kernels it can run: TECHO5's mainline
    # one (the default) and Amazon's (install-athena.py --kernel amazon, with its vendor partition). It
    # is keyed arm-athena in the manifest and published as techo5-rootfs-athena-<version>.tar.gz. An athena is offered a
    # release only when it carries this; the other Shows never take it.
    [string]$AthenaRootfs = '',
    # The athena store image for new units (tools/linux/athena/mkstore.sh -k none, gzipped), published as
    # techo5-system-athena-<version>.img.gz and named in the signed manifest for install-athena.py.
    # Refused if it carries an SSH key.
    [string]$AthenaImage = '',
    # The athena boot image (tools/linux/athena/kernel/build.sh's boot-athena.img: TECHO5's mainline
    # kernel), published as techo5-boot-athena-<version>.img and named in the signed manifest for
    # install-athena.py. Refused unless check-boot.py passes it with its amp tuning slot empty: the
    # tuning is Amazon's and is filled in from each unit's own backup, never shipped.
    [string]$AthenaBoot = '',
    # The source of that kernel, for the GPL: build.sh's out/linux-<kver>-techo5-athena-source.tar.xz,
    # published as linux-<kver>-techo5-athena-<version>-source.tar.xz. Required whenever a kernel ships
    # (-AthenaBoot, which an athena rootfs carrying a kernel needs), and refused unless check-source.py
    # finds it the patched tree, with the configuration the boot image's kernel carries.
    [string]$AthenaKernelSource = '',
    # The Skylight Calendar 15 rootfs tarball: deploy-rootfs.sh --out with
    # DEVICE_OVERLAY=tools/linux/skylight/overlay EXTRA_PACKAGES=busybox-extras KERNEL_IMAGE=<-SkylightBoot>.
    # The Show's binary on a rootfs of its own, keyed arm-skylight in the manifest and published as
    # techo5-rootfs-skylight-<version>.tar.gz. A Skylight is offered a release only when it carries this;
    # the Shows never take it.
    [string]$SkylightRootfs = '',
    # The Skylight's store image for new units (tools/linux/skylight/mkstore.sh -k none, with no -F: the
    # unit's own Wi-Fi and Bluetooth firmware go in at install, from its own vendor partition, never in a
    # release; gzipped), published as techo5-store-skylight-<version>.img.gz for install-skylight.py.
    [string]$SkylightStore = '',
    # The Skylight's boot image (tools/linux/skylight/kernel/build.sh's boot-skylight.img: TECHO5's
    # mainline kernel), published as techo5-boot-skylight-<version>.img. install-skylight.py writes it
    # into recovery, and the Skylight rootfs carries it for the kernel update.
    [string]$SkylightBoot = '',
    # The installer image (build.sh INSTALLER=1's boot-skylight-installer.img: the same kernel with
    # install.sh), published as techo5-installer-skylight-<version>.img.
    [string]$SkylightInstaller = '',
    # The source of that kernel, for the GPL: build.sh's out/linux-<kver>-techo5-skylight-source.tar.xz,
    # published as linux-<kver>-techo5-skylight-<version>-source.tar.xz.
    [string]$SkylightKernelSource = '',
    # install-skylight.py takes its images from the latest release, so a stable Show release without
    # them leaves new Skylights with nothing to install: it takes this switch to make one on purpose.
    [switch]$NoSkylight,
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
    [string]$PrebuiltArmDot = '',
    # The folder of TECHO5 Deck agent binaries from the same workflow run (techo5-deck-windows-amd64.exe
    # and the rest). Each is checked against CI's attestation for this tag, named in the signed
    # manifest, and published under its own name, so a link to the latest release's file never changes.
    [string]$Agents = '',
    # A Show release without the agents breaks every link to the latest release's agent files, so it
    # takes this switch to make one on purpose.
    [switch]$NoAgents
)
$ErrorActionPreference = 'Stop'
if (-not $Agents -and -not $NoAgents) {
    throw "no -Agents folder: the docs link to the latest release's TECHO5 Deck agent files (pass -NoAgents to release without them)"
}
if (($PrebuiltArm -and -not $PrebuiltArmDot) -or ($PrebuiltArmDot -and -not $PrebuiltArm)) {
    throw "PrebuiltArm and PrebuiltArmDot must be given together"
}
# install-athena.py's default install writes both the store image and the boot image from the same
# release: one without the other would leave new units with nothing to install.
if (($AthenaImage -and -not $AthenaBoot) -or ($AthenaBoot -and -not $AthenaImage)) {
    throw "AthenaImage and AthenaBoot must be given together: install-athena.py needs both from one release"
}
# A root filesystem must not carry LineageOS's vendor tree: it is Amazon's and the chip makers', not ours to
# publish. Each unit mounts its own (tools/linux/rootfs/etc/techo5/boot.sh).
foreach ($t in @($Rootfs, $AthenaRootfs, $SkylightRootfs) | Where-Object { $_ }) {
    if (& tar -tzf $t | Where-Object { $_ -match '^(\./)?vendor/.' } | Select-Object -First 1) { throw "$t carries a vendor tree; build it without VENDOR_TGZ" }
    # Nor a development kernel's modules and firmware (mkrootfs.sh's inputs/kernel.tar.gz): a released
    # rootfs has an empty lib/firmware and no lib/modules.
    if (& tar -tzf $t | Where-Object { $_ -match '^(\./)?lib/(modules|firmware)/.' } | Select-Object -First 1) { throw "$t carries kernel modules or firmware; build it without inputs/kernel.tar.gz" }
}
# The plain -Rootfs is the one every Show without a rootfs of its own updates from: a board's own
# (T5_BOARD in its device.conf, from the athena or Skylight overlay), or one carrying a kernel for the
# kernel update (usr/share/techo5/kernel/), would be offered to all of them.
if ($Rootfs) {
    $board = @(& tar -xzOf $Rootfs ./etc/techo5/device.conf 2>$null) -match '^T5_BOARD=' | Select-Object -Last 1
    if ($board) { throw "$Rootfs is a board's own rootfs (device.conf: '$board'), not the Shows': pass it as -AthenaRootfs or -SkylightRootfs" }
    if (& tar -tzf $Rootfs | Where-Object { $_ -match '^(\./)?usr/share/techo5/kernel/.' } | Select-Object -First 1) {
        throw "$Rootfs carries a kernel (usr/share/techo5/kernel/), which the Shows' rootfs never does: build it without KERNEL_IMAGE"
    }
}
# The Skylight runs the Shows' binary: its rootfs must carry the same one as the Shows' in this release.
if ($Rootfs -and $SkylightRootfs) {
    $cmp = Join-Path ([System.IO.Path]::GetTempPath()) "techo5-release-$PID"
    try {
        $sums = foreach ($t in @($Rootfs, $SkylightRootfs)) {
            $d = Join-Path $cmp ([guid]::NewGuid().ToString('n'))
            New-Item -ItemType Directory -Force $d | Out-Null
            & tar -xzf $t -C $d ./usr/local/bin/techo5
            if ($LASTEXITCODE -ne 0) { throw "$t has no usr/local/bin/techo5" }
            (Get-FileHash -Algorithm SHA256 (Join-Path $d 'usr/local/bin/techo5')).Hash
        }
        if ($sums[0] -ne $sums[1]) { throw "$Rootfs and $SkylightRootfs carry different usr/local/bin/techo5: build both from the same tree" }
    } finally { Remove-Item -Recurse -Force $cmp -ErrorAction SilentlyContinue }
}
# The video decoder. Every rootfs carries the same 32-bit ffmpeg as usr/local/bin/techo5-ffmpeg
# (bin/techo5-ffmpeg-arm, tools/linux/build-ffmpeg.sh). athena's and the Skylight's also carry the same
# static aarch64 build as usr/local/bin/techo5-ffmpeg64 (bin/techo5-ffmpeg-arm64,
# tools/linux/build-ffmpeg-arm64.sh), with H.265, VP8 and VP9, which the daemon runs on their 64-bit
# kernels; the Shows' rootfs never carries it.
$ffWants = @()
if ($Rootfs) { $ffWants += , @($Rootfs, $false) }
if ($AthenaRootfs) { $ffWants += , @($AthenaRootfs, $true) }
if ($SkylightRootfs) { $ffWants += , @($SkylightRootfs, $true) }
if ($ffWants.Count) {
    $ffDir = Join-Path ([System.IO.Path]::GetTempPath()) "techo5-ffmpeg-$PID"
    try {
        $ff32 = @{}; $ff64 = @{}
        foreach ($w in $ffWants) {
            $t, $want64 = $w
            $d = Join-Path $ffDir ([guid]::NewGuid().ToString('n'))
            New-Item -ItemType Directory -Force $d | Out-Null
            $names = @(& tar -tzf $t) -replace '^\./', ''
            if ($names -notcontains 'usr/local/bin/techo5-ffmpeg') { throw "$t has no usr/local/bin/techo5-ffmpeg: build it with bin/techo5-ffmpeg-arm (tools/linux/build-ffmpeg.sh)" }
            & tar -xzf $t -C $d ./usr/local/bin/techo5-ffmpeg
            if ($LASTEXITCODE -ne 0) { throw "${t}: usr/local/bin/techo5-ffmpeg could not be read" }
            $ff32[$t] = (Get-FileHash -Algorithm SHA256 (Join-Path $d 'usr/local/bin/techo5-ffmpeg')).Hash
            $has64 = $names -contains 'usr/local/bin/techo5-ffmpeg64'
            if ($want64 -and -not $has64) { throw "$t has no usr/local/bin/techo5-ffmpeg64: build bin/techo5-ffmpeg-arm64 (tools/linux/build-ffmpeg-arm64.sh) before deploy-rootfs.sh" }
            if (-not $want64 -and $has64) { throw "$t carries usr/local/bin/techo5-ffmpeg64, which only athena's and the Skylight's rootfs do" }
            if (-not $want64) { continue }
            & tar -xzf $t -C $d ./usr/local/bin/techo5-ffmpeg64
            if ($LASTEXITCODE -ne 0) { throw "${t}: usr/local/bin/techo5-ffmpeg64 could not be read" }
            $f = Join-Path $d 'usr/local/bin/techo5-ffmpeg64'
            $b = [System.IO.File]::ReadAllBytes($f)
            # An ELF64 for AArch64 (e_machine 183) with no program interpreter (PT_INTERP, 3): static, since
            # nothing of the 32-bit userspace can be linked to it.
            $elf = $b.Length -gt 64 -and $b[0] -eq 0x7f -and $b[1] -eq 0x45 -and $b[2] -eq 0x4c -and $b[3] -eq 0x46 -and $b[4] -eq 2 -and [BitConverter]::ToUInt16($b, 18) -eq 183
            $interp = $false
            if ($elf) {
                $phoff = [BitConverter]::ToUInt64($b, 32); $phsize = [BitConverter]::ToUInt16($b, 54); $phnum = [BitConverter]::ToUInt16($b, 56)
                for ($i = 0; $i -lt $phnum; $i++) { if ([BitConverter]::ToUInt32($b, [int]($phoff + $i * $phsize)) -eq 3) { $interp = $true } }
            }
            if (-not $elf -or $interp) { throw "$t carries a usr/local/bin/techo5-ffmpeg64 that is not a static aarch64 binary" }
            # What the daemon looks for to know the decoders are in it (feature/video, proc.go).
            $text = [System.Text.Encoding]::ASCII.GetString($b)
            foreach ($m in @('Two slices reporting being the first in the same frame', 'Discarding interframe without a prior keyframe!', 'Invalid compressed header size')) {
                if (-not $text.Contains($m)) { throw "$t carries a usr/local/bin/techo5-ffmpeg64 without H.265, VP8 or VP9 (no '$m')" }
            }
            $ff64[$t] = (Get-FileHash -Algorithm SHA256 $f).Hash
        }
        if (@($ff32.Values | Sort-Object -Unique).Count -gt 1) { throw "the rootfs tarballs carry different usr/local/bin/techo5-ffmpeg: build them all with the same bin/techo5-ffmpeg-arm" }
        if (@($ff64.Values | Sort-Object -Unique).Count -gt 1) { throw "$AthenaRootfs and $SkylightRootfs carry different usr/local/bin/techo5-ffmpeg64: build both with the same bin/techo5-ffmpeg-arm64" }
        foreach ($t in $ff64.Keys) { Write-Host "== ${t}: ffmpeg64 sha256 $($ff64[$t].ToLower())" }
    } finally { Remove-Item -Recurse -Force $ffDir -ErrorAction SilentlyContinue }
}
# Each Show rootfs says which board it is for (etc/techo5/device.conf, T5_BOARD); the athena store
# installs only athena's.
if ($AthenaRootfs) {
    $board = (& tar -xzOf $AthenaRootfs ./etc/techo5/device.conf) -match '^T5_BOARD=' | Select-Object -Last 1
    if ($board -ne 'T5_BOARD=athena') { throw "$AthenaRootfs is not an athena rootfs (device.conf: '$board'); build it with DEVICE_OVERLAY=tools/linux/athena/overlay" }
    # The athena rootfs carries the kernel it was tested with, which goes to the boot partition once
    # the slot is good (echod/internal/update/kernel). It must be this release's boot image: another
    # would move a freshly installed unit to a different kernel five minutes after its first boot.
    $kernelSum = & tar -xzOf $AthenaRootfs ./usr/share/techo5/kernel/boot-athena.img.sha256 2>$null
    if ($AthenaBoot) {
        if (-not $kernelSum) { throw "$AthenaRootfs carries no kernel: build it with KERNEL_IMAGE=$AthenaBoot (deploy-rootfs.sh), or updates never bring units this kernel" }
        $want = (Get-FileHash -Algorithm SHA256 $AthenaBoot).Hash.ToLower()
        $got = (("$kernelSum".Trim()) -split '\s+')[0].ToLower()
        if ($got -ne $want) { throw "$AthenaRootfs carries kernel $got, but -AthenaBoot is ${want}: build the rootfs with KERNEL_IMAGE=$AthenaBoot" }
    } elseif ($kernelSum) {
        # Units would move to a kernel this release neither publishes nor gives the source of, and new
        # installs would start on an older one.
        throw "$AthenaRootfs carries a kernel but this release publishes no -AthenaBoot: give the boot image it was built with (and -AthenaKernelSource)"
    } else {
        Write-Warning "$AthenaRootfs carries no kernel: updates leave each unit's kernel as it is"
    }
}
# A kernel is published only with its source (the GPL), and the source must be what built it.
if ($AthenaBoot -and -not $AthenaKernelSource) {
    throw "-AthenaBoot needs -AthenaKernelSource: build.sh's out/linux-<kver>-techo5-athena-source.tar.xz, for the GPL"
}
if ($AthenaKernelSource -and -not $AthenaBoot) { throw "-AthenaKernelSource goes with the -AthenaBoot it is the source of" }
$athenaSourceName = $null
if ($AthenaKernelSource) {
    $chk = @((Join-Path $PSScriptRoot 'linux/athena/kernel/check-source.py'))
    # build.sh's kernel.config beside the image is compared too, when it is there.
    $cfg = Join-Path (Split-Path -Parent (Resolve-Path $AthenaBoot)) 'kernel.config'
    if (Test-Path -LiteralPath $cfg -PathType Leaf) { $chk += @('--config', $cfg) }
    $out = @(python @chk $AthenaKernelSource $AthenaBoot)
    $out | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "$AthenaKernelSource is not the source of $AthenaBoot (check-source.py)" }
    $kver = ($out | Where-Object { $_ -match '^kver (\d+\.\d+\.\d+)$' } | Select-Object -Last 1) -replace '^kver ', ''
    if (-not $kver) { throw "check-source.py named no kernel version" }
    $athenaSourceName = "linux-$kver-techo5-athena-$Version-source.tar.xz"
}
# The store image a new unit gets must hold this release: the slot in it carries this -AthenaBoot
# (its kernel's sum file, as deploy-rootfs.sh writes it) and says this version (etc/techo5-release,
# "techo5 rootfs <version> built ..."). The image is an ext4 filesystem, gzipped, and both are small
# files whose bytes sit whole in its blocks, so the image is read as a stream and searched.
if ($AthenaImage) {
    $bootSum = (Get-FileHash -Algorithm SHA256 $AthenaBoot).Hash.ToLower()
    $holds = "import gzip,sys`nwant={w.encode():False for w in sys.argv[2:]}`nf=gzip.open(sys.argv[1],'rb'); tail=b''`nwhile not all(want.values()):`n    b=f.read(1<<20)`n    if not b: break`n    for w in want:`n        if w in tail+b: want[w]=True`n    tail=(tail+b)[-256:]`nmissing=[w.decode() for w,ok in want.items() if not ok]`nfor m in missing: print('missing: '+m)`nsys.exit(1 if missing else 0)"
    python -c $holds $AthenaImage "$bootSum  boot-athena.img`n" "techo5 rootfs $Version built "
    if ($LASTEXITCODE -ne 0) { throw "$AthenaImage does not hold this release: build it (mkstore.sh) from the athena rootfs made for $Version with KERNEL_IMAGE=$AthenaBoot" }
}
# What a published image must never carry (tools/check-image.py): an SSH key, this computer's user and
# name, this checkout's path, or a wake word model that is not one of the public ones tools/fetch-inputs.py
# fetches. The Skylight's also must not carry the unit's own Wi-Fi and Bluetooth firmware, which is the
# vendor's: each unit's store gets its own from its vendor partition at install.
$scanDeny = @($env:USERNAME, $env:COMPUTERNAME, (Resolve-Path (Join-Path $PSScriptRoot '..')).Path) | Where-Object { $_ }
$publicModels = (Select-String -Path (Join-Path $PSScriptRoot 'fetch-inputs.py') -Pattern "'([a-z0-9_]+\.tflite)':" -AllMatches |
    ForEach-Object { $_.Matches } | ForEach-Object { $_.Groups[1].Value } | Sort-Object -Unique) -join ','
if (-not $publicModels) { throw "no public wake word models found in tools/fetch-inputs.py" }
$unitFirmware = @('brcmfmac43456-sdio.bin', 'brcmfmac43456-sdio.txt', 'BCM4345C5.hcd', 'fw_bcm43456c5_ag.bin', 'nvram_ap6256.txt')
function Test-Image([string]$Path, [string[]]$NoFile = @(), [switch]$EmptyKeys) {
    $a = @((Join-Path $PSScriptRoot 'check-image.py'), '--models', $publicModels)
    foreach ($d in $scanDeny) { $a += @('--deny', $d) }
    foreach ($n in $NoFile) { $a += @('--no-file', $n) }
    if ($EmptyKeys) { $a += '--empty-keys' }
    python -I @a $Path | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "$Path carries what no published image may (check-image.py)" }
}
if ($AthenaImage) { Test-Image $AthenaImage }
# The Shows' rootfs is published too, and athena's.
if ($Rootfs) { Test-Image $Rootfs }
if ($AthenaRootfs) { Test-Image $AthenaRootfs }
# Every Show rootfs carries the echo canceller and Spotify Connect: deploy-rootfs.sh copies them only when
# bin/ has them, so a worktree without the prebuilts copied in would ship without either.
foreach ($t in @($Rootfs, $AthenaRootfs, $SkylightRootfs) | Where-Object { $_ }) {
    $names = @(& tar -tzf $t) -replace '^\./', ''
    foreach ($b in 'techo5-aec', 'techo5-librespot') {
        if ($names -notcontains "usr/local/bin/$b") { throw "$t has no usr/local/bin/${b}: copy bin/$b-arm into bin/ before building" }
    }
}

# The Skylight: its three images and the kernel's source from one release, with its rootfs. install-skylight.py
# takes the installer, boot and store images from one release, and a kernel is published only with its
# source (the GPL).
$skylightSet = @($SkylightStore, $SkylightBoot, $SkylightInstaller, $SkylightKernelSource)
$skylightGiven = @($skylightSet | Where-Object { $_ }).Count
if ($skylightGiven -and $skylightGiven -ne $skylightSet.Count) {
    throw "SkylightStore, SkylightBoot, SkylightInstaller and SkylightKernelSource go together: install-skylight.py needs the three images from one release, and the GPL the kernel's source"
}
if ($skylightGiven -and -not $SkylightRootfs) { throw "the Skylight's images need -SkylightRootfs: the store holds it, and installed units update from it" }
if (-not $skylightGiven -and -not $NoSkylight -and -not $Prerelease -and $Version -notmatch '-') {
    throw "no Skylight images: install-skylight.py takes the latest release's, and would find none (pass -NoSkylight to release without them)"
}
if ($SkylightRootfs) {
    $board = (& tar -xzOf $SkylightRootfs ./etc/techo5/device.conf) -match '^T5_BOARD=' | Select-Object -Last 1
    if ($board -ne 'T5_BOARD=skylight') { throw "$SkylightRootfs is not a Skylight rootfs (device.conf: '$board'); build it with DEVICE_OVERLAY=tools/linux/skylight/overlay" }
    # It carries the kernel it was tested with, which goes to recovery once the slot is good
    # (echod/internal/update/kernel): this release's boot image, as for athena.
    $kernelSum = & tar -xzOf $SkylightRootfs ./usr/share/techo5/kernel/boot-skylight.img.sha256 2>$null
    if ($SkylightBoot) {
        if (-not $kernelSum) { throw "$SkylightRootfs carries no kernel: build it with KERNEL_IMAGE=$SkylightBoot (deploy-rootfs.sh), or updates never bring units this kernel" }
        $want = (Get-FileHash -Algorithm SHA256 $SkylightBoot).Hash.ToLower()
        $got = (("$kernelSum".Trim()) -split '\s+')[0].ToLower()
        if ($got -ne $want) { throw "$SkylightRootfs carries kernel $got, but -SkylightBoot is ${want}: build the rootfs with KERNEL_IMAGE=$SkylightBoot" }
    } elseif ($kernelSum) {
        throw "$SkylightRootfs carries a kernel but this release publishes no -SkylightBoot: give the boot image it was built with (and its source)"
    } else {
        Write-Warning "$SkylightRootfs carries no kernel: updates leave each unit's kernel as it is"
    }
    Test-Image $SkylightRootfs -NoFile $unitFirmware -EmptyKeys
}
$skylightSourceName = $null
if ($skylightGiven) {
    $kdir = Join-Path $PSScriptRoot 'linux/skylight/kernel'
    python -I (Join-Path $kdir 'check-boot.py') $SkylightBoot
    if ($LASTEXITCODE -ne 0) { throw "$SkylightBoot is not a Skylight boot image (check-boot.py)" }
    python -I (Join-Path $kdir 'check-boot.py') --installer --kernel-of $SkylightBoot $SkylightInstaller
    if ($LASTEXITCODE -ne 0) { throw "$SkylightInstaller is not the installer image of $SkylightBoot's kernel (check-boot.py --installer)" }
    # The source must be what built the kernel both images carry; build.sh's kernel.config beside the
    # boot image is compared too, when it is there.
    $chk = @((Join-Path $kdir 'check-source.py'))
    $cfg = Join-Path (Split-Path -Parent (Resolve-Path $SkylightBoot)) 'kernel.config'
    if (Test-Path -LiteralPath $cfg -PathType Leaf) { $chk += @('--config', $cfg) }
    $out = @(python -I @chk $SkylightKernelSource $SkylightBoot)
    $out | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { throw "$SkylightKernelSource is not the source of $SkylightBoot (check-source.py)" }
    $kver = ($out | Where-Object { $_ -match '^kver (\d+\.\d+\.\d+)$' } | Select-Object -Last 1) -replace '^kver ', ''
    if (-not $kver) { throw "check-source.py named no kernel version" }
    $skylightSourceName = "linux-$kver-techo5-skylight-$Version-source.tar.xz"
    # The store a new unit gets: ext4 labeled techo5-store (install-skylight.py checks it too), holding
    # this release's rootfs and kernel, and nothing a published image must not carry.
    python -I -c "import gzip,sys; b=gzip.open(sys.argv[1],'rb').read(2048); sys.exit(0 if b[1080:1082]==b'\x53\xef' and b[1144:1160].rstrip(b'\0')==b'techo5-store' else 1)" $SkylightStore
    if ($LASTEXITCODE -ne 0) { throw "$SkylightStore is not a gzipped ext4 labeled techo5-store (mkstore.sh)" }
    $bootSum = (Get-FileHash -Algorithm SHA256 $SkylightBoot).Hash.ToLower()
    $holds = "import gzip,sys`nwant={w.encode():False for w in sys.argv[2:]}`nf=gzip.open(sys.argv[1],'rb'); tail=b''`nwhile not all(want.values()):`n    b=f.read(1<<20)`n    if not b: break`n    for w in want:`n        if w in tail+b: want[w]=True`n    tail=(tail+b)[-256:]`nmissing=[w.decode() for w,ok in want.items() if not ok]`nfor m in missing: print('missing: '+m)`nsys.exit(1 if missing else 0)"
    python -I -c $holds $SkylightStore "$bootSum  boot-skylight.img`n" "techo5 rootfs $Version built "
    if ($LASTEXITCODE -ne 0) { throw "$SkylightStore does not hold this release: build it (mkstore.sh -k none) from the Skylight rootfs made for $Version with KERNEL_IMAGE=$SkylightBoot" }
    Test-Image $SkylightStore -NoFile $unitFirmware
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

    $agentAssets = @()
    if ($Agents) {
        foreach ($n in @('techo5-deck-windows-amd64.exe', 'techo5-deck-windows-arm64.exe', 'techo5-deck-macos-amd64', 'techo5-deck-macos-arm64', 'techo5-deck-linux-amd64', 'techo5-deck-linux-arm64')) {
            $f = Join-Path $Agents $n
            if (-not (Test-Path -LiteralPath $f -PathType Leaf)) { throw "deck agent not found: $f" }
            & gh attestation verify $f --repo $repo --source-ref "refs/tags/$Version" | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "$f is not attested as built by CI from tag $Version" }
            $named = Join-Path $bin $n
            Copy-Item $f $named -Force
            $agentAssets += $named
        }
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
    # athena's store image: what install-athena.py writes to a new unit's system partition. The rescue
    # environment on it takes SSH keys from the image (root/.ssh/authorized_keys), so a published one
    # must carry none; a key line in the image is an "ssh-<type> AAAA" string anywhere in its blocks.
    if ($AthenaImage) {
        $hasKey = "import gzip,re,sys; f=gzip.open(sys.argv[1],'rb'); tail=b''`nwhile True:`n    b=f.read(1<<20)`n    if not b: break`n    if re.search(rb'(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp\d+) AAAA', tail+b): sys.exit(1)`n    tail=b[-64:]"
        python -c $hasKey $AthenaImage
        if ($LASTEXITCODE -ne 0) { throw "$AthenaImage carries an SSH key: build it with mkstore.sh -k none" }
        $named = Join-Path $bin "techo5-system-athena-$Version.img.gz"
        Copy-Item $AthenaImage $named -Force
        $bootAssets += $named
    }
    if ($AthenaBoot) {
        python (Join-Path $PSScriptRoot 'linux/athena/kernel/check-boot.py') $AthenaBoot
        if ($LASTEXITCODE -ne 0) { throw "$AthenaBoot is not a clean athena boot image (check-boot.py): its tuning slot must be empty" }
        $named = Join-Path $bin "techo5-boot-athena-$Version.img"
        Copy-Item $AthenaBoot $named -Force
        $bootAssets += $named
    }
    # The Skylight's: the store image, the boot image and the installer image, each named in the signed
    # manifest for install-skylight.py.
    foreach ($img in @(@{ Path = $SkylightStore; Name = "techo5-store-skylight-$Version.img.gz" },
                       @{ Path = $SkylightBoot; Name = "techo5-boot-skylight-$Version.img" },
                       @{ Path = $SkylightInstaller; Name = "techo5-installer-skylight-$Version.img" })) {
        if (-not $img.Path) { continue }
        $named = Join-Path $bin $img.Name
        Copy-Item $img.Path $named -Force
        $bootAssets += $named
    }
    $skylightRootfsNamed = $null
    if ($SkylightRootfs) {
        $skylightRootfsNamed = Join-Path $bin "techo5-rootfs-skylight-$Version.tar.gz"
        Copy-Item $SkylightRootfs $skylightRootfsNamed -Force
    }
    $athenaRootfsNamed = $null
    if ($AthenaRootfs) {
        $athenaRootfsNamed = Join-Path $bin "techo5-rootfs-athena-$Version.tar.gz"
        Copy-Item $AthenaRootfs $athenaRootfsNamed -Force
    }

    Write-Host "== manifest"
    $from = "https://github.com/$repo/releases/download/$Version"
    $mk = @('run', './cmd/mkmanifest', '-version', $Version, '-title', "TECHO5 $Version", '-notes', $Notes,
        '-release-url', "https://github.com/$repo/releases/tag/$Version",
        '-from', $from, '-arm', (Join-Path $bin 'echod-arm'), '-arm-dot', (Join-Path $bin 'echod-arm-dot'),
        '-out', (Join-Path $bin 'manifest.json'), '-sign-key', $SignKey)
    if ($Rootfs) { $mk += @('-rootfs-arm', $Rootfs) }
    if ($DotRootfs) { $mk += @('-rootfs-arm-dot', $DotRootfs) }
    if ($athenaRootfsNamed) { $mk += @('-rootfs-arm-athena', $athenaRootfsNamed) }
    if ($skylightRootfsNamed) { $mk += @('-rootfs-arm-skylight', $skylightRootfsNamed) }
    # Under the signature, not just in SHA256SUMS: install-show.py writes a boot image to a unit, and
    # nothing signs SHA256SUMS, so whatever could serve a substituted list could serve the image too.
    foreach ($a in $bootAssets + $agentAssets) { $mk += @('-asset', $a) }
    & $Go @mk
    if ($LASTEXITCODE -ne 0) { throw 'mkmanifest failed' }
    Get-Content (Join-Path $bin 'manifest.json')
} finally { Pop-Location }

Write-Host "== release $Version"
$args = @('release', 'create', $Version, (Join-Path $bin 'echod-arm'), (Join-Path $bin 'echod-arm-dot'), (Join-Path $bin 'manifest.json'),
    (Join-Path $bin 'manifest.json.sig'), '--repo', $repo, '--title', $Version, '--notes', $Notes)
if ($Rootfs) { $args += $Rootfs }
if ($DotRootfs) { $args += $DotRootfs }
if ($athenaRootfsNamed) { $args += $athenaRootfsNamed }
if ($skylightRootfsNamed) { $args += $skylightRootfsNamed }
$args += $bootAssets
$args += $agentAssets
# Before SHA256SUMS is made from this list, so the sums cover the source too.
if ($athenaSourceName) {
    $named = Join-Path $bin $athenaSourceName
    Copy-Item $AthenaKernelSource $named -Force
    $args += $named
}
if ($skylightSourceName) {
    $named = Join-Path $bin $skylightSourceName
    Copy-Item $SkylightKernelSource $named -Force
    $args += $named
}
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

# The dev channel moves forward with every release, stable ones too, so it never offers something
# older than a unit already runs: Install then refuses it and the card never clears (techo5 issue #42).
# It never moves back: a stable release after a newer prerelease leaves dev on the prerelease. Its
# manifest names this release's own files, so only the manifest and its signature move.
function Get-VersionRank([string]$v) {
    if ($v -notmatch '^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+))?') { return $null }
    [pscustomobject]@{ Core = [version]"$($Matches[1]).$($Matches[2]).$($Matches[3])"; Pre = $Matches[4] }
}
function Test-VersionNewer([string]$a, [string]$b) {
    $x = Get-VersionRank $a; $y = Get-VersionRank $b
    if (-not $x -or -not $y) { return $true }
    if ($x.Core -ne $y.Core) { return $x.Core -gt $y.Core }
    if (-not $x.Pre) { return [bool]$y.Pre }
    if (-not $y.Pre) { return $false }
    return [string]::CompareOrdinal($x.Pre, $y.Pre) -gt 0
}
$devVersion = $null
try { $devVersion = ((& gh release download dev --repo $repo -p manifest.json -O - 2>$null) -join "`n" | ConvertFrom-Json).version } catch { }
if (-not $devVersion -or (Test-VersionNewer $Version $devVersion)) {
    & gh release upload dev (Join-Path $bin 'manifest.json') (Join-Path $bin 'manifest.json.sig') --repo $repo --clobber
    if ($LASTEXITCODE -ne 0) { throw "published, but the dev channel was not updated: upload manifest.json and manifest.json.sig to the dev release by hand" }
    Write-Host "dev channel: $Version (was $devVersion)"
} else {
    Write-Host "dev channel left on $devVersion, which is newer than $Version"
}

# Every boot image published has to be named in the signed manifest. An installer will not use a boot
# image the manifest does not cover, so one that is missing from it is published but unusable — which
# is exactly what happened to the 1st gen Show's boot image on v0.7.6, uploaded by hand after the
# release was made. The names come back from the release itself, so an upload by hand is caught by
# re-running this. SHA256SUMS is checked too, so what people verify by hand stays complete.
$published = @(& gh release view $Version --repo $repo --json assets -q '.assets[].name')
$named = @((Get-Content (Join-Path $bin 'manifest.json') -Raw | ConvertFrom-Json).assets.PSObject.Properties.Name)
$unsigned = @($published | Where-Object { ($_ -like 'techo5-boot-*' -or $_ -like 'techo5-deck-*' -or $_ -like 'techo5-system-*' -or
    $_ -like 'techo5-store-*' -or $_ -like 'techo5-installer-*') -and $named -notcontains $_ })
if ($unsigned) {
    throw "published, but these are not named in the signed manifest and nothing will trust them: $($unsigned -join ', ')"
}
$listed = @(Get-Content $sumsFile | ForEach-Object { ($_ -split '\s+', 2)[1].Trim() })
$missing = @($published | Where-Object { $_ -ne 'SHA256SUMS' -and $listed -notcontains $_ })
if ($missing) {
    throw "published, but these assets have no checksum in SHA256SUMS: $($missing -join ', ')"
}
Write-Host "published: https://github.com/$repo/releases/tag/$Version"
