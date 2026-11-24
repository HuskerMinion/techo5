package media

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/useragent"
)

// streamClient fetches the streams the device plays itself. Two things differ from Go's default client.
//
// SHOUTcast v1 servers, still behind a good share of internet radio, answer "ICY 200 OK" where HTTP
// says "HTTP/1.0 200 OK", which Go refuses as a malformed response; the first bytes a connection reads
// are put right (icyConn).
//
// A station's address comes from wherever the station came from - Radio Browser, which anyone can add
// to, as well as the setup page - and a redirect takes the device wherever it says. A station on the
// internet may not send it to an address in the home instead: that would be the device making a request
// on a stranger's behalf to something on the network it sits on. A station that is on the home network
// to begin with, one typed on the setup page, can redirect within it.
//
// And every request names the device (useragent): some station networks give Go's own User-Agent a
// short clip and then nothing (#730). The transport sets it, so the requests redirects make carry it
// too.
var streamClient = &http.Client{
	Transport: useragent.Transport(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           icyDial,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !inHome(via[0].URL.Hostname()) && inHome(req.URL.Hostname()) {
			return errors.New("a station on the internet redirected to an address on the home network")
		}
		return nil
	},
}

// inHome is whether host is on the home network or the device itself: a private, loopback or link-local
// address, or a name ending in .local or localhost. A name that resolves to one counts too.
func inHome(host string) bool {
	if host == "localhost" || len(host) > 6 && host[len(host)-6:] == ".local" {
		return true
	}
	private := func(a netip.Addr) bool {
		return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified()
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return private(a.Unmap())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if private(a.Unmap()) {
			return true
		}
	}
	return false
}

func icyDial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return &icyConn{Conn: c}, nil
}

// icyConn reads a connection, turning an "ICY " status line into "HTTP/1.0 ". Only the first bytes are
// looked at; everything after passes through untouched, TLS included (whose first bytes are never
// "ICY ").
type icyConn struct {
	net.Conn
	checked bool
	pending []byte
}

func (c *icyConn) Read(p []byte) (int, error) {
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	n, err := c.Conn.Read(p)
	if c.checked || n == 0 {
		return n, err
	}
	c.checked = true
	if n >= 4 && string(p[:4]) == "ICY " {
		fixed := append([]byte("HTTP/1.0 "), p[4:n]...)
		m := copy(p, fixed)
		c.pending = fixed[m:]
		return m, err
	}
	return n, err
}
