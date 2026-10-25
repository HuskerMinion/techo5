#!/bin/sh
# Run by the long-running ntpd (boot.sh, -S) on its events. When it steps the clock or reaches a
# server, the RTC is written too, in UTC as the kernel reads it, and echod is told NTP has the clock,
# so Home Assistant's time leaves it alone (feature/timezone).
case "$1" in
step|stratum)
	hwclock -w -u 2>/dev/null
	: > /run/techo5/ntp-set 2>/dev/null
	;;
esac
