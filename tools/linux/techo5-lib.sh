# techo5-lib.sh — shell functions shared by the initramfs init (tools/linux/init)
# and the root filesystem's boot script (tools/linux/rootfs/etc/techo5/boot.sh).
# Busybox sh. The caller may define log() before sourcing; the default goes to
# the kernel log.

type log >/dev/null 2>&1 || log() { echo "techo5: $*" > /dev/kmsg; }

# t5_board: which Echo this is, from the panel the bootloader names in the kernel command
# line. The daemon reads the same thing (internal/layout); this side needs it because one
# root filesystem serves all three boards, so the defaults below cannot be baked in per
# board. Prints nothing and returns non-zero when no panel is named.
t5_board() {
	for w in $(cat /proc/cmdline 2>/dev/null); do
		case $w in
		lcm=*checkers*) echo checkers; return 0 ;;
		lcm=*crown*) echo crown; return 0 ;;
		lcm=*cronos*) echo cronos; return 0 ;;
		esac
	done
	return 1
}

# t5_usb_acm: one CDC ACM serial function on the USB gadget (a COM port on the
# host). Idempotent. The 4.9.77 (TWRP) kernel has the legacy android_usb
# gadget, the 4.9.337 (LineageOS) kernel has configfs; both are handled.
# T5_PRODUCT names the USB serial gadget. A device may set it, and a root filesystem
# carrying /etc/techo5/device.conf does (boot.sh sources that file first); otherwise it
# follows the board, so a Show 8 does not put the Show 5's name on the gadget.
case $(t5_board) in
crown) T5_PRODUCT=${T5_PRODUCT:-Echo Show 8 Linux} ;;
*) T5_PRODUCT=${T5_PRODUCT:-Echo Show 5 Linux} ;;
esac

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

# t5_wifi_store_records <xml>: the networks in an Android WifiConfigStore, three
# lines each on stdout - S:<name>, K:<key management>, P:<key> - with the name
# and the key still in the form Android wrote them (a quoted string, or hex for
# a name Android could not spell). P: is empty for a network with no key.
#
# Android's store is a record per network and the fields have to be read a record
# at a time. Taking the first <string name="SSID"> in the file together with the
# first <string name="PreSharedKey"> pairs the name of one network with the
# passphrase of another as soon as an open network is saved ahead of the secured
# one - the unit then cannot associate, spends its boot attempts on a network
# that was never real, and lands in rescue.
#
# The sed ahead of awk puts every element on its own line, because Android has
# written that file both pretty printed and all on one line.
t5_wifi_store_records() {
	sed 's|><|>\n<|g' "$1" | awk '
	function unent(s) {
		gsub(/&quot;/, "\"", s); gsub(/&apos;/, "\047", s)
		gsub(/&lt;/, "<", s); gsub(/&gt;/, ">", s)
		gsub(/&amp;/, "\\&", s)
		return s
	}
	function val(s) { sub(/^[^>]*>/, "", s); sub(/<\/string>.*$/, "", s); return unent(s) }
	/<Network>/ { inrec = 1; ssid = ""; key = ""; kind = "" }
	inrec && /<string name="SSID">/          { ssid = val($0) }
	inrec && /<string name="PreSharedKey">/  { key  = val($0) }
	inrec && /<string name="ConfigKey">/     { kind = val($0); sub(/^.*"/, "", kind) }
	/<\/Network>/ {
		if (inrec && ssid != "") { print "S:" ssid; print "K:" kind; print "P:" key }
		inrec = 0
	}'
}

# t5_wifi_from_store <xml>: a whole wpa_supplicant configuration on stdout, one
# network= stanza per network Android saved, in the order Android had them, so
# the unit joins whichever of them it can hear.
#
# A name goes in as hex: that is the one form wpa_supplicant reads back exactly
# as it was written, and it is the form echod's wifi package writes, which
# rewrites this same file when somebody picks a network on the screen. A key is
# Android's own text and already in the supplicant's own syntax - a quoted
# passphrase, or the 64 hex digits of a key - so it is passed through as it
# stands. A network with no key is an open one and says so; anything else with
# no key wants a certificate or a WEP key this cannot supply, and is left out.
t5_wifi_from_store() {
	printf 'ctrl_interface=/run/wpa\nupdate_config=0\n'
	t5_wifi_store_records "$1" | while IFS= read -r rec; do
		case "$rec" in
		S:*) name=${rec#S:}; continue ;;
		K:*) kind=${rec#K:}; continue ;;
		P:*) key=${rec#P:} ;;
		*)   continue ;;
		esac
		case "$name" in
		'"'*'"')
			bare=${name#\"}; bare=${bare%\"}
			hexname=$(printf %s "$bare" | od -An -v -tx1 | tr -d ' \n')
			;;
		*)
			# Android keeps a name it cannot spell as text in hex already.
			hexname=$name
			;;
		esac
		printf %s "$hexname" | grep -Eq '^([0-9a-fA-F]{2}){1,32}$' || continue
		if [ -n "$key" ]; then
			printf 'network={\n\tssid=%s\n\tpsk=%s\n}\n' "$hexname" "$key"
		elif [ "$kind" = NONE ] || [ "$kind" = OWE ]; then
			printf 'network={\n\tssid=%s\n\tkey_mgmt=NONE\n}\n' "$hexname"
		fi
	done
}

# t5_wifi_conf <file>: write a wpa_supplicant configuration from the networks
# Android had saved on userdata, unless <file> exists already. Nothing is typed
# and nothing leaves the device.
t5_wifi_conf() {
	out=$1
	[ -s "$out" ] && return 0
	xml=/data/misc/apexdata/com.android.wifi/WifiConfigStore.xml   # Android 11
	[ -r "$xml" ] || xml=/data/misc/wifi/WifiConfigStore.xml         # older Android
	if [ ! -r "$xml" ]; then
		# Nothing to take one from: a unit installed from TWRP, or one that has moved house. An empty
		# configuration still lets the supplicant start, so the screen's Wi-Fi page can find and join a
		# network; without one the page would have nothing to talk to.
		mkdir -p "$(dirname "$out")"
		umask 077
		printf 'ctrl_interface=/run/wpa\nupdate_config=0\n' > "$out"
		umask 022
		log "wifi: no saved network and no Android store; waiting for one from the screen"
		return 1
	fi
	mkdir -p "$(dirname "$out")"
	umask 077
	t5_wifi_from_store "$xml" > "$out.tmp"
	umask 022
	n=$(grep -c '^network={' "$out.tmp" 2>/dev/null)
	if [ "${n:-0}" -lt 1 ]; then
		rm -f "$out.tmp"
		log "wifi: none of Android's saved networks in $xml is one this can join"
		return 1
	fi
	mv -f "$out.tmp" "$out"
	log "wifi: configuration written from $n of Android's saved networks"
}

# t5_ipv6_private <interface>: keep this device's IPv6 addresses from spelling out its MAC.
#
# Linux builds an interface identifier out of the hardware address by default, so a global address
# reads as the prefix the ISP handed out followed by the device's own MAC (EUI-64: the first octet's
# universal bit flipped and ff:fe pushed through the middle). That address names the hardware, and
# goes on naming it through every prefix the ISP ever changes - a satellite in somebody's house
# should not be announcing which one it is. A random identifier says nothing and costs nothing:
# nothing here is reached over IPv6, the daemon is found over IPv4 and mDNS.
#
# addr_gen_mode 3 is the random one and wants Linux 4.5; the Dot's 3.18 kernel has no addr_gen_mode
# at all, so temporary addresses (RFC 4941) are set either way for anything the device itself starts,
# and the kernel that can do no better says so in the log rather than quietly differing.
#
# Before the link comes up, because the identifier is chosen when the address is generated and an
# address already there does not change.
t5_ipv6_private() {
	ifc=$1; mode=
	for w in default "$ifc"; do
		d=/proc/sys/net/ipv6/conf/$w
		[ -d "$d" ] || continue
		[ -e "$d/use_tempaddr" ] && echo 2 > "$d/use_tempaddr" 2>/dev/null
		[ -e "$d/addr_gen_mode" ] || continue
		echo 3 > "$d/addr_gen_mode" 2>/dev/null
		mode=$(cat "$d/addr_gen_mode" 2>/dev/null)
	done
	case "$mode" in
	3) log "ipv6: addresses take a random identifier, not this device's MAC" ;;
	"") log "ipv6: this kernel has no addr_gen_mode; addresses carry this device's MAC" ;;
	*) log "ipv6: addr_gen_mode stayed $mode; addresses may still carry this device's MAC" ;;
	esac
}

# t5_wifi_mac <iface>: the Echo Spot's bcmdhd comes up with Broadcom's placeholder address
# (00:90:4c:...), the same on every Spot, so two Spots on one network share one DHCP lease and
# knock each other off. Give it the factory address from IDME. The link has to be down, and the
# driver refuses (EBUSY) until its firmware is in: returns 1 then, to be tried again; any other
# refusal is logged and the placeholder kept. Other
# addresses are left alone, so a Show or a Dot keeps the one its router reservation is for.
t5_wifi_mac() {
	case "$(cat "/sys/class/net/$1/address" 2>/dev/null)" in 00:90:4c:*) ;; *) return 0 ;; esac
	mac=$(tr -dc '0-9A-Fa-f' 2>/dev/null < /proc/idme/mac_addr | tr 'A-F' 'a-f')
	# Twelve digits, not all zeros, and not a group address.
	case "$mac" in 000000000000|?[13579bdf]*) mac= ;; esac
	[ ${#mac} -eq 12 ] || { log "wifi: no factory address in IDME; keeping the placeholder"; return 0; }
	# A running supplicant holds the link: changing it under one would drop the network.
	pidof wpa_supplicant >/dev/null && return 0
	ip link set "$1" down 2>/dev/null
	if ! ip link set "$1" address "$(echo "$mac" | sed 's/../&:/g; s/:$//')" 2>/tmp/ifmac.err; then
		grep -qi busy /tmp/ifmac.err && return 1
		log "wifi: factory address refused: $(cat /tmp/ifmac.err)"
		return 0
	fi
	log "wifi: factory address set"
}

# t5_wifi_up <module.ko> <wpa.conf>: load the vendor driver if wlan0 is not
# there yet, associate, and take a DHCP lease (udhcpc stays running to renew
# it). Sets IP. The vendor driver's first full scan alone takes several
# seconds; association is allowed WIFI_WAIT seconds (60).
t5_wifi_up() {
	mod=$1; conf=$2; IP=
	# Loopback first: nothing brought it up before (the kernel leaves it down and no init script of
	# Android's runs), and the video player's guard proxy (feature/video/guard.go) listens on it. Up,
	# it is what it is on any Linux: 127.0.0.1, which nothing on the network reaches.
	ip link set lo up 2>/dev/null
	if ! ip link show wlan0 >/dev/null 2>&1; then
		[ -e "$mod" ] || { log "wifi: driver not found at $mod"; return 1; }
		insmod "$mod" 2>/tmp/insmod.err || { log "wifi: insmod failed: $(cat /tmp/insmod.err)"; return 1; }
		n=0; while [ $n -lt 10 ] && ! ip link show wlan0 >/dev/null 2>&1; do sleep 1; n=$((n+1)); done
		ip link show wlan0 >/dev/null 2>&1 || { log "wifi: driver loaded but no wlan0"; return 1; }
		log "wifi: driver loaded, wlan0 present"
	fi
	[ -r "$conf" ] || { log "wifi: no configuration"; return 1; }
	mkdir -p /run/wpa
	t5_ipv6_private wlan0
	n=0; until t5_wifi_mac wlan0; do
		n=$((n+1)); [ $n -ge 15 ] && { log "wifi: factory address not set: $(cat /tmp/ifmac.err)"; break; }
		sleep 1
	done
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
	# No network to wait for: the supplicant is up for the screen to add one, and waiting out
	# WIFI_WAIT would only hold the rest of the boot back.
	# The lease client is started too, in the background: a network joined from the screen asks it for
	# an address (lib/wifi renews it), and it keeps trying until there is one.
	if ! grep -q '^network={' "$conf"; then
		pidof udhcpc >/dev/null || udhcpc -i wlan0 -b -R -p /run/udhcpc.pid -s "${UDHCPC_SCRIPT:-/usr/share/udhcpc/default.script}" > /tmp/udhcpc.log 2>&1
		log "wifi: no network saved yet"
		return 1
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

# t5_ntp_peers: the time servers, as ntpd's -p arguments. Several: busybox ntpd
# takes one address for each name, so pool.ntp.org alone is one pool member, and
# one that never answers left the clock at 2010 until the next reboot (#77).
# NTP_SERVER, from device.conf, is asked first.
t5_ntp_peers() {
	for s in $NTP_SERVER time.cloudflare.com time.google.com 0.pool.ntp.org 1.pool.ntp.org 2.pool.ntp.org; do
		printf ' -p %s' "$s"
	done
}

# t5_ntp: set the clock once from NTP (the RTC is not trusted), then write it
# to the RTC so the next boot starts closer. Bounded: an unreachable server
# must not hold the boot. The RTC keeps UTC: the kernel reads it as UTC at boot,
# so local time written there started a warm reboot hours off.
t5_ntp() {
	timeout -s KILL ${NTP_WAIT:-40} ntpd -n -q $(t5_ntp_peers) > /tmp/ntpd.log 2>&1 || { log "clock: ntp failed"; return 1; }
	hwclock -w -u 2>/dev/null
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
	# Pairings and bluez-alsa's state must survive reboots and slot changes: keep
	# them on userdata (the root is read-only).
	mkdir -p /run/dbus /data/misc/techo5/bluetooth /data/misc/techo5/bluealsa
	mountpoint -q /var/lib/bluetooth || mount --bind /data/misc/techo5/bluetooth /var/lib/bluetooth
	# Alpine's bluez-alsa was built with /usr/var as its state directory.
	for d in /var/lib/bluealsa /usr/var/lib/bluealsa; do
		[ -d "$d" ] && { mountpoint -q "$d" || mount --bind /data/misc/techo5/bluealsa "$d"; }
	done
	# The daemon starts the bus too, for AirPlay and Spotify Connect (feature/streaming): whichever
	# holds /run/techo5-dbus-starting starts it, and the other waits for it.
	if ! pidof dbus-daemon >/dev/null; then
		if mkdir /run/techo5-dbus-starting 2>/dev/null; then
			pidof dbus-daemon >/dev/null || dbus-daemon --system --nofork --nopidfile >> "$logdir/dbus.log" 2>&1 &
			sleep 1
			rmdir /run/techo5-dbus-starting
		else
			n=0; while [ $n -lt 5 ] && [ ! -S /run/dbus/system_bus_socket ]; do sleep 1; n=$((n+1)); done
			# Whoever held it died holding it: start the bus after all.
			if [ ! -S /run/dbus/system_bus_socket ] && ! pidof dbus-daemon >/dev/null; then
				rmdir /run/techo5-dbus-starting 2>/dev/null
				dbus-daemon --system --nofork --nopidfile >> "$logdir/dbus.log" 2>&1 &
				sleep 1
			fi
		fi
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
