#!/bin/sh
# SPDX-License-Identifier: MIT
# Build the athena system image (the slot store, with the rescue environment at its root) from a root
# filesystem slot, with upstream's tools/linux/athena/mkstore.sh as it is. mkstore.sh wants Linux with
# qemu-arm binfmt, Alpine's static apk and a user namespace; this runs it in a privileged Linux
# container, which has all three on a Mac too (Docker Desktop registers qemu-arm, and the x86-64
# apk.static from tools/fetch-inputs.py runs under its x86-64 emulation).
#
#   build-store.sh <rootfs.tar.gz> <system.img>
#
# The image carries no SSH key (mkstore.sh -k none), as a released image must: its rescue is then
# reached over USB serial only. Needs network, for apk.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(dirname "$(dirname "$D")")
INPUTS=${TECHO5_INPUTS:-$ROOT/inputs}
ROOTFS=${1:?usage: build-store.sh <rootfs.tar.gz> <system.img>}
OUT=${2:?usage: build-store.sh <rootfs.tar.gz> <system.img>}
ROOTFS=$(cd "$(dirname "$ROOTFS")" && pwd)/$(basename "$ROOTFS")
mkdir -p "$(dirname "$OUT")"
OUTDIR=$(cd "$(dirname "$OUT")" && pwd)

docker build -q -t techo5-athena-store - >/dev/null <<'EOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends bash e2fsprogs uidmap ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && echo root:100000:65536 > /etc/subuid && echo root:100000:65536 > /etc/subgid
EOF
docker run --rm --privileged -v "$ROOT:/repo:ro" -v "$INPUTS:/inputs:ro" -v "$ROOTFS:/rootfs.tar.gz:ro" \
  -v "$OUTDIR:/out" -e APK=/inputs/apk.static techo5-athena-store bash -ec '
    [ -e /proc/sys/fs/binfmt_misc/qemu-arm ] || mount -t binfmt_misc none /proc/sys/fs/binfmt_misc
    bash /repo/tools/linux/athena/mkstore.sh -r /rootfs.tar.gz -k none -i /inputs -w /tmp/store \
      -o "/out/$1"' sh "$(basename "$OUT")"
