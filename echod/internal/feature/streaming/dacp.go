package streaming

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// dacpRemote is the way back to the phone playing over AirPlay: DACP, the remote control a sender
// serves for its receivers. The session brings the sender's DACP-ID and an Active-Remote token, which
// shairport-sync passes on with the metadata (daid, acre); the sender announces itself as
// iTunes_Ctrl_<DACP-ID> (_dacp._tcp), found through avahi; and it takes GET /ctrl-int/1/<command> with
// the token. This is AirPlay as shairport-sync receives it (classic, not AirPlay 2).
//
// Each session that plays names its sender afresh (take), from what it gave as it was set up and
// nothing else: one from a sender that gives no DACP-ID or token leaves none to ask, rather than the
// last one, which may be playing somewhere else by now. An iPhone ends the session each time it pauses
// and starts another to play again, with the same DACP-ID and token, so what is known of it is kept
// across a pause, for airplayKeptFor at most (airplaySession), and dropped when it has gone.
type dacpRemote struct {
	mu     sync.Mutex
	id     string     // DACP-ID
	token  string     // Active-Remote
	client netip.Addr // the sender's address as the session gave it (clip)
	port   uint16     // its DACP port, as shairport-sync found it (dapo)
	portOf string     // the DACP-ID port was found for, kept with it when the sender goes (take)
	took   uint64     // counts the sessions taken (take), for forgetIf
}

// airplayRemote is the AirPlay receiver's way back to its sender; there is one receiver.
var airplayRemote = &dacpRemote{}

// dacpSender is what a session says of its sender as it is set up, each part empty where the sender
// gave none.
type dacpSender struct {
	id, token string     // DACP-ID and Active-Remote (daid, acre)
	client    netip.Addr // where the session came from (clip), if a sender could be there (senderAddr)
	from      netip.Addr // where it came from as it was given (clientAddr), link-local or not: not for asking, for telling it from another's (disc)
}

// take makes s, the sender of a session starting to play, the one commands go to, whole: what it did
// not give is not taken from the last sender. It says whether s is another sender than the last:
// another DACP-ID, none, or one let go of since (forget).
//
// The port found for a DACP-ID (dapo) outlasts its sender: shairport-sync looks it up once, and not
// again for a phone that comes back after it had gone, or after a session that named none. Only
// another DACP-ID drops it, and shairport-sync then looks that one's up.
func (d *dacpRemote) take(s dacpSender) (another bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	another = s.id == "" || s.id != d.id
	if s.id != "" && s.id != d.portOf {
		d.port, d.portOf = 0, ""
	}
	d.id, d.token, d.client = s.id, s.token, s.client
	d.took++
	return another
}

// sessions counts the sessions taken so far, for forgetIf.
func (d *dacpRemote) sessions() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.took
}

// setPort takes the DACP port as shairport-sync sends it, four bytes, most significant first: for the
// sender taken, which shairport-sync looks up once it plays, or, with none, for the last one it looked
// up (portOf), whose service avahi saw again. Coming with neither, it is passed over.
func (d *dacpRemote) setPort(b []byte) {
	if len(b) != 4 {
		return
	}
	p := binary.BigEndian.Uint32(b)
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case p == 0 || p > 0xffff:
	case d.id != "":
		d.port, d.portOf = uint16(p), d.id
	case d.portOf != "":
		d.port = uint16(p)
	}
}

// forget drops the sender: it has gone, and nothing from here reaches it any more. The port found for
// its DACP-ID stays with it (take).
func (d *dacpRemote) forget() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.id, d.token, d.client = "", "", netip.Addr{}
}

// forgetIf drops the sender unless a session has been taken since sessions said n, and says whether it
// did: in one hold of mu, so the sender of a session starting just then is not the one dropped.
func (d *dacpRemote) forgetIf(n uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.took != n {
		return false
	}
	d.id, d.token, d.client = "", "", netip.Addr{}
	return true
}

// dacpCommands are DACP's words for the controls the device has.
var dacpCommands = map[media.Transport]string{
	media.TransportPlay:     "play",
	media.TransportPause:    "pause",
	media.TransportToggle:   "playpause",
	media.TransportNext:     "nextitem",
	media.TransportPrevious: "previtem",
}

// control binds t, as it is given, to the sender taken then (take): what it gives back asks that sender
// for t when its turn comes (media's senderQueue), and says whether it took it. It is media's
// ReceiverControl for AirPlay.
func (d *dacpRemote) control(t media.Transport) func() bool {
	d.mu.Lock()
	of := d.id
	d.mu.Unlock()
	return func() bool { return d.ask(t, of) }
}

// ask asks the sender for t, and says whether it took it: false with no sender to ask, or one that
// cannot be reached within dacpAskTimeout. A command given for one sender (of, its DACP-ID) and still
// waiting when another has been taken, behind an ask of a phone slow to answer, is not passed on to
// the new one, which plays a song of its own: it is let go of as taken, and the track here left as it
// is.
func (d *dacpRemote) ask(t media.Transport, of string) bool {
	cmd, ok := dacpCommands[t]
	if !ok {
		return false
	}
	d.mu.Lock()
	id, token, client, port := d.id, d.token, d.client, d.port
	d.mu.Unlock()
	if id != of {
		slog.Info("streaming: a command given for the last AirPlay sender is not passed to the next", "command", cmd)
		return true
	}
	if id == "" || token == "" {
		slog.Info("streaming: no AirPlay sender to ask", "command", cmd)
		return false
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), dacpAskTimeout)
	defer cancel()
	// Where the sender is: as avahi finds it, and else as the session gave it. The session's address is
	// often an IPv6 link-local one, which cannot be reached without knowing the interface; avahi's IPv4
	// one can.
	var tried []string
	rctx, rcancel := context.WithTimeout(ctx, dacpResolveTimeout)
	host, err := dacpResolve(rctx, id)
	rcancel()
	if err == nil {
		tried = append(tried, host)
	} else {
		slog.Warn("streaming: finding the AirPlay sender's remote control failed", "err", err)
	}
	if client.IsValid() && port != 0 && !client.IsLinkLocalUnicast() {
		if hp := netip.AddrPortFrom(client, port).String(); len(tried) == 0 || tried[0] != hp {
			tried = append(tried, hp)
		}
	}
	for _, hp := range tried {
		err := dacpSend(ctx, hp, cmd, token)
		if err == nil {
			slog.Info("streaming: told the AirPlay sender", "command", cmd, "took", time.Since(start).Round(time.Millisecond))
			return true
		}
		slog.Warn("streaming: telling the AirPlay sender failed", "command", cmd, "at", hp, "err", err)
	}
	return false
}

// dacpAskTimeout bounds one command from start to answer: finding the sender and every try at it. A
// sender that is there answers at once, and avahi from its cache; one that has gone holds a pause on the
// device, which it never hears, no longer than this before the track is stopped here instead (media's
// tellSender), and what waits behind it no longer either. A variable, for the tests.
var dacpAskTimeout = 4 * time.Second

// dacpResolveTimeout bounds finding the sender, within dacpAskTimeout: avahi answers from its cache at
// once, and asks the network otherwise. What is left is for asking it.
const dacpResolveTimeout = 3 * time.Second

// dacpClient sends the commands. A sender answers at once or not at all. It dials through netguard, as
// every connection the device makes for somebody else's address does, so the address is checked as it
// is connected to, on each connection, and not only as the session gave it (senderAddr): the device's
// own addresses are read then. It goes direct, by no proxy the environment names.
var dacpClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         netguard.Dialer(2 * time.Second).DialContext,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// dacpSend asks the sender at hostport for cmd, with its token, within ctx: any 2xx is taken (an iPhone
// answers 200, the protocol says 204).
func dacpSend(ctx context.Context, hostport, cmd, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostport+"/ctrl-int/1/"+cmd, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Active-Remote", token)
	resp, err := dacpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("answered %s", resp.Status)
	}
	return nil
}

// dacpResolve finds the remote control of the sender whose DACP-ID is id, as host:port; a variable,
// for the tests.
var dacpResolve = avahiResolveDACP

// avahiResolveDACP asks avahi, on the system bus, where iTunes_Ctrl_<id> is: its IPv4 address, which
// needs no interface to reach, and its port. Any host on the network can announce that name, at any
// address, so one no sender could be at is not taken (dacpHost).
func avahiResolveDACP(ctx context.Context, id string) (string, error) {
	conn, err := dbus.Connect("unix:path="+busSocket, dbus.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("system bus: %w", err)
	}
	defer conn.Close()
	const (
		ifUnspec    = int32(-1) // AVAHI_IF_UNSPEC
		protoUnspec = int32(-1) // AVAHI_PROTO_UNSPEC
		protoInet   = int32(0)  // AVAHI_PROTO_INET
	)
	var (
		iface, proto, aproto       int32
		name, typ, domain, srvHost string
		addr                       string
		port                       uint16
		txt                        [][]byte
		flags                      uint32
	)
	err = conn.Object("org.freedesktop.Avahi", "/").CallWithContext(ctx, "org.freedesktop.Avahi.Server.ResolveService", 0,
		ifUnspec, protoUnspec, "iTunes_Ctrl_"+id, "_dacp._tcp", "local", protoInet, uint32(0)).
		Store(&iface, &proto, &name, &typ, &domain, &srvHost, &aproto, &addr, &port, &txt, &flags)
	if err != nil {
		return "", err
	}
	return dacpHost(addr, port)
}

// dacpHost is the sender's remote control at addr and port as avahi gives them, as host:port, if a
// sender could be there (senderAddr).
func dacpHost(addr string, port uint16) (string, error) {
	a := senderAddr(addr)
	if !a.IsValid() || port == 0 {
		return "", fmt.Errorf("avahi gave no address a sender could be at: %q, port %d", addr, port)
	}
	return netip.AddrPortFrom(a, port).String(), nil
}

// clientAddr is addr, as a session (clip, disc) or avahi gives it, as an address, and the zero Addr if
// it is none: an IPv4 address written as IPv6 is taken as IPv4, and a zone (an interface's name, on a
// link-local address, which the address means only with) is left off, so the same client's address is
// the same however it was written.
func clientAddr(addr string) netip.Addr {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

// senderAddr is addr, as a session (clip) or avahi gives it (clientAddr), if a sender could be there,
// and the zero Addr otherwise. Any host on the network can name an address, and some are not a
// sender's: those netguard keeps the device's connections off (the device itself, by loopback or by
// one of its own addresses, which the kernel carries over loopback past the firewall; link-local; a
// group), the rest of 0.0.0.0/8, where no host is, and the broadcast address. A zone is left off: one
// on a global address got past AsSlice, and into the address dialed.
func senderAddr(addr string) netip.Addr {
	a := clientAddr(addr)
	if !a.IsValid() {
		return a
	}
	if netguard.Refused(a.AsSlice()) || (a.Is4() && (a.As4()[0] == 0 || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}))) {
		return netip.Addr{}
	}
	return a
}
