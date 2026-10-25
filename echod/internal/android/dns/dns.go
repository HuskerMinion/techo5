// Package dns teaches Go's resolver where this device keeps its nameservers.
//
// Android has no /etc/resolv.conf: bionic reads the nameservers from system properties, and echod is
// built without cgo so it gets Go's own resolver, which only knows about the file. Every lookup
// therefore goes to [::1]:53 and is refused. Nothing noticed for a long time because Home Assistant is
// reached by address and the wake word models come from Home Assistant — the first name anything had to
// resolve was a release manifest.
package dns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/prop"
)

// dhcpcd's hook sets the dhcp ones from the lease. Promoting them to net.dns is the framework's job,
// and there is no framework here.
var props = []string{
	"net.dns1", "net.dns2", "net.dns3", "net.dns4",
	"dhcp.wlan0.dns1", "dhcp.wlan0.dns2", "dhcp.wlan0.dns3", "dhcp.wlan0.dns4",
}

const (
	// timeout bounds one lookup, short enough that a nameserver which is not answering falls through to
	// the next rather than holding up whatever asked.
	timeout = 5 * time.Second

	// fresh is how long the nameservers are trusted before being read again. They come from DHCP and
	// change when the device joins another network, so reading them once would leave a moved device
	// resolving against a nameserver that is no longer there. Reading them every lookup is worse: each
	// one is a getprop, and whatever starts resolving in a loop later would be spawning processes to do
	// it. A minute is short against how often a device changes network and long against anything that
	// resolves in earnest.
	fresh = time.Minute
)

// Use makes every lookup in this process go to the nameservers the platform reports, by replacing the
// resolver the standard library shares. Nothing else has to be told: http.DefaultClient included.
func Use() {
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dial}
	slog.Info("resolver configured", "nameservers", nameservers())
}

// dial ignores the address Go derived from the resolv.conf it could not find and uses what the platform
// says, trying each in turn.
func dial(ctx context.Context, network, _ string) (net.Conn, error) {
	servers := nameservers()
	if len(servers) == 0 {
		return nil, fmt.Errorf("dns: the device reports no nameservers")
	}
	addrs := make([]string, len(servers))
	for i, s := range servers {
		addrs[i] = net.JoinHostPort(s, "53")
	}
	return dialAny(ctx, network, addrs, perServer)
}

// perServer is how long a nameserver that is not the last is waited on before the query goes to the
// next. A home router answers in milliseconds; this only has to be well short of the whole lookup's
// timeout so the next nameserver still gets most of it.
const perServer = 2 * time.Second

// dialAny connects to the first of addrs that will have it. Over UDP that is always the first, since
// a UDP dial sends nothing and cannot fail, so the connection it returns moves the query on to the next
// nameserver itself when one does not answer within wait (failover). Go's resolver dials once per
// attempt, so every lookup starts again at the first.
func dialAny(ctx context.Context, network string, addrs []string, wait time.Duration) (net.Conn, error) {
	if network == "udp" || network == "udp4" || network == "udp6" {
		f := &failover{ctx: ctx, network: network, addrs: addrs, wait: wait}
		if err := f.next(); err != nil {
			return nil, err
		}
		return f, nil
	}

	var err error
	for i, addr := range addrs {
		d := net.Dialer{Timeout: timeout}
		if i < len(addrs)-1 {
			d.Timeout = wait
		}

		var conn net.Conn
		if conn, err = d.DialContext(ctx, network, addr); err == nil {
			return conn, nil
		}
	}
	return nil, err
}

// failover is a UDP connection to the nameservers in turn. Go's resolver writes one query and reads
// until the answer to it arrives; a read that gets nothing from the current nameserver within wait, or
// an error from it (port unreachable), sends the same query to the next one and reads from that. The
// last nameserver gets whatever is left of the resolver's deadline. It is a PacketConn so the resolver
// speaks plain UDP over it rather than the stream framing it uses for TCP.
type failover struct {
	ctx     context.Context
	network string
	addrs   []string
	wait    time.Duration

	at       int // index into addrs of conn
	conn     net.Conn
	query    []byte
	deadline time.Time
}

// next connects to the next nameserver, sending it the query if one was already written.
func (f *failover) next() error {
	if f.conn != nil {
		f.conn.Close()
		f.conn = nil
		f.at++
	}
	err := errNoAnswer
	for ; f.at < len(f.addrs); f.at++ {
		var d net.Dialer
		var conn net.Conn
		if conn, err = d.DialContext(f.ctx, f.network, f.addrs[f.at]); err != nil {
			continue
		}
		conn.SetDeadline(f.deadline)
		if f.query != nil {
			if _, err = conn.Write(f.query); err != nil {
				conn.Close()
				continue
			}
		}
		f.conn = conn
		return nil
	}
	return err
}

var errNoAnswer = errors.New("dns: no nameserver answered")

func (f *failover) last() bool { return f.at >= len(f.addrs)-1 }

func (f *failover) Write(b []byte) (int, error) {
	if f.conn == nil {
		return 0, errNoAnswer
	}
	f.query = slices.Clone(b)
	return f.conn.Write(b)
}

func (f *failover) Read(b []byte) (int, error) {
	for {
		if f.conn == nil {
			return 0, errNoAnswer
		}
		d := f.deadline
		if !f.last() {
			if w := time.Now().Add(f.wait); d.IsZero() || w.Before(d) {
				d = w
			}
		}
		f.conn.SetReadDeadline(d)
		n, err := f.conn.Read(b)
		if err == nil || f.last() || f.query == nil {
			return n, err
		}
		if !f.deadline.IsZero() && !time.Now().Before(f.deadline) {
			return n, err
		}
		if err := f.next(); err != nil {
			return 0, err
		}
	}
}

func (f *failover) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := f.Read(b)
	return n, f.RemoteAddr(), err
}

func (f *failover) WriteTo(b []byte, _ net.Addr) (int, error) { return f.Write(b) }

func (f *failover) Close() error {
	if f.conn == nil {
		return nil
	}
	return f.conn.Close()
}

func (f *failover) LocalAddr() net.Addr {
	if f.conn == nil {
		return nil
	}
	return f.conn.LocalAddr()
}

func (f *failover) RemoteAddr() net.Addr {
	if f.conn == nil {
		return nil
	}
	return f.conn.RemoteAddr()
}

// The deadlines are the resolver's, for the whole exchange; Read narrows the read one per nameserver.
func (f *failover) SetDeadline(t time.Time) error {
	f.deadline = t
	if f.conn == nil {
		return nil
	}
	return f.conn.SetDeadline(t)
}

func (f *failover) SetReadDeadline(t time.Time) error {
	f.deadline = t
	return nil
}

func (f *failover) SetWriteDeadline(t time.Time) error {
	if f.conn == nil {
		return nil
	}
	return f.conn.SetWriteDeadline(t)
}

var (
	mu     sync.Mutex
	cached []string
	read   time.Time
)

func nameservers() []string {
	mu.Lock()
	defer mu.Unlock()

	if cached != nil && time.Since(read) < fresh {
		return cached
	}

	var out []string
	for _, name := range props {
		v, err := prop.Get(name)
		if err != nil || v == "" || slices.Contains(out, v) {
			continue
		}
		out = append(out, v)
	}

	// Newer Android keeps the nameservers inside netd rather than in properties, so a device on
	// LineageOS reports none this way. The default gateway is the next best guess: on a home network
	// it is the router, and the router resolves.
	if len(out) == 0 {
		if gw := gateway(); gw != "" {
			out = append(out, gw)
		}
	}

	cached, read = out, time.Now()
	return out
}

// gateway reads the default route from /proc/net/route, where the gateway is a little-endian hex
// address in the third column.
func gateway() string {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		var a, b, c, d byte
		if _, err := fmt.Sscanf(f[2], "%02x%02x%02x%02x", &d, &c, &b, &a); err != nil {
			continue
		}
		return net.IPv4(a, b, c, d).String()
	}
	return ""
}
