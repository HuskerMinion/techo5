<#
.SYNOPSIS
  Sign the add-on catalog (catalog.json in a techo5-addons checkout) with the release key and publish it.

.DESCRIPTION
  The catalog is published as two files, written from the one source (echod/cmd/mkcatalog):
    catalog-v2.json, the whole catalog in format 2, which devices that read format 2 fetch first, and
    believe only with catalog-v2.json.sig beside it, the release key's signature over
    "TECHO5 add-on catalog v2" and those bytes;
    catalog.json, format 1, for devices that read only that (up to v1.6.53), with every version they
    could not read left out, believed only with catalog.json.sig, over "TECHO5 add-on catalog v1".
  Both are at https://github.com/HuskerMinion/techo5/releases/download/addons/ (the same key as
  manifest.json.sig, never the same signed text; echod/internal/update/trust.go). This script checks
  the catalog the way a device does (mkcatalog), refuses one whose serial is below the published
  files' or that changes a version published in either, stamps it with an expiry a month out, signs
  both files, and uploads the four to the "addons" release, which it makes the first time. A listing
  goes out this way on its own, without waiting for a release.

  The catalog's source is its own repository, https://github.com/HuskerMinion/techo5-addons, where
  listings are submitted and reviewed; the signed copy is still published on this repository's
  "addons" release, whose address devices have compiled in. The techo5-addons checkout is, in order:
    -Catalog, a techo5-addons checkout or its catalog.json;
    $env:TECHO5_ADDONS, a techo5-addons checkout;
    techo5-addons beside this checkout (E:\projects\techo5-addons for E:\projects\techo5, and the same
    for a worktree beside it such as E:\projects\techo5-wt-rel1653);
    E:\projects\techo5-addons.

  It signs only what is on main, in both repositories: catalog.json committed and unchanged in a
  techo5-addons checkout whose HEAD is its origin/main, and mkcatalog built from a TECHO5 checkout with
  no changes to echod or this script and whose HEAD is its origin/main. So what devices believe is what
  was reviewed and merged, checked by code that was reviewed and merged, never a file edited on this
  computer.

  tools/release.ps1 runs this script with -Release, so every release carries the catalog as well and
  moves its expiry on: a device that hears from no release for a month stops offering installs from
  the catalog it has. It runs it with -WhatIf first, before it publishes anything.

  The key never leaves the computer that holds it ($env:TECHO5_SIGN_KEY), as for releases.

  -FirstFormat2 is for the one signing that first publishes catalog-v2.json, and only then. Once the
  "addons" release has catalog.json, this script refuses to sign without catalog-v2.json beside it to
  check against: compared with catalog.json alone, a version only catalog-v2.json lists (one that
  requires anything) could change unnoticed. The first time there is none yet, and -FirstFormat2 says
  that is expected; given when catalog-v2.json is already there, it is refused.

.EXAMPLE
  .\tools\sign-catalog.ps1
  .\tools\sign-catalog.ps1 -FirstFormat2    # once: the signing that first publishes catalog-v2.json
  .\tools\sign-catalog.ps1 -SignKey C:\keys\techo5-release.key -WhatIf
  .\tools\sign-catalog.ps1 -Catalog D:\src\techo5-addons
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$SignKey = $env:TECHO5_SIGN_KEY,
    [string]$Go = 'go',
    [string]$Repo = 'HuskerMinion/techo5',
    # A release to attach the catalog to as well, as release.ps1 does with each one.
    [string]$Release = '',
    # The techo5-addons checkout, or its catalog.json. Empty is $env:TECHO5_ADDONS, then techo5-addons
    # beside this checkout, then E:\projects\techo5-addons.
    [string]$Catalog = '',
    # The first signing that publishes catalog-v2.json, when the release has catalog.json and no
    # catalog-v2.json yet to check against (see the description). Refused once it has one.
    [switch]$FirstFormat2
)
$ErrorActionPreference = 'Stop'

# The standing release the catalog is published on by itself. Devices compile in its address
# (echod/internal/feature/addons, catalogURL).
$AddonCatalogTag = 'addons'

# Where the catalog's source lives; the checkout signed from has to have it as its origin.
$AddonCatalogRepo = 'HuskerMinion/techo5-addons'

# Resolve-AddonCatalog is the techo5-addons checkout to sign from: the folder that holds catalog.json.
function Resolve-AddonCatalog {
    param([string]$Catalog, [Parameter(Mandatory)][string]$Root)
    if ($Catalog) {
        if (Test-Path -LiteralPath $Catalog -PathType Leaf) {
            if ((Split-Path -Leaf $Catalog) -ne 'catalog.json') { throw "-Catalog $Catalog is not a catalog.json" }
            $Catalog = Split-Path -Parent (Resolve-Path -LiteralPath $Catalog).Path
        }
        if (-not (Test-Path -LiteralPath (Join-Path $Catalog 'catalog.json') -PathType Leaf)) {
            throw "-Catalog ${Catalog}: no catalog.json there"
        }
        return (Resolve-Path -LiteralPath $Catalog).Path
    }
    # TECHO5_ADDONS, when set, is the one: one that holds no catalog is a mistake to say, not a reason
    # to sign from another checkout.
    if ($env:TECHO5_ADDONS) {
        if (-not (Test-Path -LiteralPath (Join-Path $env:TECHO5_ADDONS 'catalog.json') -PathType Leaf)) {
            throw "TECHO5_ADDONS is $($env:TECHO5_ADDONS), which holds no catalog.json"
        }
        return (Resolve-Path -LiteralPath $env:TECHO5_ADDONS).Path
    }
    $candidates = @((Join-Path (Split-Path -Parent $Root) 'techo5-addons'), 'E:\projects\techo5-addons')
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath (Join-Path $c 'catalog.json') -PathType Leaf) { return (Resolve-Path -LiteralPath $c).Path }
    }
    throw "no techo5-addons checkout: clone https://github.com/$AddonCatalogRepo beside this one, or pass -Catalog or set TECHO5_ADDONS (tried $($candidates -join ', '))"
}

# Assert-OnMain checks that a checkout's HEAD is its origin/main, after fetching it.
function Assert-OnMain {
    param([Parameter(Mandatory)][string]$Dir, [Parameter(Mandatory)][string]$What)
    & git -C $Dir fetch -q origin main
    if ($LASTEXITCODE -ne 0) { throw "could not fetch origin/main of $What ($Dir) to check against" }
    $head = (& git -C $Dir rev-parse HEAD).Trim()
    $main = (& git -C $Dir rev-parse origin/main).Trim()
    if ($head -ne $main) { throw "$What ($Dir) is at $head, not origin/main ($main): sign the catalog from main" }
}

function Publish-AddonCatalog {
    param(
        [Parameter(Mandatory)][string]$SignKey,
        [string]$Go = 'go',
        [string]$Repo = 'HuskerMinion/techo5',
        # A release to attach the catalog to as well, as release.ps1 does with each one.
        [string]$Release = '',
        [string]$Catalog = '',
        [switch]$FirstFormat2,
        [switch]$DryRun
    )
    $root = Split-Path -Parent $PSScriptRoot
    if (-not $SignKey -or -not (Test-Path $SignKey)) { throw "no release signing key: set TECHO5_SIGN_KEY or pass -SignKey" }
    $addons = Resolve-AddonCatalog -Catalog $Catalog -Root $root
    $catalog = Join-Path $addons 'catalog.json'
    Write-Host "== add-on catalog: $catalog"

    # Only the reviewed catalog: catalog.json at the top of a checkout of techo5-addons, committed and
    # unchanged, and that checkout at origin/main.
    $top = (& git -C $addons rev-parse --show-toplevel 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $top) { throw "$addons is not a git checkout: clone https://github.com/$AddonCatalogRepo" }
    if ([IO.Path]::GetFullPath("$top".Trim()).TrimEnd('\', '/') -ne [IO.Path]::GetFullPath($addons).TrimEnd('\', '/')) {
        throw "$catalog is not at the top of its checkout ($("$top".Trim())): sign the techo5-addons repository's own catalog.json"
    }
    $origin = (& git -C $addons remote get-url origin 2>$null)
    if ($LASTEXITCODE -ne 0 -or "$origin" -notmatch "github\.com[:/]$([regex]::Escape($AddonCatalogRepo))(\.git)?/?$") {
        throw "the origin of $addons is '$origin', not ${AddonCatalogRepo}: sign only that repository's catalog"
    }
    & git -C $addons ls-files --error-unmatch catalog.json 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "catalog.json is not committed in ${addons}: sign only what is on main" }
    $dirty = & git -C $addons status --porcelain -- catalog.json
    if ($LASTEXITCODE -ne 0) { throw "could not ask git whether $addons is clean" }
    if ($dirty) { throw "catalog.json in $addons has changes that are not committed: sign only what is on main" }
    Assert-OnMain -Dir $addons -What 'the techo5-addons checkout'

    # mkcatalog is built from this tree (go run), so what checks and signs the catalog has to be main's
    # too: a change to it here, even untracked, would sign with code nobody reviewed.
    $dirty = & git -C $root status --porcelain -- echod tools/sign-catalog.ps1
    if ($LASTEXITCODE -ne 0) { throw "could not ask git whether the tree is clean" }
    if ($dirty) { throw "echod or this script have changes that are not committed: sign from a clean checkout of main`n$($dirty -join "`n")" }
    Assert-OnMain -Dir $root -What 'this TECHO5 checkout'

    $work = Join-Path ([IO.Path]::GetTempPath()) ("techo5-catalog-" + [guid]::NewGuid().ToString('N'))
    $published = Join-Path $work 'published'
    $signed = Join-Path $work 'signed'
    # Made and removed under -WhatIf as well: the dry run signs into it, and leaves nothing behind.
    New-Item -ItemType Directory -Force -Path $published, $signed -WhatIf:$false | Out-Null
    try {
        # What devices have now, if anything: the serial may not go below it, and no version published in
        # either file may change. The same serial with the same entries is only a fresh expiry, which
        # mkcatalog allows, so a release with no catalog change still moves the expiry on.
        $previous = @()
        $assetsJson = & gh release view $AddonCatalogTag --repo $Repo --json assets 2>$null
        $tagExists = ($LASTEXITCODE -eq 0)
        if ($tagExists) {
            $assets = @((($assetsJson -join "`n") | ConvertFrom-Json).assets | ForEach-Object { $_.name })
            # The release is there, so its catalog.json has to come down, and catalog-v2.json with it:
            # signed without them, the serial and the published versions would go unchecked (mkcatalog
            # -previous), and catalog.json alone does not hold the versions only catalog-v2.json lists.
            # Only the first format 2 signing, said with -FirstFormat2, goes ahead without
            # catalog-v2.json, there being none yet. Their errors are shown.
            $want = @('catalog.json')
            $hasV2 = $assets -contains 'catalog-v2.json'
            if ($hasV2 -and $FirstFormat2) {
                throw "-FirstFormat2 is for the first signing of catalog-v2.json, and the $AddonCatalogTag release has one already: sign without it"
            }
            if (-not $hasV2 -and -not $FirstFormat2) {
                throw "the $AddonCatalogTag release has no catalog-v2.json to check against; if this is the first signing that publishes it, pass -FirstFormat2"
            }
            if ($hasV2) { $want += 'catalog-v2.json' }
            foreach ($name in $want) {
                & gh release download $AddonCatalogTag --repo $Repo -p $name -D $published --clobber
                if ($LASTEXITCODE -ne 0 -or -not (Test-Path (Join-Path $published $name))) {
                    throw "the $AddonCatalogTag release is there but its $name could not be downloaded; not signing without checking against it"
                }
                $previous += @('-previous', (Join-Path $published $name))
            }
        }

        Write-Host "== add-on catalog: checking and signing"
        $mk = @('run', './cmd/mkcatalog', '-in', $catalog, '-out', $signed, '-sign-key', $SignKey) + $previous
        # mkcatalog holds the same line: given catalog.json alone it signs only when told this is the
        # first format 2 signing.
        if ($tagExists -and $FirstFormat2) { $mk += '-first-format2' }
        Push-Location (Join-Path $root 'echod')
        try {
            & $Go @mk
            if ($LASTEXITCODE -ne 0) { throw 'mkcatalog refused the catalog' }
        } finally { Pop-Location }
        # Both formats and both signatures, always together: one serial, from one source, so a device
        # that falls back from one to the other never finds it a different catalog.
        $files = @('catalog-v2.json', 'catalog-v2.json.sig', 'catalog.json', 'catalog.json.sig') | ForEach-Object { Join-Path $signed $_ }
        $absent = @($files | Where-Object { -not (Test-Path -LiteralPath $_ -PathType Leaf) })
        if ($absent) { throw "mkcatalog did not write $($absent -join ', '): not publishing half a catalog" }
        if ($DryRun) {
            Write-Host "dry run: the catalog checks out and signs; nothing published"
            return
        }
        if (-not $tagExists) {
            & gh release create $AddonCatalogTag --repo $Repo --title 'Add-on catalog' --latest=false `
                --notes "The signed add-on catalog devices read (from https://github.com/$AddonCatalogRepo). Not a firmware release." @files
            if ($LASTEXITCODE -ne 0) { throw 'making the addons release failed' }
        } else {
            & gh release upload $AddonCatalogTag @files --repo $Repo --clobber
            if ($LASTEXITCODE -ne 0) { throw 'uploading the catalog to the addons release failed' }
        }
        Write-Host "add-on catalog published: https://github.com/$Repo/releases/tag/$AddonCatalogTag"
        if ($Release) {
            & gh release upload $Release @files --repo $Repo --clobber
            if ($LASTEXITCODE -ne 0) { throw "the add-on catalog was not attached to $Release" }
        }
    } finally {
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue -WhatIf:$false
    }
}

Publish-AddonCatalog -SignKey $SignKey -Go $Go -Repo $Repo -Release $Release -Catalog $Catalog -FirstFormat2:$FirstFormat2 -DryRun:$WhatIfPreference
