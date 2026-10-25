#!/bin/sh
# Runs as root only to set the firewall: the browser may reach the internet and the DNS server, never
# this house's network, the shared 100.64.0.0/10 range, the machine it runs on, or the ranges in
# READER_BLOCK. Then the server runs as pwuser, which cannot change the rules.
#
# READER_BLOCK: more IPv4 addresses or ranges to keep the browser off, separated by spaces or commas,
#   such as the house's own public address (the router's admin page can answer on it).
# READER_BLOCK_PUBLIC_IP=1: also look up this network's public address at start and keep the browser
#   off it. This asks an outside service (READER_PUBLIC_IP_URL, https://api.ipify.org by default),
#   so it is off unless set, and a changed address is only picked up when the container restarts.
set -e
ns=$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)
iptables -F OUTPUT
iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

block=$(echo "${READER_BLOCK:-}" | tr ',' ' ')
if [ "${READER_BLOCK_PUBLIC_IP:-}" = 1 ]; then
	pub=$(node -e '
		fetch(process.argv[1], { signal: AbortSignal.timeout(10000) })
			.then((r) => r.text())
			.then((t) => {
				t = t.trim();
				if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(t)) throw new Error("not an IPv4 address: " + t.slice(0, 40));
				console.log(t);
			})
			.catch((e) => { console.error(e.message); process.exit(1); });
	' "${READER_PUBLIC_IP_URL:-https://api.ipify.org}") || {
		echo "could not look up the public address; not starting" >&2
		exit 1
	}
	echo "keeping the browser off the public address $pub"
	block="$block $pub"
fi
# The block list goes first, ahead of the DNS rules, so nothing on it is let through.
for n in $block; do
	iptables -A OUTPUT -d "$n" -j REJECT
done

if [ "$ns" = 127.0.0.11 ]; then
	# Docker's own DNS on a compose or user-made network: the container's nat table moves port 53 to a
	# port of Docker's choosing before these rules see it, so the address is let through on any port.
	# Nothing else listens on it.
	iptables -A OUTPUT -d 127.0.0.11 -j ACCEPT
elif [ -n "$ns" ]; then
	iptables -A OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT
	iptables -A OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT
fi
for n in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 \
	192.168.0.0/16 198.18.0.0/15 224.0.0.0/4 240.0.0.0/4; do
	iptables -A OUTPUT -d "$n" -j REJECT
done

# IPv6 is meant to be off (--sysctl net.ipv6.conf.all.disable_ipv6=1). If it is on, its traffic must be
# dropped, or the browser could reach the house over IPv6; if that cannot be done, do not start.
if ! ip6tables -P OUTPUT DROP 2>/dev/null || ! ip6tables -F OUTPUT 2>/dev/null; then
	if [ "$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null)" != 1 ] && [ -e /proc/net/if_inet6 ]; then
		echo "IPv6 is on and its firewall could not be set; not starting (run with --sysctl net.ipv6.conf.all.disable_ipv6=1)" >&2
		exit 1
	fi
fi
exec setpriv --reuid=pwuser --regid=pwuser --init-groups --inh-caps=-all --bounding-set=-all node /app/server.js
