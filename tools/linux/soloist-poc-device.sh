#!/bin/sh
# Experimental device installer. A bind mount keeps the original image intact; reboot rolls back.
# Usage: sh device.sh install /path/to/extracted/bundle | boot-on | boot-off | rollback
set -eu
base=/data/techo5-soloist
target=/usr/local/bin/techo5
case "${1:-}" in
install)
    bundle=${2:?directory containing the extracted bundle is required}
    test "$(id -u)" = 0
    test -f /lib/ld-musl-armhf.so.1
    (cd "$bundle" && sha256sum -c SHA256SUMS)
    # Refuse nested activation: rollback first, so the recovery path stays unambiguous.
    if test -f "$base/techo5-poc" && test "$(stat -c '%d:%i' "$target")" = "$(stat -c '%d:%i' "$base/techo5-poc")"; then
        echo 'Already activated; run rollback before replacing the experimental daemon.' >&2
        exit 1
    fi
    umask 077
    mkdir -p "$base/baseline" "$base/runtime"
    test -f "$base/baseline/techo5" || cp "$target" "$base/baseline/techo5"
    if test -f /usr/local/bin/techo5-librespot && ! test -f "$base/baseline/techo5-librespot"; then
        cp /usr/local/bin/techo5-librespot "$base/baseline/techo5-librespot"
    fi
    chmod 755 "$base" "$base/runtime"
    tar -xzf "$bundle/runtime.tar.gz" -C "$base/runtime"
    chown -R root:root "$base/runtime"
    cp "$bundle/techo5" "$base/techo5-poc"
    cp "$bundle/soloistd" "$base/soloistd"
    cp "$bundle/exec" "$base/exec"
    chmod 755 "$base/techo5-poc" "$base/soloistd" "$base/exec"
    "$base/exec" "$base/runtime/usr/bin/pulseaudio" --version
    mount --bind "$base/techo5-poc" "$target"
    killall -TERM techo5
    echo 'Activated until reboot. Open the TECHO5 setup page, Sound & Voice, to enter your key.'
    ;;
rollback)
    rm -f "$base/enable-at-boot"
    # Only remove our own topmost mount; never unmount an unrelated replacement.
    test -f "$base/techo5-poc"
    if test "$(stat -c '%d:%i' "$target")" != "$(stat -c '%d:%i' "$base/techo5-poc")"; then
        echo 'Experimental daemon is already inactive; automatic activation disabled.'
        exit 0
    fi
    touch /run/techo5/hold
    trap 'rm -f /run/techo5/hold' EXIT
    killall -TERM techo5 || true
    n=0
    while pidof techo5 >/dev/null; do
        n=$((n+1)); test "$n" -lt 25 || { echo 'Daemon did not stop; mount left intact.' >&2; exit 1; }
        sleep 1
    done
    umount "$target"
    echo 'Original daemon restored. Soloist key/session retained privately for a later experiment.'
    ;;
boot-on)
    test "$(id -u)" = 0
    test -x "$base/techo5-poc"
    runner=/usr/local/sbin/techo5-run
    if ! grep -q TECHO5_SOLOIST_POC_BOOT "$runner"; then
        # Preserve the installed runner, including any local additions. Add only an inert, gated hook.
        cp -p "$runner" "$base/baseline/techo5-run"
        awk '{print} /^export PATH$/ {
          print "# TECHO5_SOLOIST_POC_BOOT: remove enable-at-boot to restore normal boot."
          print "if [ -f /data/techo5-soloist/enable-at-boot ] && [ -x /data/techo5-soloist/techo5-poc ]; then"
          print "  if [ \"$(stat -c %d:%i /usr/local/bin/techo5)\" != \"$(stat -c %d:%i /data/techo5-soloist/techo5-poc)\" ]; then"
          print "    mount --bind /data/techo5-soloist/techo5-poc /usr/local/bin/techo5"
          print "  fi"
          print "fi"
        }' "$runner" > "$base/techo5-run.boot"
        test "$(grep -c TECHO5_SOLOIST_POC_BOOT "$base/techo5-run.boot")" = 1
        sh -n "$base/techo5-run.boot"
        mount -o remount,rw /
        trap 'mount -o remount,ro /' EXIT
        cp "$base/techo5-run.boot" "$runner.soloist-new"
        chmod 755 "$runner.soloist-new"
        mv "$runner.soloist-new" "$runner"
        mount -o remount,ro /
        trap - EXIT
    fi
    touch "$base/enable-at-boot"
    chmod 600 "$base/enable-at-boot"
    echo 'Experiment will reactivate at boot. rollback disables this and restores the original daemon.'
    ;;
boot-off)
    rm -f "$base/enable-at-boot"
    echo 'Automatic activation disabled; reboot or rollback restores the original daemon.'
    ;;
*) echo 'usage: sh device.sh install BUNDLE_DIRECTORY | boot-on | boot-off | rollback' >&2; exit 2 ;;
esac
