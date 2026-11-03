package wifi

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Reassociate has the supplicant drop and rejoin the network it is on, which renews every key. It is
// the cure for a radio that is still joined but has stopped hearing the network's broadcasts.
func Reassociate(ctx context.Context) error {
	_, err := cli(ctx, "reassociate")
	return err
}

// Gateway is the default route's next hop on the Wi-Fi interface, nil without one.
func Gateway() net.IP {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil
	}
	return gatewayIn(string(b))
}

// gatewayIn reads /proc/net/route: whitespace-separated columns, the destination and the gateway in
// hex, in the kernel's own byte order, which is little-endian on every device this runs on.
func gatewayIn(table string) net.IP {
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != iface() || f[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, binary.LittleEndian.Uint32(raw))
		if ip.IsUnspecified() {
			continue
		}
		return ip
	}
	return nil
}

// Answers reports whether ip replies to a ping. It is asked about the gateway, which already knows
// this device's address, so a reply means unicast still works both ways, whatever broadcasts are doing.
func Answers(ctx context.Context, ip net.IP) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", ip.String()).Run() == nil
}

// Group is the IP stack's count of packets addressed to a group (multicast or broadcast, IPv4 and
// IPv6) since the device booted.
type Group struct {
	// In and Out are what was taken in and sent out. A copy of what the device sends to a group loops
	// back and is counted as taken in too, so only taking in more than was sent shows the network
	// itself was heard; and not even that on a quiet network, where the router's membership queries
	// and the device's answers to them (which do not loop back) cancel out.
	In, Out uint64

	// Router is the router adverts and multicast listener queries taken in. Only a router sends these,
	// never the device itself, so any rise is the network heard.
	Router uint64

	// From is which files the counts were read from, FromIPv4 and FromIPv6: two readings from different
	// files cannot be compared, since the one that was missed would read as a jump.
	From uint8
}

// The files a Group is read from.
const (
	FromIPv4 uint8 = 1 << iota
	FromIPv6
)

// GroupPackets is the IP stack's group counters. The Wi-Fi driver's multicast count stays at 0 on the
// Echo Dot, so the IP stack's are the ones to read. ok is false when neither file can be read.
func GroupPackets() (g Group, ok bool) {
	if b, err := os.ReadFile("/proc/net/netstat"); err == nil {
		if g4, found := group4(string(b)); found {
			g.In, g.Out, g.From, ok = g.In+g4.In, g.Out+g4.Out, g.From|FromIPv4, true
		}
	}
	if b, err := os.ReadFile("/proc/net/snmp6"); err == nil {
		if g6, found := group6(string(b)); found {
			g.In, g.Out, g.Router, g.From, ok = g.In+g6.In, g.Out+g6.Out, g.Router+g6.Router, g.From|FromIPv6, true
		}
	}
	return g, ok
}

// group4 reads the IpExt lines of /proc/net/netstat: a line of names, then a line of values.
func group4(netstat string) (g Group, ok bool) {
	var names []string
	for _, line := range strings.Split(netstat, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "IpExt:" {
			continue
		}
		if names == nil {
			names = f
			continue
		}
		for i := 1; i < len(f) && i < len(names); i++ {
			v, err := strconv.ParseUint(f[i], 10, 64)
			if err != nil {
				continue
			}
			switch names[i] {
			case "InMcastPkts", "InBcastPkts":
				g.In, ok = g.In+v, true
			case "OutMcastPkts", "OutBcastPkts":
				g.Out += v
			}
		}
		break
	}
	return g, ok
}

// group6 reads /proc/net/snmp6: one counter per line, the name then the value.
func group6(snmp6 string) (g Group, ok bool) {
	for _, line := range strings.Split(snmp6, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "Ip6InMcastPkts":
			g.In, ok = v, true
		case "Ip6OutMcastPkts":
			g.Out = v
		case "Icmp6InRouterAdvertisements", "Icmp6InGroupMembQueries":
			g.Router += v
		}
	}
	return g, ok
}
