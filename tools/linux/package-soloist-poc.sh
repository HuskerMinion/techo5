#!/bin/bash
# Package only TECHO5, soloistd and the private Ubuntu runtime; never Spotify binaries or credentials.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
ARCH=${SOLOIST_GOARCH:-arm}
for pair in 'echod-soloist-arm techo5' "techo5-soloistd-$ARCH soloistd" "techo5-soloist-runtime-$ARCH.tar.gz runtime.tar.gz"; do
    read -r source dest <<< "$pair"
    cp "$ROOT/bin/$source" "$STAGE/$dest"
done
cp "$ROOT/tools/linux/techo5-soloist-exec" "$STAGE/exec"
cp "$ROOT/tools/linux/soloist-poc-device.sh" "$STAGE/device.sh"
cp "$ROOT/bin/techo5-soloistd-$ARCH.LICENSE" "$STAGE/soloistd.LICENSE"
cp "$ROOT/bin/techo5-soloistd-$ARCH.SOURCE" "$STAGE/soloistd.SOURCE"
(cd "$STAGE" && sha256sum techo5 soloistd runtime.tar.gz exec device.sh soloistd.LICENSE soloistd.SOURCE > SHA256SUMS)
tar --owner=0 --group=0 -czf "$ROOT/bin/techo5-soloist-poc.tar.gz" -C "$STAGE" .
echo "$ROOT/bin/techo5-soloist-poc.tar.gz"
