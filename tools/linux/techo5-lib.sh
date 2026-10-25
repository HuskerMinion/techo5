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
	# USB_RNDIS (device.conf): a network link on the same cable, set up by t5_usb_net.
	if [ -n "${USB_RNDIS:-}" ]; then
		mkdir -p $G/functions/rndis.usb0
		[ -e $G/configs/c.1/rndis.usb0 ] || ln -s $G/functions/rndis.usb0 $G/configs/c.1/rndis.usb0
	fi
	UDC=$(ls /sys/class/udc 2>/dev/null | head -1)
	if [ -n "$UDC" ]; then
		echo "$UDC" > $G/UDC && log "usb: gadget bound to $UDC (acm)" || log "usb: bind to $UDC failed"
	else
		log "usb: no UDC; gadget not started"
	fi
	[ -e /dev/ttyGS0 ] || mdev -s
}

# t5_usb_net: when USB_RNDIS is set (device.conf; the address, e.g. 172.16.42.1), give the RNDIS
# link that address and hand the host one of .2-.9 by DHCP, with no router, so the host's own
# default route stays where it is. Idempotent. Nothing on devices that do not set USB_RNDIS.
t5_usb_net() {
	[ -n "${USB_RNDIS:-}" ] || return 0
	# The interface appears when the gadget binds; give it a few seconds.
	n=0; while [ $n -lt 5 ] && ! ip link show usb0 >/dev/null 2>&1 && ! ip link show rndis0 >/dev/null 2>&1; do
		sleep 1; n=$((n+1))
	done
	ifc=$(cat /sys/kernel/config/usb_gadget/g1/functions/rndis.usb0/ifname 2>/dev/null)
	ip link show "$ifc" >/dev/null 2>&1 || ifc=usb0
	ip link show "$ifc" >/dev/null 2>&1 || ifc=rndis0
	ip link show "$ifc" >/dev/null 2>&1 || { log "usb: no RNDIS interface"; return 1; }
	ip addr add "$USB_RNDIS/24" dev "$ifc" 2>/dev/null
	ip link set "$ifc" up
	pfx=${USB_RNDIS%.*}
	mkdir -p /run
	printf 'start %s.2\nend %s.9\ninterface %s\nlease_file /run/udhcpd-usb.leases\npidfile /run/udhcpd-usb.pid\noption subnet 255.255.255.0\n' \
		"$pfx" "$pfx" "$ifc" > /run/udhcpd-usb.conf
	[ -e /run/udhcpd-usb.pid ] && kill -0 "$(cat /run/udhcpd-usb.pid)" 2>/dev/null && return 0
	: > /run/udhcpd-usb.leases
	if command -v udhcpd >/dev/null; then
		udhcpd /run/udhcpd-usb.conf
	elif command -v busybox-extras >/dev/null; then
		busybox-extras udhcpd /run/udhcpd-usb.conf
	else
		log "usb: no udhcpd; the host needs a fixed address in $pfx.0/24"
	fi
	log "usb: $USB_RNDIS on $ifc"
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
# Android had saved on userdata, unless <file> holds a network already. Nothing
# is typed and nothing leaves the device.
#
# WIFI_STORES (device.conf) names more places to look, after the usual two: on
# athena /data is a directory of Fire OS's userdata, so Fire OS's own store is
# under /store/userdata (in a slot) or /userdata (in rescue). With WIFI_STORES
# set, a file with no network in it (the header written when there was nothing
# to import) is tried again, so a store that turns up later is still taken; but
# only until one import has worked (.wifi-imported beside <file>), so networks
# forgotten on the screen afterwards stay forgotten.
t5_wifi_conf() {
	out=$1
	grep -q '^network={' "$out" 2>/dev/null && return 0
	if [ -s "$out" ]; then
		[ -n "${WIFI_STORES:-}" ] || return 0
		[ -e "$(dirname "$out")/.wifi-imported" ] && return 0
	fi
	xml=
	for c in /data/misc/apexdata/com.android.wifi/WifiConfigStore.xml /data/misc/wifi/WifiConfigStore.xml \
		${WIFI_STORES:-}; do
		[ -r "$c" ] && { xml=$c; break; }
	done
	if [ -z "$xml" ]; then
		[ -s "$out" ] && return 1
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
	: > "$(dirname "$out")/.wifi-imported"
	log "wifi: configuration written from $n of Android's saved networks"
}

# t5_wifi_watch <file>: a running supplicant that read <file> while it held no network keeps none,
# however many are written into it later; it needs a reconfigure. This watches for exactly that: the
# file holding networks the supplicant does not have (by count), steady for a few seconds, while no
# one is choosing a network on the screen (/run/techo5/wifi-setup). The daemon's own writes
# (lib/wifi) reconfigure as they go, so the counts already agree there, and a join it is in the middle
# of (which pins one network with select_network) is never undone from here. An edit that only
# changes a passphrase keeps the count and is not seen: reconfigure by hand, or reboot. Runs in the
# background for good; device.conf turns it on (WIFI_CONF_WATCH=1, athena).
t5_wifi_watch() {
	conf=$1
	(
		while true; do
			sleep 5
			[ -e /run/techo5/wifi-setup ] && continue
			pidof wpa_supplicant >/dev/null || continue
			want=$(grep -c '^network={' "$conf" 2>/dev/null)
			[ "${want:-0}" -gt 0 ] || continue
			have=$(wpa_cli -p /run/wpa -i wlan0 list_networks 2>/dev/null | grep -c '^[0-9]')
			[ "$want" = "$have" ] && continue
			# Not while it is being written: the file must be the same 5 s later.
			a=$(cksum < "$conf" 2>/dev/null); sleep 5; b=$(cksum < "$conf" 2>/dev/null)
			[ "$a" = "$b" ] || continue
			if wpa_cli -p /run/wpa -i wlan0 reconfigure >/dev/null 2>&1; then
				log "wifi: $conf holds $want networks, the supplicant had $have; reconfigured"
				# The lease client started with no network is still asking; one that gave up is
				# started again.
				pidof udhcpc >/dev/null || udhcpc -i wlan0 -b -R -p /run/udhcpc.pid \
					-s "${UDHCPC_SCRIPT:-/usr/share/udhcpc/default.script}" > /tmp/udhcpc.log 2>&1
			else
				log "wifi: reconfigure after a change to $conf failed"
			fi
		done
	) &
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
	# Placeholders the drivers come up with instead of the unit's own address: Broadcom's on the Echo
	# Spot, and MediaTek's default 00:0c:43:26:60:00 on athena's MT7663 under mainline (every unit
	# the same, so two units on one network took the same DHCP lease; unit 1 and 2, 2026-10-15).
	case "$(cat "/sys/class/net/$1/address" 2>/dev/null)" in 00:90:4c:*|00:0c:43:26:60:00) ;; *) return 0 ;; esac
	mac=$(t5_idme_mac mac_addr | tr 'A-F' 'a-f')
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
	# builtin: a driver in the kernel (athena on mainline), which makes wlan0 once its firmware is in.
	if [ "$mod" = builtin ]; then
		n=0; while [ $n -lt 20 ] && ! ip link show wlan0 >/dev/null 2>&1; do sleep 1; n=$((n+1)); done
		ip link show wlan0 >/dev/null 2>&1 || { log "wifi: no wlan0 from the built-in driver"; return 1; }
	else
		# A module must match the running kernel; retain the vendor path for 4.9.
		kernel_mod=/lib/modules/$(uname -r)/extra/mt76x8_wlan.ko
		[ -f "$kernel_mod" ] && mod=$kernel_mod
	fi
	if ! ip link show wlan0 >/dev/null 2>&1; then
		[ -e "$mod" ] || { log "wifi: driver not found at $mod"; return 1; }
		until t5_wifi_load "$mod"; do
			t5_wifi_reset "$mod" || return 1
		done
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
	# The address: fixed when the setting says so (techo5-net, which falls back to DHCP if the fixed
	# one does not work), else a lease.
	if [ -x /usr/local/sbin/techo5-net ]; then
		/usr/local/sbin/techo5-net up
	elif ! pidof udhcpc >/dev/null; then
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

# t5_wifi_load <module.ko>: load the Wi-Fi driver and wait for its wlan0.
t5_wifi_load() {
	insmod "$1" 2>/tmp/insmod.err || { log "wifi: insmod failed: $(cat /tmp/insmod.err)"; return 1; }
	n=0; while [ $n -lt 10 ] && ! ip link show wlan0 >/dev/null 2>&1; do sleep 1; n=$((n+1)); done
	ip link show wlan0 >/dev/null 2>&1 || { log "wifi: driver loaded but no wlan0"; return 1; }
}

# t5_wifi_reset <module.ko>: after a driver that made no wlan0, power the chip off and on and let
# t5_wifi_load try again, twice a boot at most. On the Show 5 the MT7668's Wi-Fi half failed so on
# 5 boots of about 260 (2026-10-17..19; Bluetooth, on the same SDIO card, came up), and the network
# keeper rebooted 15 minutes later, which always fixed it. Unbinding the SDIO host (WIFI_SDIO_HOST,
# its device in its platform driver) takes the card's supply, the chip's power, off; binding it powers
# the chip up and probes both halves again (Bluetooth's settings are made later, by t5_bt_up). The
# kernel's account of the failure is kept first, the last five in /data/techo5-linux/wifi-fail.
t5_wifi_reset() {
	h=${WIFI_SDIO_HOST:-}
	# The host itself, not only its driver: Amazon's kernels may name theirs mtk-msdc too.
	[ -n "$h" ] && [ -e "$h" ] && [ -w "${h%/*}/bind" ] || return 1
	tries=$(cat /run/techo5/wifi-resets 2>/dev/null || echo 0)
	[ "$tries" -lt 2 ] || { log "wifi: no wlan0 after two resets of the chip"; return 1; }
	echo $((tries + 1)) > /run/techo5/wifi-resets
	d=/data/techo5-linux/wifi-fail
	mkdir -p $d
	# Numbered as the crash records are (boot.sh): the clock is not set yet.
	n=$(($(cat $d/.count 2>/dev/null || echo 0) + 1))
	echo $n > $d/.count
	dmesg > "$d/$(printf %05d $n)-$(cut -c1-8 /proc/sys/kernel/random/boot_id 2>/dev/null).txt" 2>/dev/null
	ls -1 $d/[0-9][0-9][0-9][0-9][0-9]-* 2>/dev/null | sort -r | tail -n +6 | while read -r f; do rm -f "$f"; done
	m=$(basename "$1" .ko | tr - _)
	grep -q "^$m " /proc/modules && rmmod "$m" 2>/dev/null
	[ -e "$h" ] && echo "${h##*/}" > "${h%/*}/unbind"
	sleep 2
	echo "${h##*/}" > "${h%/*}/bind"
	n=0; while [ $n -lt 20 ] && [ -z "$(ls /sys/bus/sdio/devices 2>/dev/null)" ]; do sleep 1; n=$((n+1)); done
	log "wifi: no wlan0; the chip powered off and on (reset $((tries + 1))), its card back after ${n}s"
}

# t5_ip: the current IPv4 address on wlan0, empty if none.
t5_ip() { ip -4 addr show wlan0 2>/dev/null | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -1; }

# t5_wifi_joined: whether the supplicant is associated. An address alone does not say so: a lease
# stays on wlan0 after the link is lost, until it runs out.
t5_wifi_joined() { wpa_cli -p /run/wpa -i wlan0 status 2>/dev/null | grep -q '^wpa_state=COMPLETED'; }

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
#
# A pin outlives the channel it was made for: a router on automatic channels moves its 5 GHz radio
# (radar on a DFS channel, a channel scan), and a supplicant pinned to the old channel never finds it
# again (#111). So once the link has been gone at two checks in a row, the pin comes off and the
# supplicant looks on every band again. A link on another channel than the pin's is one the pin no
# longer holds: a network joined or forgotten since reloads the supplicant without it.
t5_wifi_prefer5() {
	w="wpa_cli -p /run/wpa -i wlan0"
	pin=/run/techo5/prefer5-pin
	if [ -s $pin ]; then
		if t5_wifi_joined; then
			rm -f $pin.miss
			on=$(iw dev wlan0 link 2>/dev/null | sed -n 's/.*freq: \([0-9]*\).*/\1/p')
			# Cleared on its network as well, in case the supplicant went to another saved network
			# on its own, with no reload: the pinned one would keep only the old channel until a
			# reboot. Clearing it on the network in use does not drop the link.
			if [ -n "$on" ] && [ "${on%%.*}" != "$(cut -d' ' -f2 $pin)" ]; then
				$w set_network "$(cut -d' ' -f1 $pin)" freq_list "" >/dev/null 2>&1
				rm -f $pin
			fi
		elif [ -e $pin.miss ]; then
			log "wifi: not associated while pinned to $(cut -d' ' -f2 $pin) MHz; back to every band"
			$w set_network "$(cut -d' ' -f1 $pin)" freq_list "" >/dev/null 2>&1
			$w reassociate >/dev/null 2>&1
			rm -f $pin $pin.miss
			return 0
		else
			: > $pin.miss
		fi
	fi
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
		if [ "${f%%.*}" = "$target" ] && t5_wifi_joined; then
			log "wifi: on $target MHz"
			echo "$id $target" > $pin
			rm -f $pin.miss
			return 0
		fi
	done
	log "wifi: no association on $target MHz in 30 s; back to every band for 30 minutes"
	$w set_network "$id" freq_list "" >/dev/null 2>&1
	$w reassociate >/dev/null 2>&1
	rm -f $pin $pin.miss
	echo $((now + 1800)) > /run/techo5/prefer5-after
}

# t5_ntp_peers: the time servers, as ntpd's -p arguments. Several: busybox ntpd
# takes one address for each name, so pool.ntp.org alone is one pool member, and
# one that never answers left the clock at 2010 until the next reboot (#77).
# The home's own time server, set on the setup page (echod feature/timezone), is asked first, then
# NTP_SERVER from device.conf. The file holds one name or address, checked when it was saved.
t5_ntp_peers() {
	t5_own_ntp=$(head -n 1 /data/misc/techo5/ntp-server 2>/dev/null | tr -cd 'A-Za-z0-9.:-')
	for s in $t5_own_ntp $NTP_SERVER time.cloudflare.com time.google.com 0.pool.ntp.org 1.pool.ntp.org 2.pool.ntp.org; do
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
	# Tells echod NTP has set the clock, so Home Assistant's time leaves it alone (feature/timezone).
	: > /run/techo5/ntp-set 2>/dev/null
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

# The factory Bluetooth address from idme, as 12 hex digits, or nothing. Amazon's kernels serve idme
# as /proc/idme. athena's mainline kernel has no idme driver, so there it is read off the eMMC's
# second boot area, read only (T5_IDME_DEV, for tests). The layout is the one echod reads
# (internal/layout/athena_mainline.go parseIdme): "beefdeed", the item count as a 32-bit word at
# 0x0c, then from 0x10 one item after another: a 16-byte name, the value's size and two more 32-bit
# words, then the value, NUL padded.
t5_bt_idme_addr() { t5_idme_mac bt_mac_addr; }

# t5_idme_mac <field>: a 12-hex-digit address from idme, from /proc/idme on Amazon's kernels or read
# off the eMMC's second boot partition where /proc/idme is missing (athena on mainline), or nothing.
# Only the value's hex digits count, as t5_wifi_mac always took them: whatever else idme pads or
# separates it with (NULs, a newline, colons) is dropped before twelve digits are required.
t5_idme_mac() {
	field=$1
	if [ -r "/proc/idme/$field" ]; then
		v=$(tr -dc '0-9A-Fa-f' < "/proc/idme/$field")
	else
		dev=${T5_IDME_DEV:-/dev/mmcblk0boot1}
		v=
		[ -r "$dev" ] && [ "$(dd if="$dev" bs=8 count=1 2>/dev/null | tr -d '\0')" = beefdeed ] || return 0
		n=$(od -A n -t u4 -j 12 -N 4 "$dev" | tr -d ' ')
		off=16; i=0
		while [ "$i" -lt "${n:-0}" ] && [ "$i" -lt 256 ]; do
			name=$(dd if="$dev" bs=1 skip=$off count=16 2>/dev/null | tr -d '\0')
			size=$(od -A n -t u4 -j $((off + 16)) -N 4 "$dev" | tr -d ' ')
			case "$size" in ''|*[!0-9]*) return 0;; esac
			[ "$size" -le 4096 ] || return 0
			if [ "$name" = "$field" ]; then
				v=$(dd if="$dev" bs=1 skip=$((off + 28)) count="$size" 2>/dev/null | tr -dc '0-9A-Fa-f')
				break
			fi
			off=$((off + 28 + size)); i=$((i + 1))
		done
	fi
	case "$v" in
	[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F])
		echo "$v";;
	esac
}

# Bluetooth from the kernel's own driver (BT_MODULE=builtin, device.conf: athena on mainline, whose
# btmtksdio registers hci0 itself): no vendor module, no btbridge, no vhci. What btbridge did on the
# way through is done here instead: the factory address goes to the controller through the kernel
# (mgmt Set Public Address; btmtksdio passes it on with MediaTek's 0xFC1A command), before bluetoothd
# starts, since the kernel takes a new address only while the controller is off.
t5_bt_native() {
	n=0; while [ $n -lt 20 ] && [ ! -d /sys/class/bluetooth/hci0 ]; do sleep 1; n=$((n+1)); done
	[ -d /sys/class/bluetooth/hci0 ] || { log "bt: no hci0 from the kernel's driver"; return 1; }
	command -v btmgmt >/dev/null || { log "bt: no btmgmt"; return 1; }
	a=$(t5_bt_idme_addr | sed 's/../&:/g; s/:$//' | tr a-f A-F)
	[ -n "$a" ] || { log "bt: no factory address in idme; keeping the controller's own"; return 0; }
	# The driver's first setup can still be running when hci0 appears (athena's MT7663 registers
	# 8 s after boot, after Wi-Fi), and mgmt opened during it left hci0 out of mgmt's index list for
	# good (unit 1, 2026-10-15). So nothing talks mgmt for 3 s after hci0 appears; if mgmt still does
	# not list it, the driver is bound again once, which lists it (seen on the unit).
	sleep 3
	t5_bt_listed() { n=0; while [ $n -lt 10 ]; do timeout 3 btmgmt info 2>/dev/null | grep -q '^hci0:' && return 0; sleep 1; n=$((n+1)); done; return 1; }
	if ! t5_bt_listed; then
		f=$(basename "$(readlink -f /sys/class/bluetooth/hci0/device 2>/dev/null)")
		d=$(readlink -f "/sys/bus/sdio/devices/$f/driver" 2>/dev/null)
		if [ -n "$f" ] && [ -d "$d" ]; then
			log "bt: hci0 not listed by mgmt; binding $f again"
			echo "$f" > "$d/unbind"; sleep 2; echo "$f" > "$d/bind"
			n=0; while [ $n -lt 20 ] && [ ! -d /sys/class/bluetooth/hci0 ]; do sleep 1; n=$((n+1)); done
			sleep 3
			t5_bt_listed || log "bt: hci0 still not listed by mgmt"
		fi
	fi
	# The kernel powers hci0 up once to set it up and off again two seconds later; until then
	# the index is busy or powered, and the address is refused. Tried for up to 15 seconds.
	n=0
	while [ $n -lt 15 ]; do
		cur=$(timeout 3 btmgmt -i hci0 info 2>/dev/null | awk '$1 == "addr" { print $2; exit }')
		[ "$cur" = "$a" ] && { log "bt: factory address in place"; return 0; }
		timeout 3 btmgmt -i hci0 power off >/dev/null 2>&1
		timeout 5 btmgmt -i hci0 public-addr "$a" >/dev/null 2>&1
		# The kernel programs a new address on the next power-on; without one the check above
		# only passed when something else happened to power hci0 up in time.
		timeout 5 btmgmt -i hci0 power on >/dev/null 2>&1
		sleep 1; n=$((n+1))
	done
	log "bt: the factory address was not taken; keeping the controller's own"
	return 0
}

# Bluetooth: the vendor driver gives a raw H4 channel (/dev/stpbt); btbridge
# turns it into hci0 through the kernel's vhci driver; BlueZ and bluez-alsa
# sit on top. Needs a kernel with CONFIG_BT + CONFIG_BT_HCIVHCI — without
# /dev/vhci this quietly does nothing, so an older boot image keeps working.
# With BT_MODULE=builtin hci0 comes from the kernel's own driver instead (t5_bt_native).
t5_bt_up() {
	mod=$1; logdir=${2:-/tmp}
	# builtin, or a kernel whose own driver already made hci0 (btmtksdio on the cronos mainline build).
	if [ "$mod" = builtin ] || [ -d /sys/class/bluetooth/hci0 ]; then
		t5_bt_native || return 1
	else
		t5_bt_bridge "$mod" "$logdir" || return 1
	fi
	t5_bt_stack "$logdir"
}

# hci0 from the vendor driver through btbridge and vhci (t5_bt_up).
t5_bt_bridge() {
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
		# After each restart of the bridge, t5_bt_again, as on the Shows below.
		(while true; do
			btbridge -uart "$BT_UART" ${BT_HCD:+-hcd "$BT_HCD"} ${BT_BAUD:+-baud "$BT_BAUD"} ${addr:+-bdaddr "$addr"} >> "$logdir/btbridge.log" 2>&1
			sleep 2
			t5_bt_again &
		done) &
	else
		# -p192-keys: a phone's link key reaches the kernel as the kind the controller's E0 encryption
		# satisfies, so a phone can stay connected to play to the Show (#105; btbridge h4.go p192Key).
		# After each restart of the bridge, t5_bt_again puts the new hci0 right.
		(while true; do
			btbridge -p192-keys ${addr:+-bdaddr "$addr"} >> "$logdir/btbridge.log" 2>&1
			sleep 2
			t5_bt_again &
		done) &
	fi
	n=0; while [ $n -lt 20 ] && [ ! -d /sys/class/bluetooth/hci0 ]; do sleep 1; n=$((n+1)); done
	[ -d /sys/class/bluetooth/hci0 ] || { log "bt: bridge up but no hci0"; return 1; }
}

# BlueZ and bluez-alsa on hci0, however it came up (t5_bt_up).
t5_bt_stack() {
	logdir=${1:-/tmp}
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
	# Only what audio needs, as on the Dot: no HID or HID over GATT (a paired device could otherwise
	# present itself as a keyboard), no PAN networking, no SIM access.
	[ -x "$bd" ] && "$bd" -n --noplugin=input,hog,network,sap >> "$logdir/bluetoothd.log" 2>&1 &
	# Seen on the bench: after the first power-on the controller answers commands but never
	# reports an inquiry result or an advertisement until it has been powered off and on once.
	n=0; while [ $n -lt 10 ] && ! timeout 3 btmgmt info 2>/dev/null | grep -q "current settings: powered"; do sleep 1; n=$((n+1)); done
	timeout 5 btmgmt power off >/dev/null 2>&1; sleep 1; timeout 5 btmgmt power on >/dev/null 2>&1
	# BT_SC=off (device.conf: athena on mainline): no Secure Connections on BR/EDR, so a phone pairs
	# with a P-192 key. It does what btbridge's -p192-keys does on the other Shows (#105), without a
	# bridge to rewrite the key on its way: a MediaTek controller that makes a P-256 key but encrypts
	# with E0 has the kernel drop the phone ("Missing AES-CCM usage"). After bluetoothd has set the
	# controller up, which turns it on; the kernel keeps it across power cycles.
	[ "${BT_SC:-}" = off ] && timeout 5 btmgmt sc off >/dev/null 2>&1
	# bluez-alsa registers its A2DP endpoints with bluetoothd, so it must come after it. A source, to
	# play to earbuds and speakers, and a sink, so a phone can play to the device, which then says it
	# is a loudspeaker (major 4, audio/video; minor 5, loudspeaker). btmgmt takes the minor as the
	# class byte, shifted left two bits: 5 is 20. Given 5 it refused (Invalid Parameters), and the
	# Shows went out as miscellaneous devices. The Spot's Broadcom took a phone the same way, without
	# the Shows' -p192-keys (a Pixel played to it on a unit 2026-10-14). bluetoothd's main.conf says
	# loudspeaker too (mkrootfs.sh), so each new hci0 comes up as one before anything here runs.
	command -v bluealsa >/dev/null && bluealsa -p a2dp-source -p a2dp-sink >> "$logdir/bluealsa.log" 2>&1 &
	# Tried until it takes: written once, it could meet the kernel still adding bluez-alsa's services
	# (Busy), and a phone that paired then remembers the device as having no type (#105). In the
	# background, since five tries that time out are half a minute the boot would otherwise wait.
	(n=1
	while ! t5_bt_class; do
		[ $n -lt 5 ] || { log "bt: the loudspeaker class did not take"; break; }
		sleep 1; n=$((n+1))
	done
	log "bt: hci0 up; bluetoothd and bluealsa started; class $(timeout 3 btmgmt info 2>/dev/null | sed -n 's/.*class \(0x[0-9a-f]*\).*/\1/p' | head -1)") &
	return 0
}

# t5_bt_again: a bridge that restarted brings up a new hci0 with bluetoothd's saved settings. The class
# goes back to bluetoothd's default, so it is set again, and tried until it takes: the Spot's bridge
# loads a firmware patch first, which can outlast a fixed wait. And it is made unfindable: a pairing
# window open when the bridge went down was ended meanwhile by the daemon's timer, and the "not
# findable" that ended it reached no controller. A window still open loses its findability early,
# which a window can lose; staying findable with none open is what must not happen. Both are written
# twice, 5 s apart, since bluetoothd puts its own back as the adapter registers. Only the newest call
# carries on: a bridge failing over and over restarts every 2 s, and the hci0 that matters is the
# last one. A count in /run says which is newest, with no lock to be left behind.
t5_bt_again() {
	g=$(( $(cat /run/techo5-bt-again 2>/dev/null || echo 0) + 1 ))
	echo $g > /run/techo5-bt-again
	# Busy while this runs: the daemon leaves the controller alone until it is gone (btaudio
	# secureconn.go), since a power cycle for Secure Connections in the middle would undo it.
	echo $g > /run/techo5-bt-again.busy
	t5_bt_again_pass $g
	# A newer pass has written its own number in it: that one removes it.
	[ "$(cat /run/techo5-bt-again.busy 2>/dev/null)" = "$g" ] && rm -f /run/techo5-bt-again.busy
	return 0
}

# t5_bt_again_pass: t5_bt_again's work for generation $1, which a newer generation cuts short.
t5_bt_again_pass() {
	g=$1
	sleep 5
	n=1
	while :; do
		[ "$(cat /run/techo5-bt-again 2>/dev/null)" = "$g" ] || return 0
		t5_bt_class && break
		[ $n -lt 12 ] || break
		sleep 5
		n=$((n+1))
	done
	timeout 5 btmgmt discov off >/dev/null 2>&1
	sleep 5
	[ "$(cat /run/techo5-bt-again 2>/dev/null)" = "$g" ] || return 0
	t5_bt_class
	timeout 5 btmgmt discov off >/dev/null 2>&1
}

# t5_bt_class: say the device is a loudspeaker (major 4, audio/video; minor 5, given to btmgmt as the
# class byte: 20), and report whether the controller took it.
t5_bt_class() { timeout 5 btmgmt class 4 20 2>/dev/null | grep -q succeeded; }
