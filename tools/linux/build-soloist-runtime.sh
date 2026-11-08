#!/bin/bash
# Private ARM32 glibc/PulseAudio sysroot. Run on Ubuntu 24.04/WSL; no root required.
# APT verifies Ubuntu's signed indices and package hashes. Nothing is installed on the host.
# Keep the resulting debs/manifest for exact reproduction. No Spotify binary is redistributed.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
ARCH=${SOLOIST_ARCH:-armhf}
case "$ARCH" in
armhf) TRIPLE=arm-linux-gnueabihf; LOADER=ld-linux-armhf.so.3; SUFFIX=arm ;;
arm64) TRIPLE=aarch64-linux-gnu; LOADER=ld-linux-aarch64.so.1; SUFFIX=arm64 ;;
*) echo 'SOLOIST_ARCH must be armhf or arm64' >&2; exit 2 ;;
esac
WORK=${SOLOIST_BUILD_DIR:-$HOME/techo5-soloist-build-$SUFFIX}
OUT=${1:-$ROOT/bin/techo5-soloist-runtime-$SUFFIX.tar.gz}
mkdir -p "$WORK/apt/lists/partial" "$WORK/apt/archives/partial" "$WORK/runtime"
WORK=$(cd "$WORK" && pwd)
if [ -f "$WORK/architecture" ] && [ "$(cat "$WORK/architecture")" != "$ARCH" ]; then
    echo 'Refusing to mix architectures in SOLOIST_BUILD_DIR; use a separate build directory.' >&2
    exit 2
fi
printf '%s\n' "$ARCH" > "$WORK/architecture"
touch "$WORK/apt/status"
KEY=/usr/share/keyrings/ubuntu-archive-keyring.gpg
test -r "$KEY"
cat > "$WORK/apt/sources.list" <<EOF
deb [arch=$ARCH signed-by=$KEY] https://ports.ubuntu.com/ubuntu-ports noble main universe
deb [arch=$ARCH signed-by=$KEY] https://ports.ubuntu.com/ubuntu-ports noble-updates main universe
deb [arch=$ARCH signed-by=$KEY] https://ports.ubuntu.com/ubuntu-ports noble-security main universe
EOF
APT=(-o "Dir::Etc::sourcelist=$WORK/apt/sources.list" -o Dir::Etc::sourceparts=-
     -o "Dir::State=$WORK/apt" -o "Dir::State::status=$WORK/apt/status"
     -o "Dir::Cache=$WORK/apt" -o APT::Architecture="$ARCH" -o APT::Architectures="$ARCH"
     -o Debug::NoLocking=true -o APT::Sandbox::User="$(id -un)")
apt-get "${APT[@]}" update
packages=(pulseaudio pulseaudio-utils libatomic1 ca-certificates patchelf strace)
if [ -n "${SOLOIST_PACKAGE_LOCK:-}" ]; then
    mapfile -t packages < "$SOLOIST_PACKAGE_LOCK"
fi
apt-get "${APT[@]}" --download-only --no-install-recommends -y install "${packages[@]}"
for deb in "$WORK"/apt/archives/*.deb; do
    dpkg-deb -x "$deb" "$WORK/runtime"
done
for deb in "$WORK"/apt/archives/*.deb; do
    printf '%s=%s\n' "$(dpkg-deb -f "$deb" Package)" "$(dpkg-deb -f "$deb" Version)"
done | sort > "$WORK/runtime/packages.lock"
(cd "$WORK/apt/archives" && sha256sum ./*.deb) > "$WORK/runtime/packages.sha256"
cp "$WORK/apt/sources.list" "$WORK/runtime/sources.list"
# Extracted symlinks remain inside the private runtime; use the loader explicitly, not /lib.
test -f "$WORK/runtime/usr/lib/$TRIPLE/$LOADER"
mkdir -p "$(dirname "$OUT")"
tar --owner=0 --group=0 -czf "$OUT" -C "$WORK/runtime" .
echo "Private runtime: $OUT"
echo "Exact package lock: $WORK/runtime/packages.lock"
