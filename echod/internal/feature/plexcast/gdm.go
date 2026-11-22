//go:build !dot

package plexcast

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// Plex's discovery, which is called GDM ("Good Day Mate") and is not SSDP: a controller sends a bare
// "M-SEARCH * HTTP/1.0" to the group, and every player answers it directly, from the very port the
// search came to, with a plain header block naming itself and its control port. A player also says it
// is here on its own (HELLO) when it starts and every so often after, so a controller that was not
// looking yet finds it without a search.
//
// Plex Web, Plex Desktop and Plex for Android TV find a player this way, and it is why casting works
// before an account is linked at all.
const (
	// gdmGroup is where searches go and where the player announces itself.
	gdmGroup = "239.0.0.250"

	// searchPort is where a controller sends its search, and where the answer must come from: a reply
	// from another port is dropped by some controllers (Plexamp on iOS among them).
	searchPort = 32412

	// helloPort is where a player says it is here.
	helloPort = 32413

	// helloEvery is how often it says so again, which covers a controller that started after the
	// device did.
	helloEvery = 30 * time.Second
)

// announce answers searches and says the device is here until ctx ends, then says goodbye. It is
// dlna's SSDP with Plex's words in it, including its politeness: only the home network is answered,
// and no more answers than a home can send searches.
func (f *Feature) announce(ctx context.Context) {
	group := netip.MustParseAddr(gdmGroup)
	var conn *net.UDPConn
	// Tried until it works: at boot the network may not be up yet, and a player that gave up then would
	// stay unfound until the account was unlinked and linked again.
	for waitFor := 2 * time.Second; ; waitFor = min(waitFor*2, time.Minute) {
		pc, err := net.ListenPacket("udp4", ":"+strconv.Itoa(searchPort))
		if err == nil {
			conn = pc.(*net.UDPConn)
			if err = ipv4.NewPacketConn(conn).JoinGroup(nil, &net.UDPAddr{IP: group.AsSlice()}); err == nil {
				break
			}
			conn.Close()
		}
		slog.Warn("plex: listening for searches failed", "err", err, "again in", waitFor)
		select {
		case <-ctx.Done():
			return
		case <-time.After(waitFor):
		}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	slog.Info("plex: answering searches", "name", friendlyName(), "port", Port)

	go func() {
		for {
			f.hello(group)
			select {
			case <-ctx.Done():
				f.bye(group)
				return
			case <-time.After(helloEvery):
			}
		}
	}()

	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("plex: reading searches failed", "err", err)
			}
			return
		}
		if !isSearch(buf[:n]) || !local(from.IP) || from.Port == 0 {
			continue
		}
		select {
		case answering <- struct{}{}:
			go func() {
				defer func() { <-answering }()
				if _, err := conn.WriteToUDP([]byte(answer()), from); err != nil && ctx.Err() == nil {
					slog.Debug("plex: answering a search failed", "err", err)
				}
			}()
		default: // more searches than any home sends at once: the rest go unanswered
		}
	}
}

// answering bounds the answers waiting to go out, so a flood of searches cannot become a flood of
// goroutines or a reflector pointed at whoever the searches claim to be from.
var answering = make(chan struct{}, 8)

// isSearch is whether a datagram is Plex's search. There are no MAN, MX or ST headers, as SSDP has:
// the whole packet is "M-SEARCH * HTTP/1.0" and a blank line, though senders differ on the version, on
// CRLF against LF, and on how many blank lines they send, so only the first line is read.
func isSearch(b []byte) bool {
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimRight(line, "\r")
	return strings.HasPrefix(line, "M-SEARCH * HTTP/")
}

// local is an address a search may come from: the home network's own ranges, never the internet.
func local(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

// answer is the reply to a search: what the player is, where its control server is, and what may be
// asked of it. The header names are Plex's, and the spelling is exactly what its own players send.
func answer() string {
	return "HTTP/1.0 200 OK\r\n" + serviceHeaders() + "\r\n"
}

// helloPacket is the same block announced rather than answered.
func helloPacket() string {
	return "HELLO * HTTP/1.0\r\n" + serviceHeaders() + "\r\n"
}

// byePacket is the announcement withdrawn. Plex does not document it; controllers that understand it
// drop the player at once rather than at the end of their own timeout.
func byePacket() string {
	return "BYE * HTTP/1.0\r\n" + serviceHeaders() + "\r\n"
}

// serviceHeaders is the block both packets carry, in the order Plex's own players write it.
func serviceHeaders() string {
	return "Content-Type: plex/media-player\r\n" +
		"Name: " + header(friendlyName()) + "\r\n" +
		"Port: " + strconv.Itoa(Port) + "\r\n" +
		"Product: " + product + "\r\n" +
		"Protocol: plex\r\n" +
		"Protocol-Capabilities: " + protocolCaps + "\r\n" +
		"Protocol-Version: " + strconv.Itoa(protocolVersion) + "\r\n" +
		"Resource-Identifier: " + clientIdentifier() + "\r\n" +
		"Version: " + version() + "\r\n" +
		"Device-Class: " + deviceClass + "\r\n"
}

// header is a value with what a header cannot carry taken out. A device name is the owner's: a newline
// in it would otherwise be a second header, which is how a packet becomes two commands.
func header(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// hello says the player is here, from each address the device has, so the packet leaves the interface
// that address is on.
func (f *Feature) hello(group netip.Addr) { f.tell(group, helloPacket()) }

// bye withdraws it.
func (f *Feature) bye(group netip.Addr) { f.tell(group, byePacket()) }

func (f *Feature) tell(group netip.Addr, packet string) {
	to := &net.UDPAddr{IP: group.AsSlice(), Port: helloPort}
	for _, ip := range metrics.Addresses() {
		if ip.To4() == nil {
			continue
		}
		conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: ip}, to)
		if err != nil {
			continue
		}
		_, _ = conn.Write([]byte(packet))
		conn.Close()
	}
}

// ports are the ones this feature opens in the device's firewall while an account is linked, and the
// functions that do it: variables so a test never runs iptables.
var (
	openTCP  = firewallOpen
	closeTCP = firewallClose
	openUDP  = firewallOpenUDP
	closeUDP = firewallCloseUDP
)

func (f *Feature) openPorts() {
	if err := openTCP(Port); err != nil {
		slog.Error("plex: opening the control port failed", "port", Port, "err", err)
	}
	if err := openUDP(searchPort, helloPort); err != nil {
		slog.Error("plex: opening Plex's discovery ports failed", "from", searchPort, "to", helloPort, "err", err)
	}
}

func (f *Feature) closePorts() {
	if err := closeTCP(); err != nil {
		slog.Warn("plex: closing the control port failed", "err", err)
	}
	if err := closeUDP(); err != nil {
		slog.Warn("plex: closing Plex's discovery ports failed", "err", err)
	}
}
