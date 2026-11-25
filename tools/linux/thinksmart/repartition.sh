#!/bin/sh
# repartition.sh — give a Lenovo ThinkSmart View a techo5-data partition. Run on the unit itself, as
# root, from any Linux that has sfdisk (Debian, or the postmarketOS bootstrap), before TECHO5's image
# is written to system over EDL.
#
#   repartition.sh [--write]        without --write it only shows the table it would write
#
# It starts from the unit's own GPT, as rickx's apply-ubuntu-gpt.sh left it: system from its own
# start to the sector before persist. system becomes 4694016 sectors, the size of TECHO5's image
# (tools/linux/thinksmart/mkimage.sh: lk2nd's boot partition and the 2 GiB store), and the rest of
# what it had becomes techo5-data, in the first free entry. Nothing else in the table changes: every
# other partition keeps its start, size, type, UUID and name.
#
# What is in system now is lost with the next boot: whatever runs from there has a file system that
# reaches past the new end of system. So this is the step before the image is written, not one to
# stop after. The table as it was is kept in $SAVE (default /root/gpt-before-techo5.sfdisk); writing
# it back with sfdisk, from a system that still runs, undoes this.
set -eu
DISK=${DISK:-/dev/mmcblk0}
SAVE=${SAVE:-/root/gpt-before-techo5.sfdisk}
SYSTEM_SECTORS=4694016
WRITE=
[ "${1:-}" = --write ] && WRITE=1

old=$(sfdisk -d "$DISK")
line() { echo "$old" | grep "name=\"$1\"" || true; }
field() { echo "$1" | sed -n "s/.*[ ,]$2= *\([0-9]*\).*/\1/p"; }
sys=$(line system)
[ -n "$sys" ] || { echo "repartition: no system partition on $DISK" >&2; exit 1; }
[ -z "$(line techo5-data)" ] || { echo "repartition: $DISK already has a techo5-data partition" >&2; exit 1; }
start=$(field "$sys" start); size=$(field "$sys" size)
[ "$size" -gt $((SYSTEM_SECTORS + 2097152)) ] ||
	{ echo "repartition: system is $size sectors: no room for the image and 1 GiB of data (apply-ubuntu-gpt.sh first?)" >&2; exit 1; }
sysdev=$(echo "$sys" | cut -d' ' -f1)
n=1; while echo "$old" | grep -q "^${DISK}p$n "; do n=$((n+1)); done
data_start=$((start + SYSTEM_SECTORS))
data_size=$((size - SYSTEM_SECTORS))
new=$(echo "$old" | sed "s|^\($sysdev : start= *$start, size= *\)$size,|\1$SYSTEM_SECTORS,|")
new="$new
${DISK}p$n : start=$data_start, size=$data_size, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=\"techo5-data\""
echo "repartition: system  $start + $size  ->  $start + $SYSTEM_SECTORS"
echo "repartition: techo5-data (new, entry $n)  $data_start + $data_size"
[ "$(echo "$new" | grep -c '^/dev')" -eq $(( $(echo "$old" | grep -c '^/dev') + 1 )) ]
echo "$new" | sfdisk --force --no-reread --no-act "$DISK" > /tmp/repartition.check 2>&1 ||
	{ echo "repartition: sfdisk does not take the new table:" >&2; cat /tmp/repartition.check >&2; exit 1; }
if [ -z "$WRITE" ]; then
	echo "repartition: nothing written (--write to do it). The table it would write:"
	echo "$new" | grep -E 'name="(system|techo5-data|persist)"'
	exit 0
fi
echo "$old" > "$SAVE"
echo "$new" | sfdisk --force --no-reread "$DISK" > /tmp/repartition.log 2>&1 || { cat /tmp/repartition.log >&2; exit 1; }
sync
echo "repartition: written; the table as it was is in $SAVE. Reboot into EDL and write the image to system."
