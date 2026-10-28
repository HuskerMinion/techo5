#!/bin/bash
# Build on Linux inside Docker, including on Apple silicon Macs. Does not access a device.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../../.." && pwd)
IMAGE=techo5-athena-kbuild:ubuntu24.04
VOLUME=techo5-athena-kbuild
OUT=${OUT:-$ROOT/build/athena-kernel}

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
docker build --platform linux/arm64 -t "$IMAGE" -f "$HERE/Dockerfile.build" "$HERE"
docker volume create "$VOLUME" >/dev/null
CONTAINER=$(docker create --rm --platform linux/arm64 --name techo5-athena-kbuild \
    -v "$ROOT:/repo:ro" \
    --mount "type=volume,src=$VOLUME,dst=/techo5-athena-kbuild" \
    -v "$OUT:/out" \
    -e WORK=/techo5-athena-kbuild -e OUT=/out -e JOBS="${JOBS:-4}" \
    "$IMAGE" bash /repo/tools/linux/athena/kernel/build.sh)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker start --attach "$CONTAINER" &
wait "$!"
