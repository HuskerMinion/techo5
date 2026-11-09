#!/bin/bash
# Golden UI regression for the display previews.
#
# It renders the maintained preview catalog (the Test*Previews in
# internal/feature/display, the same ones SHOW_PREVIEW/SPOT_PREVIEW drive) and
# compares a curated subset (screens.txt) against the committed images in
# images/. The render is deterministic, so an unexpected pixel change means a
# screen's look changed; the gate fails and names the screens.
#
# Usage, from the repo root, with the Go toolchain on PATH:
#   bash tools/golden/run.sh            # check; non-zero if any screen changed
#   BLESS_GOLDEN=1 bash tools/golden/run.sh   # accept the current look as the baseline
#
# When a change to a screen is intended, eyeball the new render, then bless it in
# the same commit as the code change, so the diff shows what moved.
set -u

here="tools/golden"
list="$here/screens.txt"
gold="$here/images"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/show" "$tmp/spot"

echo "golden: rendering the preview catalog..."
if ! ( cd echod && SHOW_PREVIEW="$tmp/show" go test ./internal/feature/display >/dev/null 2>&1 ); then
	echo "golden: the Show render failed (run the display tests to see why)"; exit 2
fi
if ! ( cd echod && SPOT_PREVIEW="$tmp/spot" go test -tags spot ./internal/feature/display >/dev/null 2>&1 ); then
	echo "golden: the Spot render failed (run the display tests with -tags spot to see why)"; exit 2
fi

if [ "${BLESS_GOLDEN:-}" = "1" ]; then
	n=0
	while read -r f; do
		f="${f%$'\r'}" # tolerate a CRLF checkout of screens.txt
		[ -z "$f" ] && continue
		if [ ! -f "$tmp/$f" ]; then echo "golden: cannot bless, not rendered: $f"; exit 2; fi
		mkdir -p "$gold/$(dirname "$f")"
		cp "$tmp/$f" "$gold/$f"
		n=$((n + 1))
	done < "$list"
	echo "golden: blessed $n screens into $gold"
	exit 0
fi

changed=0
missing=0
checked=0
while read -r f; do
	f="${f%$'\r'}" # tolerate a CRLF checkout of screens.txt
	[ -z "$f" ] && continue
	checked=$((checked + 1))
	if [ ! -f "$tmp/$f" ]; then echo "golden: not rendered (scene gone or renamed?): $f"; missing=$((missing + 1)); continue; fi
	if [ ! -f "$gold/$f" ]; then echo "golden: no baseline yet (bless it): $f"; missing=$((missing + 1)); continue; fi
	if ! cmp -s "$tmp/$f" "$gold/$f"; then echo "golden: CHANGED: $f"; changed=$((changed + 1)); fi
done < "$list"

echo "golden: $checked screens checked, $changed changed, $missing missing"
if [ "$changed" -ne 0 ] || [ "$missing" -ne 0 ]; then
	echo "golden: if a change is intended, review it then run BLESS_GOLDEN=1 bash tools/golden/run.sh"
	exit 1
fi
echo "golden: all screens match"
