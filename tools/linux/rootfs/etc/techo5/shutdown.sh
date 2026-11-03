#!/bin/sh
# TECHO5 root filesystem: shutdown (see /etc/inittab). A clean stop for the
# daemon first — it gates the amplifier before letting go of the speaker, and
# a stop on purpose is not a failed trial — then everything else.
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# left: whether a process killall5 signals still runs: not init, not a kernel thread, not a zombie,
# and not in this script's own session (init starts it in one of its own, and killall5 spares it).
left() {
	self=$(cut -d')' -f2- /proc/self/stat | cut -d' ' -f5)
	for f in /proc/[0-9]*/stat; do
		read -r l < "$f" 2>/dev/null || continue
		p=${l%% *}; set -- ${l##*) }
		case "$p:$1:$2:$4" in 1:* | 2:* | *:Z:* | *:*:2:* | *:*:*:"$self") continue;; esac
		return 0
	done
	return 1
}

# A reboot through init is one on purpose, for the next boot's reason (boot.sh), unless whoever asked
# for it left a more exact one.
[ -s /data/techo5-linux/restart-reason ] || echo restart > /data/techo5-linux/restart-reason
pid=$(pidof techo5)
if [ -n "$pid" ]; then
	kill -TERM $pid
	n=0; while [ $n -lt 10 ] && pidof techo5 >/dev/null; do sleep 1; n=$((n+1)); done
fi
killall dropbear ntpd udhcpc wpa_supplicant fbprobe 2>/dev/null
cp /run/boot.log /data/techo5-linux/boot.log 2>/dev/null
sync
# Whatever still runs (a board's helpers, a shell) holds files open, and umount -a then only
# remounts those filesystems read-only: a FAT one keeps its dirty bit and is "not properly
# unmounted" at the next boot.
# A second at most for them to go, ended as soon as none is left, so a normal shutdown waits for
# nothing.
killall5 -TERM
n=0; while [ $n -lt 10 ] && left; do sleep 0.1; n=$((n+1)); done
killall5 -KILL
umount -a -r 2>/dev/null
sync
