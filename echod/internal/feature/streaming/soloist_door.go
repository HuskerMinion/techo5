package streaming

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Soloist chooses a fresh Connect HTTP port. Only wildcard IPv4 listeners owned by its actual
// process are admitted on Wi-Fi. The loopback WebSocket and unrelated listeners never match.
func soloistListeningPorts(pid int, proc string) []int {
	inodes := map[string]bool{}
	fds, _ := os.ReadDir(filepath.Join(proc, strconv.Itoa(pid), "fd"))
	for _, fd := range fds {
		link, _ := os.Readlink(filepath.Join(proc, strconv.Itoa(pid), "fd", fd.Name()))
		if strings.HasPrefix(link, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] = true
		}
	}
	b, _ := os.ReadFile(filepath.Join(proc, "net/tcp"))
	var ports []int
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != "0A" || !inodes[f[9]] {
			continue
		}
		addr, port, _ := strings.Cut(f[1], ":")
		if addr != "00000000" {
			continue
		}
		p, err := strconv.ParseUint(port, 16, 16)
		if err == nil && p > 1023 {
			ports = append(ports, int(p))
		}
	}
	return ports
}

const soloistChain = "TECHO5-SOLOIST"

func soloistDoorRule(port int, open bool) {
	rule := []string{soloistChain, "-p", "tcp", "--dport", strconv.Itoa(port), "-j", "ACCEPT"}
	if open {
		if iptables("iptables-legacy", append([]string{"-C"}, rule...)...) != nil {
			_ = iptables("iptables-legacy", append([]string{"-A"}, rule...)...)
		}
	} else {
		_ = iptables("iptables-legacy", append([]string{"-D"}, rule...)...)
	}
}

func watchSoloistDoor(ctx context.Context) {
	// A dedicated chain allows crash leftovers to be cleared without touching other allowlists.
	_ = iptables("iptables-legacy", "-N", soloistChain)
	_ = iptables("iptables-legacy", "-F", soloistChain)
	jump := []string{"INPUT", "-i", "wlan0", "-j", soloistChain}
	if iptables("iptables-legacy", append([]string{"-C"}, jump...)...) != nil {
		_ = iptables("iptables-legacy", []string{"-I", "INPUT", "1", "-i", "wlan0", "-j", soloistChain}...)
	}
	defer func() {
		_ = iptables("iptables-legacy", "-F", soloistChain)
		_ = iptables("iptables-legacy", append([]string{"-D"}, jump...)...)
	}()
	known := map[int]bool{}
	for ctx.Err() == nil {
		now := map[int]bool{}
		b, _ := os.ReadFile(filepath.Join(soloistData(), "soloist.pid"))
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil && pid > 1 {
			// The saved pid alone is not trusted: require our actual private loader executable.
			exe, _ := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
			if exe == filepath.Join(soloistHome(), "data/soloistd/bin/soloist") {
				for _, port := range soloistListeningPorts(pid, "/proc") {
					now[port] = true
				}
			}
		}
		for port := range now {
			soloistDoorRule(port, true)
		} // re-add if the boot firewall was rebuilt.
		for port := range known {
			if !now[port] {
				soloistDoorRule(port, false)
			}
		}
		known = now
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
