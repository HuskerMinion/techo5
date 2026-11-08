#!/bin/bash
# Build pinned upstream soloistd plus the small private-loader adaptation.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
REV=ccf073a24d682249de8e74b8f81ab5de169457a6
WORK=${SOLOISTD_BUILD_DIR:-$HOME/techo5-soloistd-build}
ARCH=${SOLOIST_GOARCH:-arm}
OUT=${1:-$ROOT/bin/techo5-soloistd-$ARCH}
GO=${GO:-go}
if [ ! -d "$WORK/.git" ]; then git clone https://github.com/MopigamesYT/soloistd.git "$WORK"; fi
git -C "$WORK" checkout --detach "$REV"
# Work only in this dedicated build clone; regenerate the patch from pristine upstream files.
git -C "$WORK" restore commands.go download.go supervisor.go
python3 "$ROOT/tools/linux/patch-soloistd.py" "$WORK"
cp "$ROOT/tools/linux/soloistd-exec.go.txt" "$WORK/techo5_exec.go"
(cd "$WORK" && "$GO" test -mod=vendor ./...)
mkdir -p "$(dirname "$OUT")"
(cd "$WORK" && GOOS=linux GOARCH="$ARCH" GOARM=7 CGO_ENABLED=0 "$GO" build -mod=vendor -trimpath -ldflags '-s -w' -o "$OUT" .)
cp "$WORK/LICENSE" "$OUT.LICENSE"
printf '%s\n' "$REV + tools/linux/patch-soloistd.py + soloistd-exec.go.txt" > "$OUT.SOURCE"
