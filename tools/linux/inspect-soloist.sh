#!/bin/bash
# Inspect a locally downloaded Soloist executable without running or installing it.
# Obtain archives from Spotify's official Downloads and Updates page, not a mirror.
set -euo pipefail

if [ "$#" -ne 1 ] || [ "$1" = --help ]; then
    echo "usage: bash tools/linux/inspect-soloist.sh /path/to/extracted/soloist"
    echo "Prints the file checksum, ELF architecture, loader, libraries and symbol versions."
    echo "This is static inspection, not a playback or compatibility certification."
    [ "${1:-}" = --help ] && exit 0
    exit 2
fi

for tool in sha256sum readelf sed; do
    command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 2; }
done

binary=$1
[ -f "$binary" ] || { echo "not a regular file: $binary" >&2; exit 2; }
# Check the format before printing a partial report. Never execute the input.
readelf --file-header -- "$binary" >/dev/null

echo "SHA-256 (records this download; not an upstream signature):"
sha256sum -- "$binary"
echo "ELF architecture:"
readelf --file-header -- "$binary" | sed -n '/Class:/p; /Data:/p; /Machine:/p; /Flags:/p'
echo "Required program interpreter:"
readelf --wide --program-headers -- "$binary" | sed -n '/Requesting program interpreter/p'
echo "Direct shared-library dependencies (dlopen dependencies are not listed here):"
readelf --wide --dynamic -- "$binary" | sed -n '/(NEEDED)/p'
echo "Required symbol versions:"
readelf --wide --version-info -- "$binary" | sed -n '/Version needs section/,$p'
