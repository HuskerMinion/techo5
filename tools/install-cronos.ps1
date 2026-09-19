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
    # Where the API encryption key is kept on the PC (default backups\<serial>\home-assistant.key, as
    # the other installers). Created if missing; Home Assistant asks for it.
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
if (-not $KeyFile) { $KeyFile = Join-Path $PSScriptRoot "..\backups\$Serial\home-assistant.key" }
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
$tmp = Join-Path ([IO.Path]::GetTempPath()) "techo5-models"
New-Item -ItemType Directory -Force $tmp | Out-Null
foreach ($w in $WakeWords) {
    foreach ($ext in 'json', 'tflite') {
        $url = "https://raw.githubusercontent.com/esphome/micro-wake-word-models/main/models/v2/$w.$ext"
        Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmp "$w.$ext") -UseBasicParsing
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
Sh 'sh /data/local/tmp/techo5-sys.sh; rm -f /data/local/tmp/techo5-sys.sh'
Remove-Item $sysTmp -Force

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
