package api

import (
	"log/slog"
	"net"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// awayListener turns away every connection from away, over the tailnet (web.AwayConn), and is put in
// front of the listener while the link has no key of its own (servedListener): unprovisioned or open for
// adoption, the link takes whoever connects first, and over the tailnet that could be any node the
// owner's rules let through. At home nothing changes.
type awayListener struct{ net.Listener }

func (l awayListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return c, err
		}
		if web.AwayConn(c.RemoteAddr().String(), c.LocalAddr().String()) {
			slog.Warn("api: refused a connection from away; the link has no key of its own", "from", c.RemoteAddr())
			_ = c.Close()
			continue
		}
		return c, nil
	}
}
