#!/bin/sh
# Explicit, disruptive native-device tests. Never accepts or prints an API key.
# Build the expired fixture from soloist-expired-probe.go.txt and copy to $base/expired-probe.
set -eu
base=/data/techo5-soloist
state=${TECHO5_SOLOIST_STATE:-/data/misc/techo5-soloist}
managed=$state/data/soloistd/bin/soloist
config=$state/config/soloistd/config.json
test "$(id -u)" = 0
case "$state" in /data/*) ;; *) exit 2 ;; esac
test -f "$managed"
key_before=$(sha256sum "$config" | cut -d ' ' -f 1)
case "${1:-}" in
expiry)
    test -f "$base/expired-probe"
    oldpid=$(cat "$state/data/soloist/soloist.pid")
    test "$(readlink "/proc/$oldpid/exe")" = "$managed"
    cp "$managed" "$base/baseline/soloist-before-expiry-test"
    cp "$base/expired-probe" "$managed.expired-next"
    chmod 755 "$managed.expired-next"
    chown streaming:streaming "$managed.expired-next"
    restore() {
        if test "$(stat -c %s "$managed" 2>/dev/null || echo 0)" -lt 20000000; then
            cp "$base/baseline/soloist-before-expiry-test" "$managed.restore-next"
            chown streaming:streaming "$managed.restore-next"
            mv "$managed.restore-next" "$managed"
        fi
    }
    trap restore EXIT
    mv "$managed.expired-next" "$managed"
    kill -KILL "$oldpid"
    n=0
    while test "$n" -lt 40; do
        n=$((n+1))
        sleep 2
        if test "$(stat -c %s "$managed" 2>/dev/null || echo 0)" -gt 20000000; then
            pid=$(cat "$state/data/soloist/soloist.pid" 2>/dev/null || true)
            if test -n "$pid" && test "$pid" != "$oldpid" && kill -0 "$pid" 2>/dev/null; then
                if timeout 4 "$base/exec" "$managed" ctl status -D "$state/data/soloist" > "$base/expiry-status" 2>/dev/null; then
                    grep -q 'logged in: yes' "$base/expiry-status" && break
                fi
            fi
        fi
    done
    test "$n" -lt 40
    test "$(sha256sum "$config" | cut -d ' ' -f 1)" = "$key_before"
    echo 'PASS: exit-10 fixture replaced by official client; restarted and restored pairing; key unchanged.'
    ;;
failed-update)
    oldpid=$(cat "$state/data/soloist/soloist.pid")
    binary_before=$(sha256sum "$managed" | cut -d ' ' -f 1)
    cleanup() {
        iptables-legacy -D OUTPUT -m owner --uid-owner 88 -p tcp --dport 443 -m comment --comment techo5-soloist-test -j REJECT 2>/dev/null || true
        ip6tables-legacy -D OUTPUT -m owner --uid-owner 88 -p tcp --dport 443 -m comment --comment techo5-soloist-test -j REJECT 2>/dev/null || true
    }
    trap cleanup EXIT
    iptables-legacy -I OUTPUT 1 -m owner --uid-owner 88 -p tcp --dport 443 -m comment --comment techo5-soloist-test -j REJECT
    ip6tables-legacy -I OUTPUT 1 -m owner --uid-owner 88 -p tcp --dport 443 -m comment --comment techo5-soloist-test -j REJECT
    if timeout 10 su -s /bin/sh streaming -c "XDG_CONFIG_HOME=$state/config XDG_DATA_HOME=$state/data XDG_CACHE_HOME=$state/cache SOLOIST_EXEC=$base/exec $base/soloistd update" >/dev/null 2>&1; then
        echo 'FAIL: update unexpectedly succeeded while HTTPS was rejected.' >&2
        exit 1
    fi
    cleanup
    trap - EXIT
    test "$(sha256sum "$managed" | cut -d ' ' -f 1)" = "$binary_before"
    test "$(sha256sum "$config" | cut -d ' ' -f 1)" = "$key_before"
    kill -0 "$oldpid"
    echo 'PASS: rejected HTTPS download preserved binary, key and running client; network rules removed.'
    ;;
*) echo 'usage: sh test-soloist-poc-recovery.sh expiry | failed-update' >&2; exit 2 ;;
esac
