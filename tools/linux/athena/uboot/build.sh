#!/bin/sh
# build.sh — the boot layer for the Echo Show 8 2nd gen (athena), built from source: mainline U-Boot
# with abboot, an A/B boot menu, from u-boot-amazon-echo (GPL-2.0-or-later; issue #336). The release
# workflow (.github/workflows/build.yml, job uboot) runs this and attests what it makes; it runs the
# same anywhere with Docker and git.
#
#   tools/linux/athena/uboot/build.sh [--no-source] [OUT]
#
# Both sources are pinned here: u-boot-amazon-echo at $COMMIT (the boot layer and the athena board,
# configuration only), and upstream U-Boot at $UBOOT_TAG, checked against $UBOOT_SHA by
# u-boot-amazon-echo's own uboot/build.sh, which must name the same tag and commit. That script
# builds in Docker (its uboot/docker/Dockerfile; the U-Boot tree in the Docker volume
# u-boot-amazon-echo-src) the board's "final" variant, the one for the boot partition, with
# techo5-athena.config here applied on top of its defconfig. The build is reproducible: the same
# commits and options give the same bytes.
#
# OUT (default build/athena-uboot) gets:
#   techo5-uboot-athena.bin               U-Boot, as built (u-boot.bin): what a release publishes as
#                                         techo5-uboot-athena-<version>.bin, named in its signed
#                                         manifest. install-athena.py --boot-layer packs it into the
#                                         release's boot image shape (pack-uboot.py here) and fills in
#                                         the unit's amp tuning.
#   techo5-uboot-athena.config            the full configuration it was built with
#   techo5-uboot-athena-source.tar.gz     unless --no-source: the whole source, for the GPL: U-Boot at
#                                         $UBOOT_TAG and u-boot-amazon-echo at $COMMIT (git archive of
#                                         each), techo5-athena.config, this script and a README
#   SHA256SUMS
#
# pack-uboot.py here is u-boot-amazon-echo's tools/pack-uboot.py at $COMMIT but for its two comment
# lines; this stops if it is not. Moving to another commit is one edit of COMMIT (and of the copy,
# when the packer changed): read what changed there first.
set -eu
REPO=https://github.com/nuno407/u-boot-amazon-echo.git
COMMIT=1cc4c335deb7da27ae1cd2a1d288f517d99f8b07
UBOOT_REPO=https://github.com/u-boot/u-boot.git
UBOOT_TAG=v2026.10
UBOOT_SHA=5508406582f6f7120dce0c4b3059819a41788db2
BOARD=echo-show-8-gen2
MARKER=techo5-athena-uboot-image

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../../.." && pwd)
SOURCE=1
[ "${1:-}" = --no-source ] && { SOURCE=; shift; }
OUT=${1:-$ROOT/build/athena-uboot}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
command -v docker >/dev/null || { echo "$0: needs Docker (u-boot-amazon-echo's uboot/build.sh builds in it)" >&2; exit 1; }

W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT

# fetch DIR URL REF WANT: a shallow checkout of REF, which must be the commit WANT.
fetch() {
	git init -q "$1"
	git -C "$1" fetch -q --depth 1 "$2" "$3"
	git -C "$1" -c advice.detachedHead=false checkout -q FETCH_HEAD
	got=$(git -C "$1" rev-parse HEAD)
	[ "$got" = "$4" ] || { echo "$0: $2 $3 is $got, not $4" >&2; exit 1; }
}

echo "== u-boot-amazon-echo $COMMIT"
fetch "$W/u-boot-amazon-echo" "$REPO" "$COMMIT" "$COMMIT"
A=$W/u-boot-amazon-echo
for v in UBOOT_TAG UBOOT_SHA; do
	theirs=$(sed -n "s/^$v=//p" "$A/uboot/build.sh" | head -1)
	eval ours=\$$v
	[ "$theirs" = "$ours" ] || { echo "$0: its uboot/build.sh has $v=$theirs, this script $ours" >&2; exit 1; }
done
sed '3,4d' "$HERE/pack-uboot.py" | cmp -s - "$A/tools/pack-uboot.py" ||
	{ echo "$0: pack-uboot.py here is not its tools/pack-uboot.py" >&2; exit 1; }

echo "== U-Boot $UBOOT_TAG ($UBOOT_SHA), board $BOARD, variant final, with techo5-athena.config"
sh "$A/uboot/build.sh" --boards "$BOARD" --variants final --config "$HERE/techo5-athena.config" --no-test --out "$W/out"
bin=$W/out/u-boot-$BOARD-final.bin
cfg=$W/out/.config-$BOARD-final
# What install-athena.py and echod rely on: an arm64 Image header (LK starts U-Boot as a kernel), the
# marker, and the amp tuning carried.
python3 -I -c "import sys; b=open(sys.argv[1],'rb').read(); sys.exit(0 if b[0x38:0x3c]==b'ARM\x64' and sys.argv[2].encode() in b else 1)" "$bin" "$MARKER" ||
	{ echo "$0: $bin has no arm64 Image header or no $MARKER" >&2; exit 1; }
grep -qx 'CONFIG_AB_DT_CARRY="techo5,dsp-config"' "$cfg" || { echo "$0: the build does not carry the amp tuning" >&2; exit 1; }
cp "$bin" "$OUT/techo5-uboot-athena.bin"
cp "$cfg" "$OUT/techo5-uboot-athena.config"
files="techo5-uboot-athena.bin"

if [ -n "$SOURCE" ]; then
	echo "== the source archive"
	fetch "$W/u-boot" "$UBOOT_REPO" "refs/tags/$UBOOT_TAG" "$UBOOT_SHA"
	S=$W/source/techo5-uboot-athena-source
	mkdir -p "$S"
	git -C "$W/u-boot" archive --format=tar --prefix=u-boot-${UBOOT_TAG#v}/ HEAD | tar -xf - -C "$S"
	git -C "$A" archive --format=tar --prefix=u-boot-amazon-echo/ HEAD | tar -xf - -C "$S"
	cp "$HERE/techo5-athena.config" "$HERE/build.sh" "$S/"
	cp "$cfg" "$S/techo5-uboot-athena.config-full"
	cat > "$S/README.txt" <<EOF
The source of TECHO5's U-Boot for the Echo Show 8 2nd gen (techo5-uboot-athena-<version>.bin)

U-Boot is GPL-2.0-or-later; so are the boot layer (abboot) and the board files of u-boot-amazon-echo.

- u-boot-${UBOOT_TAG#v}/: upstream U-Boot, tag $UBOOT_TAG, commit $UBOOT_SHA
  ($UBOOT_REPO), unmodified.
- u-boot-amazon-echo/: commit $COMMIT of $REPO: the boot layer, the patches and the board, which its
  uboot/install.sh adds to the U-Boot tree, and its build script (uboot/build.sh, in Docker).
- techo5-athena.config: TECHO5's values, applied on top of the board's defconfig.
- techo5-uboot-athena.config-full: the whole configuration of the build.
- build.sh: TECHO5's script that runs it (tools/linux/athena/uboot/build.sh in TECHO5's repository):
  u-boot-amazon-echo/uboot/build.sh --boards $BOARD --variants final --config techo5-athena.config
  builds u-boot-$BOARD-final.bin, published as techo5-uboot-athena-<version>.bin.
EOF
	# Reproducible: names sorted, times and owners zero, gzip without a name or time.
	python3 -I - "$W/source" techo5-uboot-athena-source "$OUT/techo5-uboot-athena-source.tar.gz" <<'PY'
import gzip, os, sys, tarfile
base, top, out = sys.argv[1:]
def clean(t):
    t.mtime, t.uid, t.gid, t.uname, t.gname = 0, 0, 0, '', ''
    t.mode = 0o755 if t.isdir() or t.mode & 0o111 else 0o644
    return t
with open(out + '.part', 'wb') as f, gzip.GzipFile(filename='', mode='wb', fileobj=f, mtime=0, compresslevel=9) as z, \
        tarfile.open(fileobj=z, mode='w', format=tarfile.PAX_FORMAT) as tar:
    for d, dirs, files in os.walk(os.path.join(base, top)):
        dirs.sort()
        rel = os.path.relpath(d, base)
        tar.add(d, rel, recursive=False, filter=clean)
        # Links to folders too: os.walk lists them with the folders but does not go into them.
        for n in sorted(files + [x for x in dirs if os.path.islink(os.path.join(d, x))]):
            tar.add(os.path.join(d, n), os.path.join(rel, n), recursive=False, filter=clean)
os.replace(out + '.part', out)
PY
	files="$files techo5-uboot-athena-source.tar.gz"
fi
cd "$OUT" && sha256sum $files > SHA256SUMS 2>/dev/null || shasum -a 256 $files > SHA256SUMS
cat SHA256SUMS
