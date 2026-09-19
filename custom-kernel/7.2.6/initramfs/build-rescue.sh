#!/bin/sh
# Build Alpine rescue from public inputs and current boot scripts.
set -eu
D=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
"$D/k6.sh" 'sh /host/initramfs/build-kernel-bundle.sh'
exec python3 "$D/initramfs/build-rescue.py"
