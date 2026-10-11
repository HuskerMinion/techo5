package streaming

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// dacpRemote is the way back to the phone playing over AirPlay: DACP, the remote control a sender
// serves for its receivers. The session brings the sender's DACP-ID and an Active-Remote token, which
// shairport-sync passes on with the metadata (daid, acre); the sender announces itself as
// iTunes_Ctrl_<DACP-ID> (_dacp._tcp), found through avahi; and it takes GET /ctrl-int/1/<command> with
// the token. This is AirPlay as shairport-sync receives it (classic, not AirPlay 2).
//
// An iPhone ends the session each time it pauses and starts another to play again, with the same
// DACP-ID and token, so what is known of the sender is kept across a pause (airplaySession) and dropped
// only when the session ends for good.
type dacpRemote struct {
	mu     sync.Mutex
	id     string     // DACP-ID
	token  string     // Active-Remote
	client netip.Addr // the sender's address as the session gave it (clip)
	port   uint16     // its DACP port, as shairport-sync found it (dapo)
}

// airplayRemote is the AirPlay receiver's way back to its sender; there is one receiver.
var airplayRemote = &dacpRemote{}

func (d *dacpRemote) setID(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id != d.id {
		// Another sender, or the same one choosing this speaker again: what was found of the last is
		// not this one's.
		d.client, d.port = netip.Addr{}, 0
	}
	d.id = id
}

func (d *dacpRemote) setToken(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = token
}

// setClient takes the sender's address as shairport-sync wrote it; one that does not read is left out.
func (d *dacpRemote) setClient(addr string) {
	a, err := netip.ParseAddr(addr)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		a = netip.Addr{}
	}
	d.client = a.Unmap()
}

// setPort takes the DACP port as shairport-sync sends it: four bytes, most significant first.
func (d *dacpRemote) setPort(b []byte) {
	if len(b) != 4 {
		return
	}
	p := binary.BigEndian.Uint32(b)
	d.mu.Lock()
	defer d.mu.Unlock()
	if p > 0 && p <= 0xffff {
		d.port = uint16(p)
	}
}

// forget drops the sender: the session is over, and nothing from here reaches it any more.
func (d *dacpRemote) forget() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.id, d.token, d.client, d.port = "", "", netip.Addr{}, 0
}

// dacpCommands are DACP's words for the controls the device has.
var dacpCommands = map[media.Transport]string{
	media.TransportPlay:     "play",
	media.TransportPause:    "pause",
	media.TransportToggle:   "playpause",
	media.TransportNext:     "nextitem",
	media.TransportPrevious: "previtem",
}

// control asks the sender for t, and says whether it took it: false with no sender to ask, or one that
// cannot be reached. It is media's ReceiverControl for AirPlay.
func (d *dacpRemote) control(t media.Transport) bool {
	cmd, ok := dacpCommands[t]
	if !ok {
		return false
	}
	d.mu.Lock()
	id, token, client, port := d.id, d.token, d.client, d.port
	d.mu.Unlock()
	if id == "" || token == "" {
		slog.Info("streaming: no AirPlay sender to ask", "command", cmd)
		return false
	}
	// Where the sender is: as avahi finds it, and else as the session gave it. The session's address is
	// often an IPv6 link-local one, which cannot be reached without knowing the interface; avahi's IPv4
	// one can.
	var tried []string
	ctx, cancel := context.WithTimeout(context.Background(), dacpResolveTimeout)
	host, err := dacpResolve(ctx, id)
	cancel()
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
		err := dacpSend(hp, cmd, token)
		if err == nil {
			slog.Info("streaming: told the AirPlay sender", "command", cmd)
			return true
		}
		slog.Warn("streaming: telling the AirPlay sender failed", "command", cmd, "at", hp, "err", err)
	}
	return false
}

// dacpResolveTimeout bounds finding the sender: avahi answers from its cache at once, and asks the
// network otherwise.
const dacpResolveTimeout = 3 * time.Second

// dacpClient sends the commands. A sender answers at once or not at all.
var dacpClient = &http.Client{
	Timeout: 2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// dacpSend asks the sender at hostport for cmd, with its token: any 2xx is taken (an iPhone answers
// 200, the protocol says 204).
func dacpSend(hostport, cmd, token string) error {
	req, err := http.NewRequest(http.MethodGet, "http://"+hostport+"/ctrl-int/1/"+cmd, nil)
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
// needs no interface to reach, and its port.
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
	if net.ParseIP(addr) == nil || port == 0 {
		return "", errors.New("avahi gave no address")
	}
	return net.JoinHostPort(addr, strconv.Itoa(int(port))), nil
}
