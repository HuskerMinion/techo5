<#
.SYNOPSIS
  Install the TECHO5 daemon on an Echo Show 5 (2nd gen, cronos) running LineageOS 18.1.

.DESCRIPTION
  Does, over adb as root, everything the bench work did by hand:
    - installs the daemon as /system/bin/techo5 with an init service (tools/init/techo5.rc)
    - points Android at its null primary audio HAL so audioserver never touches the PCM devices
    - provisions /data/misc/techo5: the device name, the ESPHome API key, wake word models
    - fixes the mute button's key layout so it no longer sleeps the screen
    - starts the service (or reboots with -Reboot)

  Prerequisites on the Show: LineageOS 18.1 for cronos, USB debugging on, "Rooted debugging" on
  in developer options. On the PC: adb, and a built daemon at bin\echod-arm
  (GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o bin/echod-arm ./cmd/echod, run inside echod/).

.EXAMPLE
  .\tools\install-cronos.ps1 -Serial G000000000000000 -Name "Bench Show"
#>
param(
    [Parameter(Mandatory)][string]$Serial,
    [Parameter(Mandatory)][string]$Name,
    # Where the API encryption key is kept on the PC (default <TECHO5_BACKUPS or backups>\<serial>\
    # home-assistant.key, as the other installers). Created if missing; Home Assistant asks for it.
    [string]$KeyFile,
    [string]$Adb = 'adb',
    [string]$Binary = (Join-Path $PSScriptRoot '..\bin\echod-arm'),
    [string]$Rc = (Join-Path $PSScriptRoot 'init\techo5.rc'),
    # microWakeWord models to install, by id as published in github.com/esphome/micro-wake-word-models.
    [string[]]$WakeWords = @('alexa', 'okay_nabu', 'hey_jarvis'),
    [switch]$Reboot
)
$ErrorActionPreference = 'Stop'

function Sh([string]$cmd) { & $Adb -s $Serial shell $cmd }
function Push([string]$local, [string]$remote) { & $Adb -s $Serial push $local $remote | Out-Null }

if (-not (Test-Path $Binary)) { throw "daemon binary not found at $Binary; build it first (see help)" }
if (-not (Test-Path $Rc)) { throw "init script not found at $Rc" }

Write-Host "== device"
$dev = (& $Adb -s $Serial shell getprop ro.product.device).Trim()
if ($dev -ne 'cronos') { throw "device $Serial reports '$dev', not cronos" }
& $Adb -s $Serial root | Out-Null; Start-Sleep -Seconds 3
$id = (Sh 'id').Trim()
if ($id -notmatch '^uid=0') { throw "adb is not root ($id); turn on Rooted debugging in Developer options" }
Write-Host "   cronos, adb root ok, LineageOS $((Sh 'getprop ro.build.display.id').Trim())"

Write-Host "== key"
if (-not $KeyFile) {
    # The same folder the other installers use: TECHO5_BACKUPS when it is set, else backups\ in the repository.
    $backups = if ($env:TECHO5_BACKUPS) { $env:TECHO5_BACKUPS } else { Join-Path $PSScriptRoot '..\backups' }
    $KeyFile = Join-Path $backups "$Serial\home-assistant.key"
}
New-Item -ItemType Directory -Force (Split-Path $KeyFile) | Out-Null
if (Test-Path $KeyFile) {
    $psk = (Get-Content $KeyFile -Raw).Trim()
} else {
    $bytes = [byte[]]::new(32); [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $psk = [Convert]::ToBase64String($bytes)
    [IO.File]::WriteAllText($KeyFile, $psk)
    Write-Host "   new key written to $KeyFile - keep it; Home Assistant asks for it when adding the device"
}
if ([Convert]::FromBase64String($psk).Length -ne 32) { throw "key in $KeyFile is not 32 bytes base64" }

Write-Host "== wake word models"
# From a commit, not a branch, and each file checked against its sha256 before it goes near the unit: these
# land in the directory the daemon parses as root. The same pins as tools/fetch-inputs.py (MODELS_COMMIT,
# MODEL_SHA256); move them together.
$modelsCommit = '05b65922cc433c9df13e98e32a7fe520758c837e'  # esphome/micro-wake-word-models, 2025-03-21
$modelSha256 = @{
    'okay_nabu.tflite'   = '0689abe1912a95a3318a0d8cb2e67bad0cbcfe3e24dd6e050c75debddfb6f891'
    'okay_nabu.json'     = '6dd65604f70fe5ea9d1af73a7bf239529d1fbabc363807f45d2b22ce464ddbed'
    'hey_jarvis.tflite'  = '21a7976add39ee24ec96c63d96b7aaa18e24d1d9824b963e451da8feb4b78b77'
    'hey_jarvis.json'    = 'b153867d818675d8abcc9dace474afe7f83551ae0d5a9b1d71a98681320185af'
    'hey_mycroft.tflite' = 'c2a9b6ed51182db72e014781d5a4ece1929dc232a40b5b4be384f0295f0e1571'
    'hey_mycroft.json'   = '57b2b06fe5fdbbe834a242fabc7af31e4194a550fc382b2c88636a6d62d0d57e'
    'alexa.tflite'       = '9011a8155b04de858c48038529235cbc0e42e9fca05a55bf588cb80a653a723b'
    'alexa.json'         = '1d999798b35b1fe2606465b75ab840be51c1811d2909d5e620cefb6e96f8abd0'
}
$tmp = Join-Path ([IO.Path]::GetTempPath()) "techo5-models"
New-Item -ItemType Directory -Force $tmp | Out-Null
foreach ($w in $WakeWords) {
    foreach ($ext in 'json', 'tflite') {
        $file = "$w.$ext"
        if (-not $modelSha256.ContainsKey($file)) {
            throw "no pinned checksum for wake word '$w'; -WakeWords takes: $((($modelSha256.Keys | ForEach-Object { $_ -replace '\.(json|tflite)$', '' }) | Sort-Object -Unique) -join ', ')"
        }
        $out = Join-Path $tmp $file
        Invoke-WebRequest -Uri "https://raw.githubusercontent.com/esphome/micro-wake-word-models/$modelsCommit/models/v2/$file" -OutFile $out -UseBasicParsing
        $got = (Get-FileHash -Algorithm SHA256 $out).Hash.ToLower()
        if ($got -ne $modelSha256[$file]) {
            Remove-Item $out -Force
            throw "$file is not the file that was pinned (sha256 $got); nothing was installed"
        }
    }
    Write-Host "   $w"
}

Write-Host "== stopping a running daemon"
# TERM first: a daemon on an update trial clears its trial marker on a clean stop, and init's own
# stop is a SIGKILL, which the next start would read as a crash and roll back.
Sh 'for p in $(pidof echod techo5); do kill -TERM $p; done; sleep 2; setprop ctl.stop techo5 2>/dev/null; exit 0' | Out-Null

Write-Host "== /data/misc/techo5"
Sh 'mkdir -p /data/misc/techo5/models /data/techo5; chmod 700 /data/misc/techo5' | Out-Null
$nameTmp = Join-Path ([IO.Path]::GetTempPath()) 'techo5-name'; [IO.File]::WriteAllText($nameTmp, $Name)
$pskTmp = Join-Path ([IO.Path]::GetTempPath()) 'techo5-psk'; [IO.File]::WriteAllText($pskTmp, $psk)
Push $nameTmp /data/misc/techo5/name
Push $pskTmp /data/misc/techo5/psk
Get-ChildItem $tmp | ForEach-Object { Push $_.FullName "/data/misc/techo5/models/$($_.Name)" }
Sh 'chmod 600 /data/misc/techo5/psk /data/misc/techo5/name; chmod 644 /data/misc/techo5/models/*' | Out-Null
Remove-Item $nameTmp, $pskTmp -Force

Write-Host "== /system: daemon, init service, null audio HAL"
Push $Binary /data/local/tmp/techo5.new
Push $Rc /data/local/tmp/techo5.rc
$sys = @'
set -e
mount -o remount,rw /
# A binary installed by hand supersedes any update trial: without this the next start would take
# the trial marker as a crashed update and put the previous binary back over the new one.
rm -f /system/bin/techo5.prev /data/misc/techo5/updating
setprop echolocal.trial ""
cp /data/local/tmp/techo5.new /system/bin/techo5 && chmod 755 /system/bin/techo5 && chcon u:object_r:system_file:s0 /system/bin/techo5
sed -i "s/\r$//" /data/local/tmp/techo5.rc
cp /data/local/tmp/techo5.rc /system/etc/init/techo5.rc && chmod 644 /system/etc/init/techo5.rc && chcon u:object_r:system_file:s0 /system/etc/init/techo5.rc
if grep -q "^ro.hardware.audio.primary=amazon_wrapper$" /system/build.prop; then
  cp /system/build.prop /data/techo5/build.prop.orig
  sed -i "s/^ro.hardware.audio.primary=amazon_wrapper$/ro.hardware.audio.primary=default\n# techo5: was amazon_wrapper; the null HAL keeps audioserver off the PCM devices the daemon owns/" /system/build.prop
  echo "   audio HAL switched to default (original at /data/techo5/build.prop.orig); takes effect at reboot"
else
  echo "   audio HAL already: $(grep ^ro.hardware.audio.primary= /system/build.prop)"
fi
mount -o remount,ro /
rm -f /data/local/tmp/techo5.new /data/local/tmp/techo5.rc
'@
$sysTmp = Join-Path ([IO.Path]::GetTempPath()) 'techo5-sys.sh'; [IO.File]::WriteAllText($sysTmp, ($sys -replace "`r`n", "`n"))
Push $sysTmp /data/local/tmp/techo5-sys.sh
# adb shell does not carry the script's exit status back reliably, so the script says it finished: set -e
# stops it at the first failure, and without the marker nothing after this point is done and "Done" is not
# printed over a /system that was left half written.
$out = @(Sh 'sh /data/local/tmp/techo5-sys.sh && echo TECHO5-SYS-OK; rm -f /data/local/tmp/techo5-sys.sh')
Remove-Item $sysTmp -Force
$out | Where-Object { $_ -ne 'TECHO5-SYS-OK' } | ForEach-Object { Write-Host $_ }
if ($out -notcontains 'TECHO5-SYS-OK') {
    throw "installing into /system failed (see above); the daemon, its init service or the audio HAL change may not be in place"
}

Write-Host "== mute button key layout"
Sh 'mkdir -p /data/system/devices/keylayout; printf "key 116 WAKEUP\n" > /data/system/devices/keylayout/gpio-privacy-button.kl; chmod 644 /data/system/devices/keylayout/gpio-privacy-button.kl; chown system:system /data/system/devices/keylayout/gpio-privacy-button.kl' | Out-Null
Write-Host "   key 116 -> WAKEUP (loads at reboot)"

$hal = (Sh 'getprop ro.hardware.audio.primary').Trim()
if ($Reboot -or $hal -ne 'default') {
    Write-Host "== rebooting (the audio HAL change needs it)"
    & $Adb -s $Serial reboot
} else {
    Write-Host "== starting the service"
    Sh 'setprop ctl.start techo5; sleep 3; echo "   techo5: $(getprop init.svc.techo5)"'
}

Write-Host ""
Write-Host "Done. Home Assistant will discover '$Name' as an ESPHome device; paste the key from $KeyFile when asked."
Write-Host "Logs: adb -s $Serial logcat -s techo5"
