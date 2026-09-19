# techo5-lib.sh — shell functions shared by the initramfs init (tools/linux/init)
# and the root filesystem's boot script (tools/linux/rootfs/etc/techo5/boot.sh).
# Busybox sh. The caller may define log() before sourcing; the default goes to
# the kernel log.

type log >/dev/null 2>&1 || log() { echo "techo5: $*" > /dev/kmsg; }

# t5_usb_acm: one CDC ACM serial function on the USB gadget (a COM port on the
# host). Idempotent. The 4.9.77 (TWRP) kernel has the legacy android_usb
# gadget, the 4.9.337 (LineageOS) kernel has configfs; both are handled.
# T5_PRODUCT names the USB serial gadget; a device sets it (rootfs: /etc/techo5/device.conf).
T5_PRODUCT=${T5_PRODUCT:-Echo Show 5 Linux}

t5_usb_acm() {
	A=/sys/class/android_usb/android0
	G=/sys/kernel/config/usb_gadget/g1
	if [ -d $A ]; then
		[ "$(cat $A/functions 2>/dev/null)" = acm ] && [ "$(cat $A/enable 2>/dev/null)" = 1 ] && return 0
		echo 0 > $A/enable
		echo 1d6b > $A/idVendor
		echo 0104 > $A/idProduct
		echo TECHO5 > $A/iManufacturer
		echo "$T5_PRODUCT" > $A/iProduct
		echo techo5 > $A/iSerial
		echo acm > $A/functions
		[ -e $A/f_acm/instances ] && echo 1 > $A/f_acm/instances
		echo 1 > $A/enable && log "usb: legacy gadget enabled (acm)" || log "usb: legacy gadget enable failed"
		return 0
	fi
	mountpoint -q /sys/kernel/config || mount -t configfs none /sys/kernel/config 2>/dev/null || { log "usb: configfs mount failed"; return 1; }
	if [ -d $G ] && [ -n "$(cat $G/UDC 2>/dev/null)" ]; then
		return 0
	fi
	mkdir -p $G || return 1
	echo 0x1d6b > $G/idVendor
	echo 0x0104 > $G/idProduct
	echo 0x0200 > $G/bcdUSB
	mkdir -p $G/strings/0x409
	echo "TECHO5" > $G/strings/0x409/manufacturer
	echo "$T5_PRODUCT" > $G/strings/0x409/product
	echo "techo5" > $G/strings/0x409/serialnumber
	mkdir -p $G/configs/c.1/strings/0x409
	echo "acm" > $G/configs/c.1/strings/0x409/configuration
	mkdir -p $G/functions/acm.usb0
	[ -e $G/configs/c.1/acm.usb0 ] || ln -s $G/functions/acm.usb0 $G/configs/c.1/acm.usb0
	UDC=$(ls /sys/class/udc 2>/dev/null | head -1)
	if [ -n "$UDC" ]; then
		echo "$UDC" > $G/UDC && log "usb: gadget bound to $UDC (acm)" || log "usb: bind to $UDC failed"
	else
		log "usb: no UDC; gadget not started"
	fi
	[ -e /dev/ttyGS0 ] || mdev -s
}

# t5_wifi_conf <file>: write a wpa_supplicant configuration from the network
# Android had saved on userdata, unless <file> exists already. Nothing is typed
# and nothing leaves the device.
t5_wifi_conf() {
	out=$1
	[ -s "$out" ] && return 0
	xml=/data/misc/apexdata/com.android.wifi/WifiConfigStore.xml   # Android 11
	[ -r "$xml" ] || xml=/data/misc/wifi/WifiConfigStore.xml         # older Android
	[ -r "$xml" ] || { log "wifi: no configuration at $out and no Android store to take one from"; return 1; }
	xmlval() { sed -n "s/.*<string name=\"$1\">\(.*\)<\/string>.*/\1/p" "$xml" | head -1 | sed 's/&quot;//g; s/&amp;/\&/g; s/&lt;/</g; s/&gt;/>/g'; }
	ssid=$(xmlval SSID); psk=$(xmlval PreSharedKey)
	[ -n "$ssid" ] && [ -n "$psk" ] || { log "wifi: no PSK network in $xml"; return 1; }
	mkdir -p "$(dirname "$out")"
	umask 077
	printf 'ctrl_interface=/run/wpa\nupdate_config=0\nnetwork={\n\tssid="%s"\n\tpsk="%s"\n}\n' "$ssid" "$psk" > "$out"
	umask 022
	log "wifi: configuration written from Android's saved network '$ssid'"
}

# t5_wifi_up <module.ko> <wpa.conf>: load the vendor driver if wlan0 is not
# there yet, associate, and take a DHCP lease (udhcpc stays running to renew
# it). Sets IP. The vendor driver's first full scan alone takes several
# seconds; association is allowed WIFI_WAIT seconds (60).
t5_wifi_up() {
	mod=$1; conf=$2; IP=
	# A module must match the running kernel; retain the vendor path for 4.9.
	kernel_mod=/lib/modules/$(uname -r)/extra/mt76x8_wlan.ko
	[ -f "$kernel_mod" ] && mod=$kernel_mod
	if ! ip link show wlan0 >/dev/null 2>&1; then
		[ -e "$mod" ] || { log "wifi: driver not found at $mod"; return 1; }
		insmod "$mod" 2>/tmp/insmod.err || { log "wifi: insmod failed: $(cat /tmp/insmod.err)"; return 1; }
		n=0; while [ $n -lt 10 ] && ! ip link show wlan0 >/dev/null 2>&1; do sleep 1; n=$((n+1)); done
		ip link show wlan0 >/dev/null 2>&1 || { log "wifi: driver loaded but no wlan0"; return 1; }
		log "wifi: driver loaded, wlan0 present"
	fi
	[ -r "$conf" ] || { log "wifi: no configuration"; return 1; }
	mkdir -p /run/wpa
	# The Echo Spot's bcmdhd is a USB device that downloads its firmware at insmod, drops off the bus
	# and comes back: wlan0 exists before it can be opened, and bringing it up then fails with EBUSY,
	# which leaves wpa_supplicant unable to start. Wait for the open to succeed. The Show's mt76x8 is
	# up on the first try.
	n=0; until ip link set wlan0 up 2>/tmp/ifup.err; do
		n=$((n+1)); [ $n -ge 30 ] && { log "wifi: wlan0 would not come up: $(cat /tmp/ifup.err)"; return 1; }
		sleep 1
	done
	[ $n -gt 0 ] && log "wifi: wlan0 up after ${n}s"
	if ! pidof wpa_supplicant >/dev/null; then
		wpa_supplicant -B -i wlan0 -c "$conf" -P /run/wpa.pid > /tmp/wpa.log 2>&1
	fi
	n=0; while [ $n -lt ${WIFI_WAIT:-60} ]; do
		wpa_cli -p /run/wpa -i wlan0 status 2>/dev/null | grep -q '^wpa_state=COMPLETED' && break
		sleep 1; n=$((n+1))
	done
	if ! wpa_cli -p /run/wpa -i wlan0 status 2>/dev/null | grep -q '^wpa_state=COMPLETED'; then
		log "wifi: not associated ($(wpa_cli -p /run/wpa -i wlan0 status 2>/dev/null | grep wpa_state))"
		return 1
	fi
	if ! pidof udhcpc >/dev/null; then
		udhcpc -i wlan0 -b -R -t 10 -p /run/udhcpc.pid -s "${UDHCPC_SCRIPT:-/usr/share/udhcpc/default.script}" > /tmp/udhcpc.log 2>&1
	fi
	n=0; while [ $n -lt 30 ]; do
		IP=$(ip -4 addr show wlan0 2>/dev/null | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -1)
		[ -n "$IP" ] && break
		sleep 1; n=$((n+1))
	done
	[ -n "$IP" ] || { log "wifi: associated but no lease"; return 1; }
	log "wifi: $IP on wlan0"
}

# t5_ip: the current IPv4 address on wlan0, empty if none.
t5_ip() { ip -4 addr show wlan0 2>/dev/null | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -1; }

# t5_wifi_prefer5: keep the link on the network's strongest worthwhile radio, 5 GHz first.
#
# The supplicant picks a radio at connect time and often takes 2.4 GHz, where the Bluetooth half of the
# same chip shares its antenna and spectrum: on the bench that was 1-4 MB/s against 3-12 MB/s on 5 GHz
# with earbuds connected (2026-09-16). It can also take a far 5 GHz radio: the bench once booted onto one
# at -70 dBm, took 70 s to get a lease and ran at 20-40 Mbit/s, next to a -34 dBm 2.4 GHz radio.
#
# Called by the keeper every minute. A 5 GHz radio at T5_5G_MIN dBm or better is strong: the link moves
# to the strongest one when it is on 2.4 GHz or on a weak 5 GHz radio. With no strong 5 GHz radio, a
# weak 5 GHz link moves to 2.4 GHz when that is T5_2G_GAIN dB stronger and at T5_2G_MIN or better. Until
# the link is on a strong 5 GHz radio it keeps scanning, every five minutes, so a 5 GHz radio that comes
# back (or was never listed: a connected supplicant stops scanning) is found. A move pins the running
# supplicant to the chosen channel, nothing is saved; if it does not associate there within 30 s it
# goes back to every band and leaves it for 30 minutes.
t5_wifi_prefer5() {
	w="wpa_cli -p /run/wpa -i wlan0"
	link=$(iw dev wlan0 link 2>/dev/null)
	freq=$(echo "$link" | sed -n 's/.*freq: \([0-9]*\).*/\1/p'); freq=${freq%%.*}
	sig=$(echo "$link" | sed -n 's/.*signal: \(-*[0-9]*\).*/\1/p')
	[ -n "$freq" ] && [ -n "$sig" ] || return 0
	strong=${T5_5G_MIN:--65}
	# On a strong 5 GHz radio already: nothing to look for.
	[ "$freq" -gt 4000 ] && [ "$sig" -ge "$strong" ] && return 0
	now=$(cut -d. -f1 /proc/uptime)
	mkdir -p /run/techo5
	[ "$now" -ge "$(cat /run/techo5/prefer5-after 2>/dev/null || echo 0)" ] || return 0
	id=$($w list_networks 2>/dev/null | awk -F'\t' 'NR>1 && $4 ~ /CURRENT/ {print $1}')
	ssid=$($w status 2>/dev/null | sed -n 's/^ssid=//p')
	[ -n "$id" ] && [ -n "$ssid" ] || return 0

	# The strongest radio of this network on each band, as "signal freq".
	best5=$($w scan_results 2>/dev/null | awk -F'\t' -v s="$ssid" 'NR>1 && $5 == s && $2 > 4000 {print $3, $2}' | sort -n | tail -1)
	best2=$($w scan_results 2>/dev/null | awk -F'\t' -v s="$ssid" 'NR>1 && $5 == s && $2 < 4000 {print $3, $2}' | sort -n | tail -1)
	sig5=${best5%% *}; f5=${best5##* }
	sig2=${best2%% *}; f2=${best2##* }

	# Keep looking: the list ages out, and radios come and go.
	if [ "$now" -ge "$(cat /run/techo5/prefer5-scan 2>/dev/null || echo 0)" ]; then
		$w scan >/dev/null 2>&1
		echo $((now + 300)) > /run/techo5/prefer5-scan
	fi

	target=
	if [ -n "$sig5" ] && [ "$sig5" -ge "$strong" ] && [ "$f5" != "$freq" ]; then
		target=$f5
		log "wifi: on $freq MHz at $sig dBm while '$ssid' has 5 GHz at $sig5 dBm; moving to $f5 MHz"
	elif [ "$freq" -gt 4000 ] && [ -n "$sig2" ] && [ "$sig2" -ge "${T5_2G_MIN:--60}" ] &&
		[ $((sig2 - sig)) -ge "${T5_2G_GAIN:-20}" ]; then
		target=$f2
		log "wifi: on a weak 5 GHz radio ($sig dBm) while '$ssid' has 2.4 GHz at $sig2 dBm; moving to $f2 MHz"
	fi
	[ -n "$target" ] || return 0

	$w set_network "$id" freq_list "$target" >/dev/null 2>&1
	$w reassociate >/dev/null 2>&1
	n=0
	while [ $n -lt 30 ]; do
		sleep 2; n=$((n+2))
		f=$(iw dev wlan0 link 2>/dev/null | sed -n 's/.*freq: \([0-9]*\).*/\1/p')
		if [ "${f%%.*}" = "$target" ] && $w status 2>/dev/null | grep -q '^wpa_state=COMPLETED'; then
			log "wifi: on $target MHz"
			return 0
		fi
	done
	log "wifi: no association on $target MHz in 30 s; back to every band for 30 minutes"
	$w set_network "$id" freq_list "" >/dev/null 2>&1
	$w reassociate >/dev/null 2>&1
	echo $((now + 1800)) > /run/techo5/prefer5-after
}

# t5_ntp: set the clock once from NTP (the RTC is not trusted), then write it
# to the RTC so the next boot starts closer. Bounded: an unreachable server
# must not hold the boot.
t5_ntp() {
	timeout -s KILL ${NTP_WAIT:-40} ntpd -n -q -p "${NTP_SERVER:-pool.ntp.org}" > /tmp/ntpd.log 2>&1 || { log "clock: ntp failed"; return 1; }
	hwclock -w 2>/dev/null
	log "clock: $(date)"
}

# t5_dropbear <keydir> [extra args]: SSH on port 22 with host keys kept in
# <keydir> (on userdata, so the host key is stable across images and slots).
# /etc/dropbear is a symlink to <keydir> in the rootfs; in the initramfs it is
# a directory and gets links to the keys.
t5_dropbear() {
	keydir=$1; shift
	mkdir -p "$keydir" /etc/dropbear
	for t in rsa ed25519; do
		[ -e "$keydir/dropbear_${t}_host_key" ] || dropbearkey -t $t -f "$keydir/dropbear_${t}_host_key" >/dev/null 2>&1
		[ -L /etc/dropbear ] || ln -sf "$keydir/dropbear_${t}_host_key" /etc/dropbear/dropbear_${t}_host_key
	done
	pidof dropbear >/dev/null && return 0
	dropbear -R -p 22 "$@" > /tmp/dropbear.log 2>&1 && log "ssh: dropbear listening on :22" || { log "ssh: dropbear failed to start"; return 1; }
}

# Bluetooth: the vendor driver gives a raw H4 channel (/dev/stpbt); btbridge
# turns it into hci0 through the kernel's vhci driver; BlueZ and bluez-alsa
# sit on top. Needs a kernel with CONFIG_BT + CONFIG_BT_HCIVHCI — without
# /dev/vhci this quietly does nothing, so an older boot image keeps working.
t5_bt_up() {
	mod=$1; logdir=${2:-/tmp}
	# btmtksdio exposes a native HCI controller on mainline.
	if [ ! -d /sys/class/bluetooth/hci0 ]; then
		[ -e /dev/vhci ] || { log "bt: no /dev/vhci (kernel without Bluetooth); skipping"; return 1; }
		# BT_UART (device.conf): a Broadcom controller on a tty, which btbridge patches and brings up itself
		# (the Echo Spot); otherwise the MediaTek driver module and its /dev/stpbt.
		if [ -n "${BT_UART:-}" ]; then
			[ -e "$BT_UART" ] || { log "bt: no $BT_UART"; return 1; }
		elif [ ! -e /dev/stpbt ]; then
			[ -e "$mod" ] || { log "bt: driver not found at $mod"; return 1; }
			insmod "$mod" 2>/tmp/insmod-bt.err || { log "bt: insmod failed: $(cat /tmp/insmod-bt.err)"; return 1; }
			n=0; while [ $n -lt 10 ] && [ ! -e /dev/stpbt ]; do sleep 1; n=$((n+1)); done
			[ -e /dev/stpbt ] || { log "bt: driver loaded but no /dev/stpbt"; return 1; }
		fi
		command -v btbridge >/dev/null || { log "bt: no btbridge"; return 1; }
		# The factory address from IDME; the firmware otherwise comes up with a random one.
		addr=$(tr -d '\n\0' < /proc/idme/bt_mac_addr 2>/dev/null)
		if [ -n "${BT_UART:-}" ]; then
			(while true; do btbridge -uart "$BT_UART" ${BT_HCD:+-hcd "$BT_HCD"} ${BT_BAUD:+-baud "$BT_BAUD"} ${addr:+-bdaddr "$addr"} >> "$logdir/btbridge.log" 2>&1; sleep 2; done) &
		else
			(while true; do btbridge ${addr:+-bdaddr "$addr"} >> "$logdir/btbridge.log" 2>&1; sleep 2; done) &
		fi
		n=0; while [ $n -lt 20 ] && [ ! -d /sys/class/bluetooth/hci0 ]; do sleep 1; n=$((n+1)); done
		[ -d /sys/class/bluetooth/hci0 ] || { log "bt: bridge up but no hci0"; return 1; }
	fi
	# Pairings and bluez-alsa's state must survive reboots and slot changes: keep
	# them on userdata (the root is read-only).
	mkdir -p /run/dbus /data/misc/techo5/bluetooth /data/misc/techo5/bluealsa
	mountpoint -q /var/lib/bluetooth || mount --bind /data/misc/techo5/bluetooth /var/lib/bluetooth
	# Alpine's bluez-alsa was built with /usr/var as its state directory.
	for d in /var/lib/bluealsa /usr/var/lib/bluealsa; do
		[ -d "$d" ] && { mountpoint -q "$d" || mount --bind /data/misc/techo5/bluealsa "$d"; }
	done
	if ! pidof dbus-daemon >/dev/null; then
		dbus-daemon --system --nofork --nopidfile >> "$logdir/dbus.log" 2>&1 &
		sleep 1
	fi
	bd=$(command -v bluetoothd || echo /usr/lib/bluetooth/bluetoothd)
	[ -x "$bd" ] && "$bd" -n >> "$logdir/bluetoothd.log" 2>&1 &
	# Seen on the bench: after the first power-on the controller answers commands but never
	# reports an inquiry result or an advertisement until it has been powered off and on once.
	n=0; while [ $n -lt 10 ] && ! timeout 3 btmgmt info 2>/dev/null | grep -q "current settings: powered"; do sleep 1; n=$((n+1)); done
	timeout 5 btmgmt power off >/dev/null 2>&1; sleep 1; timeout 5 btmgmt power on >/dev/null 2>&1
	# bluez-alsa registers its A2DP endpoints with bluetoothd, so it must come after it.
	command -v bluealsa >/dev/null && bluealsa -p a2dp-source >> "$logdir/bluealsa.log" 2>&1 &
	log "bt: hci0 up; bluetoothd and bluealsa started"
	return 0
}
