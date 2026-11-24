<#
.SYNOPSIS
  Sign the add-on catalog (addons/catalog.json) with the release key and publish it.

.DESCRIPTION
  Devices fetch the catalog from
  https://github.com/HuskerMinion/techo5/releases/download/addons/catalog.json and believe it only with
  catalog.json.sig beside it, the release key's signature over "TECHO5 add-on catalog v1" and those bytes
  (the same key as manifest.json.sig, never the same signed text; echod/internal/update/trust.go). This
  script checks the catalog the way a device does (echod/cmd/mkcatalog), refuses one whose serial is
  below the published one's or that changes a published version, stamps it with an expiry a month out,
  signs it, and uploads both files to the "addons" release, which it makes the first time. A listing
  goes out this way on its own, without waiting for a release.

  It signs only the catalog that is on main: addons/catalog.json committed, and this checkout's HEAD the
  same commit as origin/main, so what devices believe is what was reviewed and merged, never a file
  edited on this computer.

  tools/release.ps1 runs this script with -Release, so every release carries the catalog as well and
  moves its expiry on: a device that hears from no release for a month stops offering installs from
  the catalog it has.

  The key never leaves the computer that holds it ($env:TECHO5_SIGN_KEY), as for releases.

.EXAMPLE
  .\tools\sign-catalog.ps1
  .\tools\sign-catalog.ps1 -SignKey C:\keys\techo5-release.key -WhatIf
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$SignKey = $env:TECHO5_SIGN_KEY,
    [string]$Go = 'go',
    [string]$Repo = 'HuskerMinion/techo5',
    # A release to attach the catalog to as well, as release.ps1 does with each one.
    [string]$Release = ''
)
$ErrorActionPreference = 'Stop'

# The standing release the catalog is published on by itself. Devices compile in its address
# (echod/internal/feature/addons, catalogURL).
$AddonCatalogTag = 'addons'

function Publish-AddonCatalog {
    param(
        [Parameter(Mandatory)][string]$SignKey,
        [string]$Go = 'go',
        [string]$Repo = 'HuskerMinion/techo5',
        # A release to attach the catalog to as well, as release.ps1 does with each one.
        [string]$Release = '',
        [switch]$DryRun
    )
    $root = Split-Path -Parent $PSScriptRoot
    $catalog = Join-Path $root 'addons/catalog.json'
    if (-not (Test-Path $catalog)) { throw "no catalog at $catalog" }
    if (-not $SignKey -or -not (Test-Path $SignKey)) { throw "no release signing key: set TECHO5_SIGN_KEY or pass -SignKey" }

    # Only the reviewed catalog: committed, and this checkout at origin/main.
    & git -C $root ls-files --error-unmatch addons/catalog.json 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "addons/catalog.json is not committed: sign only what is on main" }
    & git -C $root diff --quiet HEAD -- addons/catalog.json
    if ($LASTEXITCODE -ne 0) { throw "addons/catalog.json has changes that are not committed: sign only what is on main" }
    # mkcatalog is built from this tree (go run), so what checks and signs the catalog has to be main's
    # too: a change to it here, even untracked, would sign with code nobody reviewed.
    $dirty = & git -C $root status --porcelain -- echod addons tools/sign-catalog.ps1
    if ($LASTEXITCODE -ne 0) { throw "could not ask git whether the tree is clean" }
    if ($dirty) { throw "echod, addons or this script have changes that are not committed: sign from a clean checkout of main`n$($dirty -join "`n")" }
    & git -C $root fetch -q origin main
    if ($LASTEXITCODE -ne 0) { throw "could not fetch origin/main to check against" }
    $head = (& git -C $root rev-parse HEAD).Trim()
    $main = (& git -C $root rev-parse origin/main).Trim()
    if ($head -ne $main) { throw "this checkout is at $head, not origin/main ($main): sign the catalog from main" }

    $work = Join-Path ([IO.Path]::GetTempPath()) ("techo5-catalog-" + [guid]::NewGuid().ToString('N'))
    $published = Join-Path $work 'published'
    $signed = Join-Path $work 'signed'
    New-Item -ItemType Directory -Force -Path $published, $signed | Out-Null
    try {
        # What devices have now, if anything: the serial may not go below it, and no version published in
        # it may change. The same serial with the same entries is only a fresh expiry, which mkcatalog
        # allows, so a release with no catalog change still moves the expiry on.
        $have = $false
        & gh release view $AddonCatalogTag --repo $Repo --json tagName 2>$null | Out-Null
        $tagExists = ($LASTEXITCODE -eq 0)
        if ($tagExists) {
            # The release is there, so its catalog has to come down: signed without it, the serial and
            # the published versions would go unchecked (mkcatalog -previous). Its errors are shown.
            & gh release download $AddonCatalogTag --repo $Repo -p catalog.json -D $published --clobber
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path (Join-Path $published 'catalog.json'))) {
                throw "the $AddonCatalogTag release is there but its catalog.json could not be downloaded; not signing without checking against it"
            }
            $have = $true
        }

        Write-Host "== add-on catalog: checking and signing"
        $mk = @('run', './cmd/mkcatalog', '-in', $catalog, '-out', $signed, '-sign-key', $SignKey)
        if ($have) { $mk += @('-previous', (Join-Path $published 'catalog.json')) }
        Push-Location (Join-Path $root 'echod')
        try {
            & $Go @mk
            if ($LASTEXITCODE -ne 0) { throw 'mkcatalog refused the catalog' }
        } finally { Pop-Location }
        $files = @((Join-Path $signed 'catalog.json'), (Join-Path $signed 'catalog.json.sig'))
        if ($DryRun) {
            Write-Host "dry run: signed into $signed, nothing published"
            return
        }
        if (-not $tagExists) {
            & gh release create $AddonCatalogTag --repo $Repo --title 'Add-on catalog' --latest=false `
                --notes 'The signed add-on catalog devices read (addons/catalog.json). Not a firmware release.' @files
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
        Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
    }
}

Publish-AddonCatalog -SignKey $SignKey -Go $Go -Repo $Repo -Release $Release -DryRun:$WhatIfPreference
