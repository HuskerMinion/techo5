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
# The SDIO host the MT7668 sits on, on the mainline kernel: t5_wifi_up powers the chip off and on
# through it when the driver makes no wlan0 (t5_wifi_reset). Only on a mainline kernel (6.x and later):
# a vendor driver taken out and the host rebound on Amazon's or LineageOS's 4.x could hang the unit.
WIFI_SDIO_HOST=
[ "$(uname -r | cut -d. -f1)" -ge 6 ] 2>/dev/null && WIFI_SDIO_HOST=/sys/bus/platform/drivers/mtk-msdc/11250000.mmc
[ -r /etc/techo5/device.conf ] && . /etc/techo5/device.conf
export T5_PRODUCT
. /lib/techo5-lib.sh

# --- Mounts the initramfs normally provides, for a boot that did not come through it.
mountpoint -q /proc || mount -t proc proc /proc
# On the 2nd gen Show 5's mainline kernel, an RCU stall is a wedged core: panic, and the kernel's
# panic timeout reboots (as do its lockup detectors), rather than a unit that hangs for good. The Show 8
# 2nd gen's kernel does the same from its own command line (tools/linux/athena/kernel/pack.py). Amazon's
# and LineageOS's kernels (4.x) are left as they are.
if grep -q 'amazon,cronos' /proc/device-tree/compatible 2>/dev/null &&
	[ "$(uname -r | cut -d. -f1)" -ge 6 ] 2>/dev/null; then
	echo 1 > /proc/sys/kernel/panic_on_rcu_stall 2>/dev/null
fi
# Each user sees only its own processes. The daemon hands the camera decoder a camera's login, and the
# Home Assistant stream link with its token, on the command line, which /proc/<pid>/cmdline shows to
# anyone otherwise: the receivers (streaming), the decoder (techo5-video), avahi and dbus-daemon
# (messagebus) are the users that are not root, and none of them needs another user's processes.
# dbus-daemon learns who a peer is from the socket (SO_PEERCRED) and its groups from the user database,
# not from /proc. Root sees everything, as before. hidepid=2 rather than "invisible", which these 4.9
# kernels do not know (5.8). Before anything that is not root starts.
mount -o remount,hidepid=2 /proc 2>/dev/null || log "proc: hidepid=2 refused; other users' processes stay visible"
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
# /etc/hosts is a symlink to it: localhost here, and echod adds the .local names it looks up.
[ -e /run/hosts ] || printf '127.0.0.1\tlocalhost localhost.localdomain\n::1\t\tlocalhost localhost.localdomain\n' > /run/hosts
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
# device that restarted itself. The cronos kernel keeps its console there too, so every restart leaves
# the boot before's kernel log. The clock is not set yet (1970), so a copy is named by this boot's id
# as well, or each would replace the last; the newest 20 are kept.
if mount -t pstore pstore /sys/fs/pstore 2>/dev/null || [ -d /sys/fs/pstore ]; then
	# Named by a count of the boots that kept one, which orders them where the time cannot (the clock
	# is not set yet), the newest 20 kept.
	n=
	for f in /sys/fs/pstore/*; do
		[ -e "$f" ] || continue
		mkdir -p /data/techo5-linux/crash
		[ -n "$n" ] || { n=$(($(cat /data/techo5-linux/crash/.count 2>/dev/null || echo 0) + 1)); echo $n > /data/techo5-linux/crash/.count; }
		cp "$f" "/data/techo5-linux/crash/$(printf %05d $n)-$(cut -c1-8 /proc/sys/kernel/random/boot_id 2>/dev/null)-${f##*/}" 2>/dev/null &&
			log "kernel crash record kept: ${f##*/}" && rm -f "$f"
	done
	ls -1 /data/techo5-linux/crash/[0-9][0-9][0-9][0-9][0-9]-* 2>/dev/null | sort -r | tail -n +21 | while read -r f; do rm -f "$f"; done
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
# mdev, with no rules, makes every node root's alone (0660), these too, which every program may use
# (the video decoder's user among them).
chmod 666 /dev/null /dev/zero /dev/full /dev/random /dev/urandom /dev/tty 2>/dev/null
mkdir -p /dev/graphics
[ -e /dev/fb0 ] && ln -sf /dev/fb0 /dev/graphics/fb0
# The 2nd gen Show 5 on mainline (mtk-cpufreq, schedutil): let schedutil change the CPU frequency at
# most every 10 ms rather than every 1 ms. The driver gives no transition latency, so the default is
# 1 ms, and every change costs I2C to the sym827 (cpufreq's Vproc): the switch runs the cores on the
# 1092 MHz intermediate clock, whose OPP is 1.2 V, so even a change between two 1.15 V OPPs raises
# and lowers the voltage. At 1 ms the daemon's idle load made 4 000-4 400 I2C interrupts a second
# and kept sugov:0 at 13-15% of a core; at 10 ms, about 400-600 and 1.5-2.5%. The wake word's
# per-frame time got shorter, and the camera stream kept its rate at 4 and 20 ms. Amazon's kernel has
# neither the driver nor the governor.
if grep -q 'amazon,cronos' /proc/device-tree/compatible 2>/dev/null &&
	[ "$(cat /sys/devices/system/cpu/cpufreq/policy0/scaling_driver 2>/dev/null)" = mtk-cpufreq ] &&
	[ -w /sys/devices/system/cpu/cpufreq/policy0/schedutil/rate_limit_us ]; then
	echo 10000 > /sys/devices/system/cpu/cpufreq/policy0/schedutil/rate_limit_us ||
		log "cpufreq: schedutil rate limit not set"
fi
# The 2nd gen Show 5 on mainline (mediatek-drm): on about one boot in twenty the bootloader times out
# (its boot takes over 10 s rather than 4.2) and leaves the display engine unstarted, which the kernel
# cannot recover: the panel gets no frames and the screen is drawn on the cores. Such a boot says so
# on the command line (the bootloader's fps=6000, against 6206 or 6226 on a good one), so it is
# restarted at once, at most twice in a row (the count in /data is cleared by any good boot) so a
# unit that always boots this way still comes up. Amazon's and LineageOS's kernels are left alone.
if grep -q 'amazon,cronos' /proc/device-tree/compatible 2>/dev/null &&
	[ -d /sys/bus/platform/drivers/mediatek-drm ]; then
	stalls=/data/techo5-linux/display-stall-reboots
	if grep -qw 'fps=6000' /proc/cmdline; then
		n=$(cat "$stalls" 2>/dev/null || echo 0)
		if [ "$n" -lt 2 ]; then
			echo $((n + 1)) > "$stalls"
			sync
			log "display: the bootloader left the display unstarted (fps=6000); restarting ($((n + 1)) of 2)"
			reboot
			sleep 60
		fi
		log "display: the bootloader left the display unstarted again; carrying on"
	else
		rm -f "$stalls"
	fi
fi
hostname -F /etc/hostname
log "slot $(cat /run/techo5/slot 2>/dev/null || echo '?'): $(cat /etc/techo5-release 2>/dev/null)"

# --- Vendor tree: the Wi-Fi and Bluetooth drivers, firmware and audio tuning from the unit's own
# LineageOS. Images don't carry it (it isn't ours to publish): it is kept once in the store, taken
# from wherever this unit already has it (the installer puts it there; a unit coming from an image
# that carried it adopts it from a slot), and appears at /vendor in every slot.
VMOD=${WIFI_MODULE#/vendor/}
# A unit whose own vendor partition holds them (VENDOR_DEV, device.conf: athena) mounts it read-only
# at /vendor as it is, and nothing is copied into the store.
if [ -n "${VENDOR_DEV:-}" ] && ! mountpoint -q /vendor; then
	mount -t ext4 -o ro,noatime "$VENDOR_DEV" /vendor && log "vendor tree: $VENDOR_DEV at /vendor" ||
		log "vendor tree: mounting $VENDOR_DEV failed"
fi
[ -n "${FIRMWARE_PATH:-}" ] && printf %s "$FIRMWARE_PATH" > /sys/module/firmware_class/parameters/path 2>/dev/null
# A unit whose Wi-Fi driver is built into its kernel and that has no vendor partition (the Skylight)
# has no vendor tree to keep at all.
if [ -z "${VENDOR_DEV:-}" ] && [ "$WIFI_MODULE" != builtin ] && [ ! -e "/store/vendor/$VMOD" ]; then
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

# --- Root shell on USB serial (techo5-console attaches to it), and RNDIS where USB_RNDIS is set.
t5_usb_acm
t5_usb_net
# Bring-up SSH (BENCH_SSH_KEYS, device.conf: athena): keys only, from that directory, over USB and Wi-Fi.
if [ -n "${BENCH_SSH_KEYS:-}" ] && [ -s "$BENCH_SSH_KEYS/authorized_keys" ]; then
	t5_dropbear $LOGDIR/dropbear -s -D "$BENCH_SSH_KEYS" -P /run/dropbear-bench.pid
fi

# --- Inbound on Wi-Fi closed except what the device serves, before there is any network
# (usr/local/sbin/techo5-firewall).
/usr/local/sbin/techo5-firewall > $LOGDIR/firewall.log 2>&1

# --- Network. Credentials: the file on userdata, first written from Android's saved network.
t5_wifi_conf $LOGDIR/wpa_supplicant.conf
export UDHCPC_SCRIPT=/etc/techo5/udhcpc.sh
# Modules the Wi-Fi driver needs loaded first (WIFI_PRE_MODULES, device.conf: athena's prealloc).
for m in ${WIFI_PRE_MODULES:-}; do
	n=$(basename "$m" .ko | tr - _)
	grep -q "^$n " /proc/modules || insmod "$m" 2>/tmp/insmod.err || log "wifi: insmod $m failed: $(cat /tmp/insmod.err)"
done
t5_wifi_up $WIFI_MODULE $LOGDIR/wpa_supplicant.conf
# Networks written into the file by hand reach a supplicant that started without any
# (WIFI_CONF_WATCH, device.conf: athena).
[ "${WIFI_CONF_WATCH:-}" = 1 ] && t5_wifi_watch $LOGDIR/wpa_supplicant.conf

# SSH is echod's (feature/security): off unless switched on, and only with a key on userdata.
if [ -n "$IP" ]; then
	t5_ntp
	ntpd -S /etc/techo5/ntp-event.sh $(t5_ntp_peers) > /dev/null 2>&1
fi

# --- Bluetooth (only on a kernel that has it; see t5_bt_up).
t5_bt_up "$BT_MODULE" /var/log

# --- Network keeper: bring Wi-Fi back if it is gone, and reboot after 15 minutes
# without an address — an unattended unit must never sit unreachable.
(
	down=0; lost=0
	while true; do
		sleep 60
		# Someone is choosing a network on the screen: not a fault, no reboot.
		[ -e /run/techo5/wifi-setup ] && { down=0; lost=0; continue; }
		# A lease stays on wlan0 after the link is lost, until it runs out, hours later: an address
		# is not a network. Not associated for three minutes in a row counts as down (#111).
		if t5_wifi_joined; then lost=0; else lost=$((lost+1)); fi
		if [ -n "$(t5_ip)" ] && [ $lost -lt 3 ]; then
			down=0
			# An address without a default route is the aftermath of the link bouncing:
			# udhcpc does not put the route back. Renewing the lease does, and techo5-net
			# puts a fixed address's route back the same way.
			if ! ip route show default 2>/dev/null | grep -q default; then
				log "network: address but no default route; renewing the lease"
				if [ -x /usr/local/sbin/techo5-net ]; then
					/usr/local/sbin/techo5-net renew
				else
					killall udhcpc 2>/dev/null
					udhcpc -i wlan0 -b -R -t 10 -p /run/udhcpc.pid -s "${UDHCPC_SCRIPT:-/usr/share/udhcpc/default.script}" > /tmp/udhcpc.log 2>&1
				fi
			fi
			# The clock is set once there is an address. When Wi-Fi takes longer than the boot
			# script to come up, that happens here: without it the clock stays years behind,
			# and the update check, like anything that checks a certificate, needs it right.
			# A clock still before 2020 is one the running ntpd has never set: its servers
			# are not answering, so it is started again, and asks them afresh (#77).
			if [ "$(date +%Y)" -lt 2020 ] && pidof ntpd >/dev/null; then
				killall ntpd 2>/dev/null
				sleep 1
			fi
			if ! pidof ntpd >/dev/null; then
				t5_ntp
				ntpd -S /etc/techo5/ntp-event.sh $(t5_ntp_peers) > /dev/null 2>&1
			fi
			# A fixed address whose gateway stays silent gives way to DHCP until it answers again
			# (techo5-net check): without it, a device whose router moved to another subnet, or
			# whose supplicant hung, would keep an address and a route and never be noticed.
			[ -x /usr/local/sbin/techo5-net ] && /usr/local/sbin/techo5-net check
			t5_wifi_prefer5
			continue
		fi
		# No network saved: nothing a restart or a reboot could join. The supplicant only has to
		# be there, for the screen's Wi-Fi page to add one. Killing it here each minute used to
		# leave none at all, and the page never opened.
		if ! grep -q '^network={' $LOGDIR/wpa_supplicant.conf 2>/dev/null; then
			down=0
			pidof wpa_supplicant >/dev/null || t5_wifi_up $WIFI_MODULE $LOGDIR/wpa_supplicant.conf
			continue
		fi
		down=$((down+1))
		if [ $lost -ge 3 ]; then
			log "network: not associated for $lost min, down for $down"
		else
			log "network: no address for $down min"
		fi
		if [ $down -ge 15 ]; then
			# A kernel on trial is rebooted by its own loop below, which tells a boot that only
			# lacked a network from one that failed: this reboot would count it as failed.
			if [ -n "${KERNEL_UPDATE:-}" ] && grep -q '^trial' /data/techo5-linux/kernel/state 2>/dev/null; then
				[ $down -eq 15 ] && log "network: down, but a kernel is on trial; its own loop reboots"
			else
				log "network: rebooting"
				sync
				reboot
			fi
		fi
		killall udhcpc wpa_supplicant 2>/dev/null
		# A new supplicant has no channel pin (t5_wifi_prefer5).
		rm -f /run/techo5/prefer5-pin /run/techo5/prefer5-pin.miss
		# Gone before it is started again: t5_wifi_up starts one only when none is running, and
		# one still on its way out counts.
		n=0; while [ $n -lt 10 ] && pidof wpa_supplicant >/dev/null; do sleep 1; n=$((n+1)); done
		t5_wifi_up $WIFI_MODULE $LOGDIR/wpa_supplicant.conf
	done
) &

# --- Hardware that comes up late (HW_WAIT, device.conf: athena on mainline, whose sound card and
# backlight probe ~11 s into the boot): the daemon opens them once, at its start, so it waits here.
for p in ${HW_WAIT:-}; do
	n=0; while [ $n -lt 30 ] && [ ! -e "$p" ]; do sleep 1; n=$((n+1)); done
	if [ -e "$p" ]; then log "hardware: $p after ${n}s"; else log "hardware: no $p after 30s"; fi
done

# --- Slot trial: this slot is committed once the daemon has been running for
# five minutes without a restart — the same evidence the daemon itself takes
# before it commits an update of its own. Until then every boot costs a try,
# and a boot where that never happens within TRIAL_TIMEOUT seconds reboots on
# its own: a slot whose daemon keeps dying must not sit there reachable but
# useless, it must use up its tries and let the initramfs fall back.
# TRIAL_NEEDS_NETWORK=1 (trial.conf; off by default) also wants a network, an
# address with a default route, before committing: a slot that cannot reach
# the network (its Wi-Fi driver does not fit the kernel, say) then uses up its
# tries rather than leaving the unit unreachable for good. It is off because
# a network without a gateway never has a default route, and an update there
# would never be committed.
TRIAL_SETTLE=${TRIAL_SETTLE:-300}
TRIAL_TIMEOUT=${TRIAL_TIMEOUT:-900}
TRIAL_NEEDS_NETWORK=${TRIAL_NEEDS_NETWORK:-0}
[ -r /etc/techo5/trial.conf ] && . /etc/techo5/trial.conf
# trial_network: whether the trial has the network it needs (always, unless TRIAL_NEEDS_NETWORK=1).
trial_network() {
	[ "$TRIAL_NEEDS_NETWORK" = 1 ] || return 0
	ip route show default 2>/dev/null | grep -q default
}
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
			if [ -n "$started" ] && [ $((now-started)) -ge $TRIAL_SETTLE ] && trial_network; then
				log "slot $s: daemon up for $TRIAL_SETTLE s, committing"
				slotctl commit >> /run/boot.log 2>&1
				exit 0
			fi
		fi
		if [ $now -ge $TRIAL_TIMEOUT ]; then
			if trial_network; then
				log "slot $s: daemon did not settle within $TRIAL_TIMEOUT s; rebooting to use up a try"
			else
				log "slot $s: no network within $TRIAL_TIMEOUT s; rebooting to use up a try"
			fi
			cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
			sync
			reboot
		fi
	done
) &

# --- Kernel (KERNEL_UPDATE, device.conf: athena on mainline, the Skylight). The slot carries the
# kernel it was tested with, and once the slot is good that kernel goes to the kernel's partition:
# athena's boot, with the unit's own amp tuning; the Skylight's recovery, as it is (`techo5 tools
# kernel`, echod/internal/update/kernel). A new kernel is on trial as a new
# slot is: every boot takes a try, a daemon that settles commits it, and one that never settles
# reboots at TRIAL_TIMEOUT to use up a try; out of tries, the previous kernel is put back.
# Settled means more for a kernel than for a slot: Wi-Fi, the sound card and the backlight are the
# kernel's, and a kernel that keeps the daemon up without a network could never be updated again. So
# it also needs an address with a default route, and every HW_WAIT device. A boot that had all of it
# but a default route still uses its try (the kernel may have broken Wi-Fi), but says so
# (`techo5 tools kernel noroute`): a kernel whose every try ended that way is put back without being
# marked bad, since the router may only have been down, and is tried again a day later.
#
# Both reboots wait while the daemon says the unit is busy (/run/techo5/busy, echod
# feature/busy): an alarm or timer ringing, a voice request, a call, a sound, music, the wake light,
# or an alarm due within 30 minutes. Not forever: after BUSY_LIMIT seconds of busy in a row only a
# ring, or an alarm or timer due within the minute, still holds them, so a unit that is never idle, or a kernel that keeps something from
# ending, still gets its update or its verdict.
BUSY_LIMIT=${BUSY_LIMIT:-7200}
t5_hw_there() {
	for p in ${HW_WAIT:-}; do [ -e "$p" ] || return 1; done
	return 0
}
t5_kernel_works() {
	ip route show default 2>/dev/null | grep -q default && t5_hw_there
}
# t5_busy: whether a reboot now would cut something off. The marker names the daemon's pid and the
# uptime it was written at, and the daemon writes it again every few seconds: one more than a minute
# old, or whose pid is not the daemon's (gone, or reused), says nothing. busy_since is the caller's,
# kept across calls.
t5_busy() {
	bp=$(sed -n 's/^pid //p' /run/techo5/busy 2>/dev/null)
	ba=$(sed -n 's/^at //p' /run/techo5/busy 2>/dev/null)
	bt=$(cut -d. -f1 /proc/uptime)
	case "$bp" in ""|*[!0-9]*) ba=-999;; esac
	case "$ba" in ""|*[!0-9]*) ba=-999;; esac
	if [ $((bt-ba)) -gt 60 ] || [ "$(cat "/proc/$bp/comm" 2>/dev/null)" != techo5 ]; then
		busy_since=
		return 1
	fi
	if [ -z "$busy_since" ]; then
		busy_since=$bt
		log "kernel: waiting to reboot, the unit is busy: $(grep -vE '^(pid|at) ' /run/techo5/busy | xargs)"
	fi
	[ $((bt-busy_since)) -lt $BUSY_LIMIT ] && return 0
	# past the limit, only what rings now or within the minute
	grep -qxE 'ring|soon' /run/techo5/busy
}
if [ -n "${KERNEL_UPDATE:-}" ]; then
	/usr/local/bin/techo5 tools kernel boot >> /run/boot.log 2>&1
	(
		s=$(cat /run/techo5/slot 2>/dev/null)
		[ -n "$s" ] || exit 0
		routed=; settled=; up=; busy_since=
		while true; do
			sleep 15
			now=$(cut -d. -f1 /proc/uptime)
			ip route show default 2>/dev/null | grep -q default && routed=1
			up=
			pid=$(pidof techo5 | cut -d' ' -f1)
			if [ -n "$pid" ]; then
				started=$(awk '{print int($22/100)}' /proc/$pid/stat 2>/dev/null)
				# The slot trial above commits at this same moment: wait for the slot to be good.
				[ -n "$started" ] && [ $((now-started)) -ge $TRIAL_SETTLE ] &&
					[ "$(cat "/store/slots/$s.state" 2>/dev/null)" = good ] && up=1
			fi
			if [ -n "$up" ] && t5_kernel_works; then
				if [ -z "$settled" ]; then
					/usr/local/bin/techo5 tools kernel settled >> /run/boot.log 2>&1
					cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
					settled=1
				fi
				# apply reboots when it writes a kernel
				t5_busy && continue
				/usr/local/bin/techo5 tools kernel apply >> /run/boot.log 2>&1
				cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
				exit 0
			fi
			if [ $now -ge $TRIAL_TIMEOUT ] && grep -q '^trial' /data/techo5-linux/kernel/state 2>/dev/null; then
				# Busy: the trial runs on until it is not, and may still settle meanwhile.
				t5_busy && continue
				if [ -n "$up" ] && [ -z "$routed" ] && t5_hw_there; then
					log "kernel: no default route all this boot, nothing else wrong; rebooting to use up a try"
					/usr/local/bin/techo5 tools kernel noroute >> /run/boot.log 2>&1
				else
					log "kernel: daemon did not settle within $TRIAL_TIMEOUT s; rebooting to use up a try"
				fi
				cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
				sync
				# Read again at the last moment: a ring may have started meanwhile. noroute counts
				# a boot once, so going round again is safe.
				t5_busy && continue
				reboot
				exit 0
			fi
		done
	) &
fi

cp /run/boot.log $LOGDIR/boot.log 2>/dev/null
log "boot script done"
