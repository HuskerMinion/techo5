package wifi

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
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
