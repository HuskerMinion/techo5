// Package peeruid says which user is at the other end of a TCP connection the device made with itself,
// over loopback: who a controller on 127.0.0.1 is.
//
// A loopback address once meant root: only somebody with a root shell could put a program on the device.
// Add-ons changed that (feature/addons): each runs as a user of its own, and so do the receivers and the
// video decoder. So a connection from loopback is trusted as the owner's only when the program that made
// it runs as root, or as an add-on the owner installed and agreed to (Trusted), and the kernel says
// which: its own table of TCP sockets (/proc/net/tcp, tcp6) lists each socket's owner, and the
// connection's other end is the socket whose local address is this one's remote and the other way round.
package peeruid

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Trusted is whether a user other than root is one whose loopback connections stand for the owner's:
// an installed add-on's (feature/addons sets it). Nobody, until then.
var Trusted = func(uid uint32) bool { return false }

// tables are the kernel's TCP socket tables; a variable, for tests.
var tables = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// ErrNotFound is a connection the tables do not list: gone already, or not over TCP on this device.
var ErrNotFound = errors.New("peeruid: no such connection")

// Of is the user id that owns the socket at the other end of a connection this process accepted, from
// that connection's remote and local addresses ("127.0.0.1:41234", "127.0.0.1:8181").
func Of(remote, local string) (uint32, error) {
	rs, err := keys(remote)
	if err != nil {
		return 0, err
	}
	ls, err := keys(local)
	if err != nil {
		return 0, err
	}
	for _, t := range tables {
		f, err := os.Open(t)
		if err != nil {
			continue
		}
		s := bufio.NewScanner(f)
		s.Scan() // the heading
		for s.Scan() {
			fl := strings.Fields(s.Text())
			// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode
			if len(fl) < 10 || !match(fl[1], rs) || !match(fl[2], ls) {
				continue
			}
			// Only a live, established socket says who holds it. One its program closed at once
			// after sending its request (FIN_WAIT2, a time-wait entry) is listed with uid 0 and
			// inode 0, and on kernels before 4.10 even one still closing reads as root: taken at
			// its word, that would let any program pass for root by hanging up quickly.
			if fl[3] != "01" || fl[9] == "0" {
				continue
			}
			uid, err := strconv.ParseUint(fl[7], 10, 32)
			f.Close()
			if err != nil {
				return 0, err
			}
			return uint32(uid), nil
		}
		f.Close()
	}
	return 0, ErrNotFound
}

// keys is an address as either table can list it: an IPv4 one as itself, and as the IPv6 address it is
// mapped to, which is how a socket of the IPv6 family (Go's listeners are) holds an IPv4 peer.
func keys(addr string) ([]string, error) {
	k, err := key(addr)
	if err != nil {
		return nil, err
	}
	out := []string{k}
	host, port, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		if m, err := key16(ip.To16(), port); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func match(field string, ks []string) bool {
	for _, k := range ks {
		if strings.EqualFold(field, k) {
			return true
		}
	}
	return false
}

// key is an address as the kernel's tables write it: the address as the words it is stored in, each in
// the machine's order (little-endian on these boards), and the port, in hex.
func key(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("peeruid: %q is not an address", host)
	}
	raw := ip.To4()
	if raw == nil {
		raw = ip.To16()
	}
	return key16(raw, port)
}

// key16 is key for the address's bytes as they are, four of them or sixteen.
func key16(raw net.IP, port string) (string, error) {
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", err
	}
	words := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.LittleEndian.PutUint32(words[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	return strings.ToUpper(hex.EncodeToString(words)) + ":" + fmt.Sprintf("%04X", p), nil
}

// Owner is whether the program at the other end of a loopback connection stands for the owner: it runs
// as root, or as a user Trusted says is an installed add-on's. A connection that is not from loopback,
// or whose owner cannot be read, is not.
func Owner(remote, local string) bool {
	host, _, err := net.SplitHostPort(remote)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return false
	}
	uid, err := Of(remote, local)
	if err != nil {
		return false
	}
	return uid == 0 || Trusted(uid)
}
