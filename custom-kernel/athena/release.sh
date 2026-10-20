#!/bin/sh
# SPDX-License-Identifier: MIT
# Build, sign and publish an Echo Show 8 2nd gen (athena) release on Linux 7.2.11 to the private
# update channel ../release.sh publishes the Echo Show 5's to, signed with the same key.
#
#   release.sh [--dry-run | --publish] <version>       e.g. release.sh v1.2.17-mainline.1
#
# A release is what upstream's names it, so tools/install-athena.py takes its files as they are:
#   techo5-rootfs-athena-<version>.tar.gz   the root filesystem slot, keyed arm-athena in the manifest:
#                                           what the daemon installs into the spare slot over the network
#   techo5-system-athena-<version>.img.gz   the system partition for a new unit (mkstore.sh -k none)
#   techo5-boot-athena-<version>.img        the 7.2.11 boot image, its amp tuning slot empty
#   echod-arm, manifest.json, manifest.json.sig
#
# T5_ARCH=aarch64 builds the 64-bit userspace (build-rootfs.sh): the daemon ships as echod-arm64, and
# the slot is published under arm64-athena, which a 64-bit daemon asks for, and under arm-athena too,
# so a unit still on the 32-bit userspace moves onto it with an ordinary update.
#
# athena releases have a channel of their own, $CHANNEL/athena, which the daemon in this release's
# slot follows. One manifest at a time is "latest" on a channel, and a device is offered only a
# release that carries a rootfs for its board: on the Echo Show 5's channel, an athena release would
# hide the Echo Show 5's latest update, and the other way round.
#
# Settings as for ../release.sh (the environment, or ~/.config/techo5/release.env): CHANNEL, DEST and
# KEY; ATHENA_CHANNEL and ATHENA_DEST override $CHANNEL/athena and $DEST/athena. ATHENA_BOOT takes a
# boot-athena.img already built by build.sh, with its kernel.release beside it, instead of building it
# again; it is checked the same way. ATHENA_PANEL is the panel the boot image drives (build.sh's PANEL:
# ili9881c-boe by default, jd9365-boe-new for a unit whose boot loader says jd936x_id=0x?5).
#
# --dry-run builds, signs and checks the release in out/release/<version> and copies nothing.
# --publish copies a release staged by --dry-run, as it is, after checking its signature again: the
# files installed on a unit from the dry run are then the ones the channel offers.
#
# The slot carries the release's boot image, which the daemon writes to the boot partition once the slot
# is good, on trial (echod/internal/update/kernel), so a new kernel comes with the update too. The slot has
# no kernel modules, so it also runs on whichever kernel the unit had before.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
MODE=
case "${1:-}" in --dry-run|--publish) MODE=${1#--}; shift;; esac
V=${1:?usage: release.sh [--dry-run | --publish] <version, like v1.2.17-mainline.1>}
[ -f "$HOME/.config/techo5/release.env" ] && . "$HOME/.config/techo5/release.env"
CHANNEL=${CHANNEL:?set CHANNEL, the address the device fetches releases from}
ATHENA_CHANNEL=${ATHENA_CHANNEL:-$CHANNEL/athena}
if [ "$MODE" != dry-run ]; then
	DEST=${DEST:?set DEST, where release files are copied}
	ATHENA_DEST=${ATHENA_DEST:-$DEST/athena}
fi
KEY=${KEY:-$HOME/.config/techo5/release-key}
[ -f "$KEY" ] && [ -f "$KEY.pub" ] || { echo "no signing key at $KEY (and $KEY.pub)" >&2; exit 1; }
KREL=7.2.11-techo5-athena
PANEL=${ATHENA_PANEL:-ili9881c-boe}
O=$D/out
R=$O/release/$V

# Steps 1 to 4 build the release into $R; --publish takes the one already there.
build() {
mkdir -p "$O/kernel"
# 1. The boot image: build.sh in the kernel build container, on the tree an earlier build left in its
# volume when there is one (each of its steps is idempotent, and make rebuilds only what changed).
if [ -n "${ATHENA_BOOT:-}" ]; then
	# build.sh's output directory: the kernel is compressed in the image, so its release is read
	# from the kernel.release written beside it.
	cp "$ATHENA_BOOT" "$O/kernel/boot-athena.img"
	cp "$(dirname "$ATHENA_BOOT")/kernel.release" "$O/kernel/kernel.release"
else
	docker run --rm --platform linux/arm64 -v techo5-k6:/src -v "$ROOT:/repo:ro" -v "$O/kernel:/kout" \
	  -e PANEL="$PANEL" -w /repo techo5-kbuild bash -ec '
	    t=/src/athena-7.2.11/linux-7.2.11
	    [ -f $t/Makefile ] && export KTREE=$t
	    WORK=/src/athena-7.2.11 OUT=/kout custom-kernel/athena/build.sh'
fi
[ "$(cat "$O/kernel/kernel.release")" = "$KREL" ] || { echo "the kernel is not $KREL" >&2; exit 1; }
python3 -I "$ROOT/tools/linux/athena/kernel/check-boot.py" "$O/kernel/boot-athena.img"

# 2. The root filesystem slot, with a daemon that follows this channel and trusts this key.
VERSION="$V" RELEASES="$ATHENA_CHANNEL" RELEASE_KEY="$(cat "$KEY.pub")" KERNEL_IMAGE="$O/kernel/boot-athena.img" \
	sh "$D/build-rootfs.sh"

# 3. The system image for new units, from that slot.
sh "$D/build-store.sh" "$O/techo5-rootfs-athena.tar.gz" "$O/system.img"

# 4. The release, named as upstream names it, and its signed manifest.
rm -rf "$R"
mkdir -p "$R"
cp "$O/techo5-rootfs-athena.tar.gz" "$R/techo5-rootfs-athena-$V.tar.gz"
BIN=arm; [ "${T5_ARCH:-armv7}" = aarch64 ] && BIN=arm64
cp "$O/rootfs-stage/bin/techo5" "$R/echod-$BIN"
cp "$O/kernel/boot-athena.img" "$R/techo5-boot-athena-$V.img"
gzip -9n < "$O/system.img" > "$R/techo5-system-athena-$V.img.gz"
# A published store image carries no SSH key: its rescue would accept it on every interface.
python3 -I - "$R/techo5-system-athena-$V.img.gz" <<'EOF'
import gzip, re, sys
f, tail = gzip.open(sys.argv[1], 'rb'), b''
while True:
    b = f.read(1 << 20)
    if not b:
        break
    if re.search(rb'(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp\d+) AAAA', tail + b):
        sys.exit('%s carries an SSH key' % sys.argv[1])
    tail = b[-64:]
EOF

docker run --rm -v "$ROOT:/work" -v "$R:/rel" -v "$KEY:/key:ro" \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  -w /work/echod golang:1.26 go run ./cmd/mkmanifest -version "$V" -title "TECHO5 $V (Echo Show 8 2nd gen)" \
    -notes "Linux $KREL, for the $PANEL panel. The kernel goes on with the root filesystem, on trial, once the new slot is good." \
    -from "$ATHENA_CHANNEL/download/$V" -$BIN /rel/echod-$BIN -rootfs-arm-athena "/rel/techo5-rootfs-athena-$V.tar.gz" \
    $( [ $BIN = arm64 ] && echo -rootfs-arm64-athena "/rel/techo5-rootfs-athena-$V.tar.gz" ) \
    -asset "/rel/techo5-system-athena-$V.img.gz" -asset "/rel/techo5-boot-athena-$V.img" \
    -out /rel/manifest.json -sign-key /key
(cd "$R" && sha256sum -- * > SHA256SUMS)
}

if [ "$MODE" = publish ]; then
	[ -f "$R/manifest.json" ] || { echo "nothing staged in $R: run release.sh --dry-run $V first" >&2; exit 1; }
else
	build
fi

# The signature checked the way install-athena.py checks it (tools/techo5lib.py), against KEY.pub,
# and every file the manifest names against its sha256.
python3 -I - "$ROOT/tools" "$KEY.pub" "$R" "$ATHENA_CHANNEL/download/$V" <<'EOF'
import base64, hashlib, json, os, sys
sys.path.insert(0, sys.argv[1])
from techo5lib import ed25519_verify
pub = base64.b64decode(open(sys.argv[2]).read().strip())
rel, base = sys.argv[3], sys.argv[4]
raw = open(os.path.join(rel, 'manifest.json'), 'rb').read()
sig = base64.b64decode(open(os.path.join(rel, 'manifest.json.sig')).read().strip())
if not ed25519_verify(pub, raw, sig):
    sys.exit('manifest.json is not signed by the release key')
m = json.loads(raw)
entries = list((m.get('assets') or {}).values()) + list(m['rootfs'].values()) + list(m['binaries'].values())
for e in entries:
    name = e['url'][len(base) + 1:]
    if not e['url'].startswith(base + '/') or '/' in name:
        sys.exit('%s is not on this release\'s channel' % e['url'])
    data = open(os.path.join(rel, name), 'rb').read()
    if hashlib.sha256(data).hexdigest() != e['sha256'] or len(data) != e['size']:
        sys.exit('%s does not match the manifest' % name)
print('manifest %s signed by the release key; %d files match it' % (m['version'], len(entries)))
EOF

if [ "$MODE" = dry-run ]; then
	echo "dry run: $V staged in $R for $ATHENA_CHANNEL, nothing copied"
	exit 0
fi
# The release's own directory first, the channel's pointer to it last, as ../release.sh does.
host=${ATHENA_DEST%%:*} dir=${ATHENA_DEST#*:}
ssh "$host" "mkdir -p '$dir/download/$V' '$dir/latest/download'"
scp -q "$R"/* "$host:$dir/download/$V/"
ssh "$host" "cd '$dir' && cp download/$V/manifest.json latest/download/manifest.json.new &&
	cp download/$V/manifest.json.sig latest/download/manifest.json.sig.new &&
	mv latest/download/manifest.json.sig.new latest/download/manifest.json.sig &&
	mv latest/download/manifest.json.new latest/download/manifest.json"
curl -fsS "$ATHENA_CHANNEL/latest/download/manifest.json" >/dev/null || {
	echo "published, but $ATHENA_CHANNEL does not serve it: Home Assistant serves /config/www only if it existed when it started; restart it once" >&2
	exit 1
}
echo "published $V to $ATHENA_CHANNEL"
