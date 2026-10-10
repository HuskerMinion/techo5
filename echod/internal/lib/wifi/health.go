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
		if len(f) < 3 || f[0] != iface || f[1] != "00000000" {
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

// GroupPackets is how many packets addressed to a group (multicast or broadcast, IPv4 and IPv6) the
// device has taken in and sent out since it booted, from the IP stack's own counters. The Wi-Fi
// driver's multicast count stays at 0 on the Echo Dot, so the IP stack's is the one to read. A copy of
// what the device sends to a group loops back and is counted as taken in too, which is why the caller
// gets both: only taking in more than it sent shows that the network itself is still heard. ok is
// false when neither file can be read.
func GroupPackets() (in, out uint64, ok bool) {
	if b, err := os.ReadFile("/proc/net/netstat"); err == nil {
		if i, o, found := groupIn4(string(b)); found {
			in, out, ok = in+i, out+o, true
		}
	}
	if b, err := os.ReadFile("/proc/net/snmp6"); err == nil {
		if i, o, found := groupIn6(string(b)); found {
			in, out, ok = in+i, out+o, true
		}
	}
	return in, out, ok
}

// groupIn4 reads the IpExt lines of /proc/net/netstat: a line of names, then a line of values.
func groupIn4(netstat string) (in, out uint64, ok bool) {
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
				in, ok = in+v, true
			case "OutMcastPkts", "OutBcastPkts":
				out += v
			}
		}
		break
	}
	return in, out, ok
}

// groupIn6 reads /proc/net/snmp6: one counter per line, the name then the value.
func groupIn6(snmp6 string) (in, out uint64, ok bool) {
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
			in, ok = v, true
		case "Ip6OutMcastPkts":
			out = v
		}
	}
	return in, out, ok
}
