#!/bin/sh
# Install TECHO5 on the Skylight Calendar 15, as root on TECHO5's kernel with nothing of the eMMC
# mounted. The installer image's /init runs it (tools/linux/skylight/kernel/installer/init) on the
# set install-skylight.py staged on Android's cache partition:
#
#   install.sh SETDIR
#
# SETDIR holds:
#   install.conf    the unit's own geometry, from its own GPT (install-skylight.py, mkgpt.py): TOTAL
#                   sectors, STORE_FIRST/STORE_LAST, DATA_FIRST/DATA_LAST, BACKUP_LBA, the sha256 of
#                   the stock table (STOCK_PRIMARY_SHA: LBA 1..33, STOCK_BACKUP_SHA: the last 33 LBAs),
#                   the store image's size and sha256 uncompressed (STORE_BYTES, STORE_SHA)
#   SHA256SUMS      of everything the host staged
#   gpt-primary.bin, gpt-backup.bin   the new table (mkgpt.py)
#   store.img.gz    techo5-store (mkstore.sh -k none, no unit firmware)
#   boot.img        TECHO5's boot image, for recovery
#   firmware/       the unit's own Wi-Fi and Bluetooth files, from its vendor partition
#   settings/       optional: name, psk, wpa_supplicant.conf for techo5-data
#   data.img.gz, data.img.info   techo5-data as the first boot finds it ("BYTES SHA256"), from the
#                   installer image
#
# Writes, in order, each read back from the flash: the new GPT (userdata becomes techo5-store +
# techo5-data), techo5-store, techo5-data, the unit's firmware into the store and the settings into
# techo5-data, the boot image into recovery, and last "boot-recovery" at the start of misc, so the
# stock U-Boot starts recovery (TECHO5) on every boot. Nothing outside these ranges is written.
#
# Run again on the same set, it writes the same bytes: a table that is already the new one is taken
# as an earlier, interrupted run of this set. So is a primary table that is neither (a write of it cut
# off by a power loss) with the backup table still the stock one, but only when the installer says an
# earlier run of this set never finished (INSTALL_ATTEMPT=2): then nothing else wrote it, and stopping
# with "nothing written" would send the unit back to an Android whose table this set tore. On a first
# run the same table stops it before writing.
#
# Exit status: 0 done; 2 stopped before writing anything; 1 stopped after writing began.
#
# INSTALL_ROOT, unset on the unit, puts /sys, /dev, /proc, /mnt and /tmp under another folder: the
# offline tests (tools/test_install_skylight.py) run this file on a simulated eMMC there.
set -u
R=${INSTALL_ROOT:-}
D=${1:?usage: install.sh SETDIR}
WROTE=2
say() {
	echo "install: $*"
	echo "install: $*" > /dev/kmsg 2>/dev/null
	[ -n "${INSTALL_TTY:-}" ] && echo "$*" > "$INSTALL_TTY" 2>/dev/null
	return 0
}
stop() { say "STOPPED: $*"; exit $WROTE; }
cd "$D" || stop "no set at $D"

# --- 1. Checks. Nothing is written until all of them pass.
for f in install.conf SHA256SUMS gpt-primary.bin gpt-backup.bin store.img.gz boot.img data.img.gz data.img.info \
	firmware/fw_bcm43456c5_ag.bin firmware/nvram_ap6256.txt firmware/BCM4345C5.hcd; do
	[ -s "$f" ] || stop "$f is missing from the set"
done
sha256sum -c SHA256SUMS > $R/tmp/sums.log 2>&1 || { cat $R/tmp/sums.log; stop "the set does not match its SHA256SUMS"; }
# Only KEY=number or KEY=hex lines: it is sourced next.
grep -qvE '^(#.*|[A-Z_]+=[0-9a-f]+)$' install.conf && stop "install.conf holds lines it should not"
. ./install.conf
for k in TOTAL STORE_FIRST STORE_LAST DATA_FIRST DATA_LAST BACKUP_LBA STOCK_PRIMARY_SHA STOCK_BACKUP_SHA \
	STORE_BYTES STORE_SHA; do
	eval "v=\${$k:-}"
	[ -n "$v" ] || stop "install.conf has no $k"
done
read -r DATA_BYTES DATA_SHA < data.img.info
[ -n "${DATA_SHA:-}" ] || stop "data.img.info is not BYTES SHA256"

# The eMMC, found by its partitions' names (the numbers are the stock table's, but are not trusted).
part() {
	for u in $R/sys/class/block/mmcblk*p*/uevent; do
		grep -qx "PARTNAME=$1" "$u" 2>/dev/null && { basename "$(dirname "$u")"; return 0; }
	done
	return 1
}
# mmcblk0p8 is a partition of mmcblk0: the kernel names an MMC partition after its disk.
disk_of() { echo "${1%p*}"; }
MISC=$(part misc) || stop "no partition named misc"
RECOVERY=$(part recovery) || stop "no partition named recovery"
DISK=$(disk_of "$MISC")
[ "$(disk_of "$RECOVERY")" = "$DISK" ] || stop "misc and recovery are on different disks"
[ "$(cat "$R/sys/class/block/$DISK/size")" = "$TOTAL" ] || stop "$DISK is not $TOTAL sectors"
[ $((STORE_FIRST % 2048)) = 0 ] && [ $((DATA_FIRST % 2048)) = 0 ] || stop "the new partitions are not 1 MiB aligned"
[ $((STORE_LAST + 1)) = "$DATA_FIRST" ] && [ "$DATA_LAST" -lt "$BACKUP_LBA" ] || stop "install.conf's geometry does not add up"
[ $(( (STORE_LAST - STORE_FIRST + 1) * 512 )) -ge "$STORE_BYTES" ] || stop "store.img is larger than techo5-store"
[ $(( (DATA_LAST - DATA_FIRST + 1) * 512 )) -ge "$DATA_BYTES" ] || stop "data.img is larger than techo5-data"
n=$(stat -c %s boot.img)
[ $(( $(cat "$R/sys/class/block/$RECOVERY/size") * 512 )) -ge "$n" ] || stop "boot.img is larger than recovery"
[ "$(head -c 8 boot.img)" = "ANDROID!" ] || stop "boot.img is not a boot image"

DEV=$R/dev/$DISK
sha_at() { # sha_at LBA BYTES: the sha256 of BYTES at LBA, read from the flash rather than the page cache
	sync; echo 3 > $R/proc/sys/vm/drop_caches 2>/dev/null
	if [ $(($1 % 2048)) = 0 ]; then
		dd if=$DEV bs=1048576 skip=$(($1 / 2048)) count=$(( ($2 + 1048575) / 1048576 )) 2>/dev/null
	else
		dd if=$DEV bs=512 skip=$1 count=$(( ($2 + 511) / 512 )) 2>/dev/null
	fi | head -c "$2" | sha256sum | cut -d' ' -f1
}
sha_of() { sha256sum "$1" | cut -d' ' -f1; }
NEW_PRIMARY_SHA=$(sha_of gpt-primary.bin)
NEW_BACKUP_SHA=$(sha_of gpt-backup.bin)
p=$(sha_at 1 16896); b=$(sha_at "$BACKUP_LBA" 16896)
if [ "$p" = "$STOCK_PRIMARY_SHA" ] && [ "$b" = "$STOCK_BACKUP_SHA" ]; then
	say "the partition table is the stock one install-skylight.py read"
elif [ "$p" = "$NEW_PRIMARY_SHA" ] || [ "$b" = "$NEW_BACKUP_SHA" ]; then
	say "the partition table is already (partly) the new one: an earlier run of this set; going on"
elif [ "$b" = "$STOCK_BACKUP_SHA" ] && [ "${INSTALL_ATTEMPT:-1}" = 2 ]; then
	say "the primary partition table is torn and the backup one is stock: an earlier run of this set was cut off writing it; going on"
else
	stop "the partition table is neither the stock one install-skylight.py read nor the new one"
fi
say "checks passed: $DISK, $TOTAL sectors; techo5-store at $STORE_FIRST, techo5-data at $DATA_FIRST"

# --- 2. Writes.
WROTE=1
check() { # check LBA BYTES SHA WHAT
	[ "$(sha_at "$1" "$2")" = "$3" ] || stop "$4 did not read back as written"
	say "$4 written and read back"
}
dd if=gpt-primary.bin of=$DEV bs=512 seek=1 conv=fsync 2>/dev/null || stop "writing the GPT failed"
check 1 16896 "$NEW_PRIMARY_SHA" "the GPT (primary)"
dd if=gpt-backup.bin of=$DEV bs=512 seek="$BACKUP_LBA" conv=fsync 2>/dev/null || stop "writing the backup GPT failed"
check "$BACKUP_LBA" 16896 "$NEW_BACKUP_SHA" "the GPT (backup)"
say "writing techo5-store ($((STORE_BYTES >> 20)) MiB)"
gunzip -c store.img.gz | dd of=$DEV bs=1048576 seek=$((STORE_FIRST / 2048)) conv=fsync 2>/dev/null || stop "writing techo5-store failed"
check "$STORE_FIRST" "$STORE_BYTES" "$STORE_SHA" "techo5-store"
gunzip -c data.img.gz | dd of=$DEV bs=1048576 seek=$((DATA_FIRST / 2048)) conv=fsync 2>/dev/null || stop "writing techo5-data failed"
check "$DATA_FIRST" "$DATA_BYTES" "$DATA_SHA" "techo5-data"

# The kernel reads the new table: it must name techo5-store and techo5-data where they were written.
sync
blockdev --rereadpt $DEV || stop "the kernel would not read the new partition table"
mdev -s 2>/dev/null
STORE=$(part techo5-store) || stop "the kernel does not see techo5-store"
DATA=$(part techo5-data) || stop "the kernel does not see techo5-data"
[ "$(cat $R/sys/class/block/$STORE/start)" = "$STORE_FIRST" ] || stop "techo5-store is not where it was written"
[ "$(cat $R/sys/class/block/$DATA/start)" = "$DATA_FIRST" ] || stop "techo5-data is not where it was written"
say "the kernel sees techo5-store ($STORE) and techo5-data ($DATA)"

# The unit's own firmware into the store, under the names the drivers ask for (as mkstore.sh -F).
mkdir -p $R/mnt/store $R/mnt/data
mount -t ext4 $R/dev/$STORE $R/mnt/store || stop "techo5-store would not mount"
mkdir -p $R/mnt/store/lib/firmware/brcm
for pair in fw_bcm43456c5_ag.bin:brcmfmac43456-sdio.bin nvram_ap6256.txt:brcmfmac43456-sdio.txt \
	BCM4345C5.hcd:BCM4345C5.hcd; do
	src=firmware/${pair%%:*}; dst=$R/mnt/store/lib/firmware/brcm/${pair#*:}
	cp "$src" "$dst" && chmod 644 "$dst" && [ "$(sha_of "$dst")" = "$(sha_of "$src")" ] ||
		{ umount $R/mnt/store; stop "copying $src into the store failed"; }
done
sync
umount $R/mnt/store || stop "techo5-store would not unmount"
say "the unit's Wi-Fi and Bluetooth firmware are in the store"

# The settings, when given, into techo5-data (its techo5 directory is /data on TECHO5).
if [ -d settings ]; then
	mount -t ext4 $R/dev/$DATA $R/mnt/data || stop "techo5-data would not mount"
	S=$R/mnt/data/techo5/misc/techo5
	mkdir -p $S $R/mnt/data/techo5/techo5-linux && chmod 700 $S
	ok=1
	[ -f settings/name ] && { cp settings/name $S/name && chmod 644 $S/name || ok=; }
	[ -f settings/psk ] && { cp settings/psk $S/psk && chmod 600 $S/psk || ok=; }
	[ -f settings/wpa_supplicant.conf ] && { cp settings/wpa_supplicant.conf $R/mnt/data/techo5/techo5-linux/ &&
		chmod 600 $R/mnt/data/techo5/techo5-linux/wpa_supplicant.conf || ok=; }
	sync
	umount $R/mnt/data || ok=
	[ -n "$ok" ] || stop "writing the settings into techo5-data failed"
	say "settings written into techo5-data"
fi

dd if=boot.img of=$R/dev/$RECOVERY bs=1048576 conv=fsync 2>/dev/null || stop "writing recovery failed"
sync; echo 3 > $R/proc/sys/vm/drop_caches 2>/dev/null
[ "$(head -c "$n" $R/dev/$RECOVERY | sha256sum | cut -d' ' -f1)" = "$(sha_of boot.img)" ] || stop "recovery did not read back as written"
say "recovery holds TECHO5's boot image"

# Last: the boot-recovery command at the start of misc (2048 bytes; the factory message at 16 KiB and
# the rest of misc are left alone).
{ printf 'boot-recovery'; head -c $((2048 - 13)) /dev/zero; } > $R/tmp/bcb
dd if=$R/tmp/bcb of=$R/dev/$MISC bs=2048 count=1 conv=notrunc,fsync 2>/dev/null || stop "writing misc failed"
sync; echo 3 > $R/proc/sys/vm/drop_caches 2>/dev/null
[ "$(dd if=$R/dev/$MISC bs=13 count=1 2>/dev/null)" = "boot-recovery" ] || stop "misc did not read back as written"
sync
say "done: misc says boot-recovery; every start is TECHO5 now"
exit 0
