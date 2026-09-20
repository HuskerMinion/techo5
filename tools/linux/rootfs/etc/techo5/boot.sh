#!/bin/sh
# TECHO5 root filesystem: sysinit (see /etc/inittab).
#
# The initramfs hands over with /proc, /sys, /dev (tmpfs, populated by mdev),
# /run, /tmp, /data (userdata) and /store (the slot store, read-only) already
# mounted and the name of the booted slot in /run/techo5/slot. Everything here
# is best effort and logged: nothing is allowed to keep the daemon from
# starting.

PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
LOGDIR=/data/techo5-linux
log() { echo "techo5-boot: $*" > /dev/kmsg; echo "$(cut -d' ' -f1 /proc/uptime) $*" >> /run/boot.log; }

# What differs between devices; the Echo Show 5's values unless the image carries its own.
DATA_DEV=/dev/mmcblk0p16
STORE_DEV=/dev/mmcblk0p12
WIFI_MODULE=/vendor/lib/modules/mt76x8_wlan.ko
BT_MODULE=/vendor/lib/modules/mt76x8_bt.ko
[ -r /etc/techo5/device.conf ] && . /etc/techo5/device.conf
export T5_PRODUCT
. /lib/techo5-lib.sh

# --- Mounts the initramfs normally provides, for a boot that did not come through it.
mountpoint -q /proc || mount -t proc proc /proc
mountpoint -q /sys || mount -t sysfs sysfs /sys
if ! mountpoint -q /dev; then
	mount -t tmpfs -o mode=0755 tmpfs /dev
	mkdir -p /dev/pts
	mount -t devpts devpts /dev/pts
fi
mountpoint -q /run || mount -t tmpfs tmpfs /run
mountpoint -q /tmp || mount -t tmpfs tmpfs /tmp
mountpoint -q /data || mount -t ext4 -o noatime $DATA_DEV /data
mountpoint -q /store || mount -t ext4 -o ro,noatime $STORE_DEV /store
# The root is read-only; what needs writing lives on tmpfs or userdata.
mount -t tmpfs tmpfs /var/log
mount -t tmpfs tmpfs /var/tmp
mkdir -p /run/lock /run/techo5 $LOGDIR /data/misc/techo5/models
# The clock's zone lives on userdata (echod sets it from Home Assistant); a unit without one starts
# on the image's default.
if [ ! -e /data/misc/techo5/timezone ] || [ ! -e /data/misc/techo5/localtime ]; then
	zone=$(cat /etc/techo5/default-timezone 2>/dev/null || echo UTC)
	[ -e "/usr/share/zoneinfo/$zone" ] || zone=UTC
	ln -sfn "/usr/share/zoneinfo/$zone" /data/misc/techo5/localtime
	echo "$zone" > /data/misc/techo5/timezone
fi
mkdir -p -m 700 /data/misc/techo5/ssh
# The kernel keeps its last words in RAM across a restart (pstore). Mounting it makes them readable,
# and a copy on userdata outlives the next boot; a power cut clears the RAM, so this only catches a
# device that restarted itself.
if mount -t pstore pstore /sys/fs/pstore 2>/dev/null || [ -d /sys/fs/pstore ]; then
	for f in /sys/fs/pstore/*; do
		[ -e "$f" ] || continue
		mkdir -p /data/techo5-linux/crash
		cp "$f" "/data/techo5-linux/crash/$(date +%Y%m%d-%H%M%S)-${f##*/}" 2>/dev/null &&
			log "kernel crash record kept: ${f##*/}" && rm -f "$f"
	done
fi
# The wake word models the image carries, any this unit does not have yet: a fresh unit gets them
# all, and one set up before a model joined the image gets it at its next update, so every unit
# offers the same words. Nothing on the unit is replaced or removed (a purged one comes back here).
if [ -d /usr/share/techo5/models ]; then
	mkdir -p /data/misc/techo5/models
	n=0
	for f in /usr/share/techo5/models/*; do
		[ -e "/data/misc/techo5/models/${f##*/}" ] && continue
		cp "$f" /data/misc/techo5/models/ && n=$((n + 1))
	done
	[ $n -gt 0 ] && log "wake word models: $n files added from the image"
fi
: > /run/boot.log

echo /sbin/mdev > /proc/sys/kernel/hotplug
mdev -s
mkdir -p /dev/graphics
[ -e /dev/fb0 ] && ln -sf /dev/fb0 /dev/graphics/fb0
hostname -F /etc/hostname
log "slot $(cat /run/techo5/slot 2>/dev/null || echo '?'): $(cat /etc/techo5-release 2>/dev/null)"

# --- Vendor tree: the Wi-Fi and Bluetooth drivers, firmware and audio tuning from the unit's own
# LineageOS. Images don't carry it (it isn't ours to publish): it is kept once in the store, taken
# from wherever this unit already has it (the installer puts it there; a unit coming from an image
# that carried it adopts it from a slot), and appears at /vendor in every slot.
VMOD=${WIFI_MODULE#/vendor/}
if [ ! -e "/store/vendor/$VMOD" ]; then
	src=
	for d in /vendor /store/slots/a/vendor /store/slots/b/vendor; do
		[ -e "$d/$VMOD" ] && { src=$d; break; }
	done
	if [ -z "$src" ]; then
		log "vendor tree: none on this unit; no Wi-Fi or Bluetooth drivers"
	elif mount -o remount,rw /store; then
		rm -rf /store/vendor.new
		if cp -a "$src" /store/vendor.new && [ -e "/store/vendor.new/$VMOD" ]; then
			rm -rf /store/vendor && mv /store/vendor.new /store/vendor && log "vendor tree: kept in the store, from $src"
		else
			rm -rf /store/vendor.new
			log "vendor tree: copying $src into the store failed"
		fi
		sync
		mount -o remount,ro /store || log "vendor tree: the store stayed writable"
	else
		log "vendor tree: the store could not be made writable"
	fi
fi
if [ ! -e "$WIFI_MODULE" ] && [ -e "/store/vendor/$VMOD" ]; then
	mkdir -p /vendor 2>/dev/null
	mount --bind /store/vendor /vendor && log "vendor tree: /vendor is the store's"
fi

# --- Screen: the daemon paints it (feature/display); the bootloader's logo stays until then.
# `fbprobe -hold 1m` is still there for a bare-panel check from the console.

# --- Root shell on USB serial (techo5-console attaches to it).
t5_usb_acm

# --- Network. Credentials: the file on userdata, first written from Android's saved network.
t5_wifi_conf $LOGDIR/wpa_supplicant.conf
export UDHCPC_SCRIPT=/etc/techo5/udhcpc.sh
t5_wifi_up $WIFI_MODULE $LOGDIR/wpa_supplicant.conf

# SSH is echod's (feature/security): off unless switched on, and only with a key on userdata.
if [ -n "$IP" ]; then
	t5_ntp
	ntpd -p "${NTP_SERVER:-pool.ntp.org}" > /dev/null 2>&1
fi

# --- Bluetooth (only on a kernel that has it; see t5_bt_up).
t5_bt_up "$BT_MODULE" /var/log

# --- Network keeper: bring Wi-Fi back if it is gone, and reboot after 15 minutes
# without an address — an unattended unit must never sit unreachable.
(
	down=0
	while true; do
		sleep 60
		# Someone is choosing a network on the screen: not a fault, no reboot.
		[ -e /run/techo5/wifi-setup ] && { down=0; continue; }
		if [ -n "$(t5_ip)" ]; then
			down=0
			# An address without a default route is the aftermath of the link bouncing:
			# udhcpc does not put the route back. Renewing the lease does.
			if ! ip route show default 2>/dev/null | grep -q default; then
				log "network: address but no default route; renewing the lease"
				killall udhcpc 2>/dev/null
				udhcpc -i wlan0 -b -R -t 10 -p /run/udhcpc.pid -s "${UDHCPC_SCRIPT:-/usr/share/udhcpc/default.script}" > /tmp/udhcpc.log 2>&1
			fi
			t5_wifi_prefer5
			continue
		fi
		down=$((down+1))
		log "network: no address for $down min"
		if [ $down -ge 15 ]; then
			log "network: rebooting"
			sync
			reboot
		fi
		killall udhcpc wpa_supplicant 2>/dev/null
		t5_wifi_up $WIFI_MODULE $LOGDIR/wpa_supplicant.conf
	done
) &

# --- Slot trial: this slot is committed once the daemon has been running for
# five minutes without a restart — the same evidence the daemon itself takes
# before it commits an update of its own. Until then every boot costs a try,
# and a boot where that never happens within TRIAL_TIMEOUT seconds reboots on
# its own: a slot whose daemon keeps dying must not sit there reachable but
# useless, it must use up its tries and let the initramfs fall back.
TRIAL_SETTLE=${TRIAL_SETTLE:-300}
TRIAL_TIMEOUT=${TRIAL_TIMEOUT:-900}
[ -r /etc/techo5/trial.conf ] && . /etc/techo5/trial.conf
(
	s=$(cat /run/techo5/slot 2>/dev/null)
	[ -n "$s" ] || exit 0
	case "$(cat /store/slots/$s.state 2>/dev/null)" in trial*) ;; *) exit 0;; esac
	while true; do
		sleep 15
		now=$(cut -d. -f1 /proc/uptime)
		pid=$(pidof techo5 | cut -d' ' -f1)
		if [ -n "$pid" ]; then
			started=$(awk '{print int($22/100)}' /proc/$pid/stat 2>/dev/null)
			if [ -n "$started" ] && [ $((now-started)) -ge $TRIAL_SETTLE ]; then
				log "slot $s: daemon up for $TRIAL_SETTLE s, committing"
				slotctl commit >> /run/boot.log 2>&1
				exit 0
			fi
		fi
		if [ $now -ge $TRIAL_TIMEOUT ]; then
			log "slot $s: daemon did not settle within $TRIAL_TIMEOUT s; rebooting to use up a try"
			cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
			sync
			reboot
		fi
	done
) &

cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
log "boot script done"
