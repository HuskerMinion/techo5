#!/bin/sh
# Build Alpine rescue from public inputs and current boot scripts.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
ROOT=$(dirname "$(dirname "$D")")
docker run --rm -v "$ROOT:/work" -w /work \
  -v techo5-go-mod:/go/pkg/mod -v techo5-go-build:/root/.cache/go-build \
  golang:1.26 sh -ec 'GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/fbprobe-arm ./cmd/fbprobe'
"$D/k6.sh" 'sh /host/initramfs/build-kernel-bundle.sh'
exec python3 "$D/initramfs/build-rescue.py"
