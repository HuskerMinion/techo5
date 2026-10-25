// Package localname looks up .local names, which a device on a TECHO5 root filesystem cannot do
// otherwise: musl's resolver and Go's own ask the DNS servers, and a home's DNS knows nothing of
// homeassistant.local. The name is asked for over multicast DNS instead, the way a phone or a laptop
// finds it, and what comes back is written into the hosts file, which every program on the device
// reads - the daemon, ffmpeg playing Home Assistant's text to speech, the media players.
package localname

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// group is where a multicast DNS question goes and its answers come back.
var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// listen opens the socket a question is asked from and its answers heard on: the multicast DNS port
// itself, shared with the device's own advertiser (Go sets SO_REUSEADDR on it). A question from a
// port of its own would be answered straight back to that port by the responder's own address, and
// the device's firewall, which lets 5353 in for exactly this, turns such an answer away. A variable
// so a test can answer on loopback.
var listen = func() (*net.UDPConn, *net.UDPAddr, error) {
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	return conn, group, err
}

// IsLocal is whether host is a .local name: one only multicast DNS can find.
func IsLocal(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	return strings.HasSuffix(h, ".local") && net.ParseIP(h) == nil
}

// interfaceAddrs are the device's own addresses, with their networks. A variable for the tests.
var interfaceAddrs = net.InterfaceAddrs

// ownNetworks are the IPv4 networks the device is on.
func ownNetworks() []*net.IPNet {
	addrs, _ := interfaceAddrs()
	var out []*net.IPNet
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			out = append(out, n)
		}
	}
	return out
}

// OnOwnNetwork is whether ip is on one of the networks the device itself is on.
func OnOwnNetwork(ip net.IP) bool {
	for _, n := range ownNetworks() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// usable is whether an answer can be a host on the network: not loopback, unspecified, multicast,
// link-local or a broadcast address, the whole network's or one of the device's own networks'. A
// responder that answers with one of those is wrong, and writing it would only send Home Assistant's
// traffic nowhere.
func usable(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil || ip4.IsLoopback() || ip4.IsUnspecified() || ip4.IsMulticast() || ip4.IsLinkLocalUnicast() ||
		ip4.Equal(net.IPv4bcast) {
		return false
	}
	for _, n := range ownNetworks() {
		if ones, bits := n.Mask.Size(); bits == 32 && ones < 31 && n.Contains(ip4) {
			last := make(net.IP, 4)
			for i := range last {
				last[i] = n.IP.To4()[i] | ^n.Mask[i]
			}
			if ip4.Equal(last) {
				return false
			}
		}
	}
	return true
}

// Resolve asks the network for host's IPv4 address over multicast DNS: the first of ResolveAll.
func Resolve(ctx context.Context, host string) (net.IP, error) {
	ips, err := ResolveAll(ctx, host)
	if err != nil {
		return nil, err
	}
	return ips[0], nil
}

// ResolveAll asks the network for host's IPv4 addresses over multicast DNS, and gives every usable one
// in the first answer that has any, those on one of the device's own networks first. It finds a name
// on this network only: a router passes multicast DNS from another one only if it is set to.
func ResolveAll(ctx context.Context, host string) ([]net.IP, error) {
	name := dns.Fqdn(strings.ToLower(host))
	conn, to, err := listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	q := new(dns.Msg)
	q.SetQuestion(name, dns.TypeA)
	q.RecursionDesired = false
	out, err := q.Pack()
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	// Asked twice, a second apart, as multicast loses things: the first answer ends the wait.
	if _, err := conn.WriteToUDP(out, to); err != nil {
		return nil, err
	}
	again := time.Now().Add(time.Second)
	buf := make([]byte, 9000)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_ = conn.SetReadDeadline(minTime(deadline, again))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && time.Now().Before(deadline) {
				if !again.IsZero() {
					_, _ = conn.WriteToUDP(out, to)
					again = time.Time{}
				}
				continue
			}
			return nil, fmt.Errorf("localname: no answer for %s", host)
		}
		var m dns.Msg
		if m.Unpack(buf[:n]) != nil || !m.Response {
			continue // the questions on the group, its own among them
		}
		var mine, other []net.IP
		for _, rr := range append(m.Answer, m.Extra...) {
			a, ok := rr.(*dns.A)
			if !ok || !strings.EqualFold(a.Hdr.Name, name) ||
				slices.ContainsFunc(mine, a.A.Equal) || slices.ContainsFunc(other, a.A.Equal) {
				continue
			}
			switch {
			case !usable(a.A):
			case OnOwnNetwork(a.A):
				mine = append(mine, a.A)
			default:
				other = append(other, a.A)
			}
		}
		if len(mine)+len(other) > 0 {
			return append(mine, other...), nil
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

// base is what the hosts file always holds, as boot.sh writes it.
const base = "127.0.0.1\tlocalhost localhost.localdomain\n::1\t\tlocalhost localhost.localdomain\n"

// WriteHosts replaces the hosts file at path with localhost and names, each name's address beside it.
// Through a file next to it and a rename, so a program reading it never sees half of one.
//
// A name is written in lowercase and, when it was given otherwise, as it was given too: musl finds a
// name in the hosts file only as it is asked for, capitals and all, and a program asks for the name
// in the address it was given.
func WriteHosts(path string, names map[string]net.IP) error {
	var b strings.Builder
	b.WriteString(base)
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		given := strings.TrimSuffix(k, ".")
		line := strings.ToLower(given)
		if given != line {
			line += " " + given
		}
		fmt.Fprintf(&b, "%s\t%s\n", names[k], line)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hosts-")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
