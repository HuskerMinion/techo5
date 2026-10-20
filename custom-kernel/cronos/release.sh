#!/bin/sh
# Build, sign and publish a mainline release to a private update channel.
#
#   release.sh [--dry-run | --publish] <version>          e.g. release.sh v1.2.17-mainline.1
#
# --dry-run builds and signs the release in out/release/<version> and copies nothing, without asking
# the unit which kernel it runs. --publish copies a release staged by --dry-run, as it is, once the unit
# runs its kernel: the root filesystem installed by hand from the dry run is then the one the channel
# offers. With neither, the release is built and published in one go.
#
# The image carries firmware taken from the unit's own boot image (the microphone FPGA bitstreams and
# the amplifier's tuning), which is not ours to publish, so releases go to a server on the LAN rather
# than to GitHub. The device needs no login to fetch them and no HTTPS to trust them: it believes a
# manifest only when it is signed with the release key, whose public half is built into the daemon.
#
# Where the channel is is this household's own business, so it is not written here: the settings
# come from the environment, or from ~/.config/techo5/release.env, which this script reads first.
#
#   CHANNEL   the address the device fetches from, like http://<home assistant>:8123/local/techo5
#   DEST      where the files are copied, as scp takes it, like <ssh host>:/config/www/techo5
#   KEY       the signing key, base64 of an ed25519 seed (default: ~/.config/techo5/release-key);
#             KEY.pub next to it is its public half
#   DEVICE    an ssh host for a unit, checked to be running this build's kernel (unset skips it)
#
# A release replaces the root filesystem only; the boot partition is never written over the network.
# A release built on a kernel the unit is not running would leave it in rescue, because the slot's
# modules would not match: so the unit's kernel is checked first, and a new kernel goes on by
# fastboot (the boot image is in the release, signed with the rest) before the release is installed.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
MODE=
case "${1:-}" in --dry-run|--publish) MODE=${1#--}; shift;; esac
V=${1:?usage: release.sh [--dry-run | --publish] <version, like v1.2.17-mainline.1>}
[ -f "$HOME/.config/techo5/release.env" ] && . "$HOME/.config/techo5/release.env"
CHANNEL=${CHANNEL:?set CHANNEL, the address the device fetches releases from}
[ "$MODE" = dry-run ] || DEST=${DEST:?set DEST, where release files are copied}
KEY=${KEY:-$HOME/.config/techo5/release-key}
DEVICE=${DEVICE:-}
[ -f "$KEY" ] && [ -f "$KEY.pub" ] || { echo "no signing key at $KEY (and $KEY.pub)" >&2; exit 1; }

R=$D/out/release/$V
if [ "$MODE" = publish ]; then
	[ -f "$R/manifest.json" ] || { echo "nothing staged in $R: run release.sh --dry-run $V first" >&2; exit 1; }
else
	export VERSION="$V" RELEASES="$CHANNEL" RELEASE_KEY="$(cat "$KEY.pub")"
	sh "$D/initramfs/build-rescue.sh"
	"$D/build-rootfs.sh"
	sh "$D/mkboot.sh"
fi
rootfs=$D/out/techo5-rootfs-mainline.tar.gz
[ "$MODE" = publish ] && rootfs=$R/rootfs-$V.tar.gz
kernel=$(tar xzOf "$rootfs" ./etc/techo5/kernel-release)

if [ -n "$DEVICE" ] && [ "$MODE" != dry-run ]; then
	running=$(ssh -o ConnectTimeout=10 "$DEVICE" uname -r) || { echo "cannot ask $DEVICE which kernel it runs; DEVICE= skips the check" >&2; exit 1; }
	if [ "$running" != "$kernel" ]; then
		echo "$DEVICE runs $running and this release is for $kernel: flash out/techo5-linux-7.2.11.img first" >&2
		exit 1
	fi
fi

if [ "$MODE" != publish ]; then
rm -rf "$R"
mkdir -p "$R"
cp "$D/out/techo5-rootfs-mainline.tar.gz" "$R/rootfs-$V.tar.gz"
# T5_ARCH=aarch64 (build-rootfs.sh) ships the 64-bit daemon as echod-arm64 and publishes the slot under
# arm64, which a 64-bit daemon asks for, and under arm too, so a unit on the 32-bit userspace moves onto it.
BIN=arm; [ "${T5_ARCH:-armv7}" = aarch64 ] && BIN=arm64
cp "$D/out/rootfs-stage/bin/techo5" "$R/echod-$BIN"
cp "$D/out/techo5-linux-7.2.11.img" "$R/techo5-boot-mainline-$V.img"
docker run --rm -v "$ROOT:/work" -v "$R:/rel" -v "$KEY:/key:ro" \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  -w /work/echod golang:1.26 go run ./cmd/mkmanifest -version "$V" -title "TECHO5 $V" \
    -notes "Linux $kernel. A new kernel goes on by fastboot before this is installed." \
    -from "$CHANNEL/download/$V" -$BIN /rel/echod-$BIN -rootfs-arm "/rel/rootfs-$V.tar.gz" \
    $( [ $BIN = arm64 ] && echo -rootfs-arm64 "/rel/rootfs-$V.tar.gz" ) \
    -asset "/rel/techo5-boot-mainline-$V.img" -out /rel/manifest.json -sign-key /key
fi

# The signature checked against KEY.pub the way the installers check it (tools/techo5lib.py), and
# every file the manifest names against its sha256.
python3 -I - "$ROOT/tools" "$KEY.pub" "$R" "$CHANNEL/download/$V" <<'EOF'
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
	echo "dry run: $V (Linux $kernel) staged in $R for $CHANNEL, nothing copied"
	exit 0
fi

# The release's own directory first, the channel's pointer to it last: a device reading the channel
# while this runs finds the previous release or this whole one, never a manifest without its files.
host=${DEST%%:*} dir=${DEST#*:}
ssh "$host" "mkdir -p '$dir/download/$V' '$dir/latest/download'"
scp -q "$R"/* "$host:$dir/download/$V/"
ssh "$host" "cd '$dir' && cp download/$V/manifest.json latest/download/manifest.json.new &&
	cp download/$V/manifest.json.sig latest/download/manifest.json.sig.new &&
	mv latest/download/manifest.json.sig.new latest/download/manifest.json.sig &&
	mv latest/download/manifest.json.new latest/download/manifest.json"
curl -fsS "$CHANNEL/latest/download/manifest.json" >/dev/null || {
	echo "published, but $CHANNEL does not serve it: Home Assistant serves /config/www only if it existed when it started; restart it once" >&2
	exit 1
}
echo "published $V to $CHANNEL"
