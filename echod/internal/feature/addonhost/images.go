package addonhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	neturl "net/url"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/useragent"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's images by address (stage 2): the daemon fetches them for the add-on's scenes, never the
// add-on, and only from the LAN or Home Assistant's own host. The address an add-on names is the
// device fetching for it, so the rule is held where the connection is made, on the address actually
// dialed, whatever the name resolved to and wherever a redirect led: never the device itself, loopback,
// link-local or multicast (netguard), and nothing but a private address (10/8, 172.16/12, 192.168/16,
// fc00::/7) or one in 100.64/10, where a Home Assistant reached over Tailscale is, unless the name dialed
// is Home Assistant's own host, as the device was given it, and then only at Home Assistant's own scheme
// and port. The device's Home Assistant sign-in is never
// sent: what an image needs of Home Assistant is what its /local folder serves to anyone on the LAN.
// What comes is held to addonapi.MaxImage bytes and fetchWithin, kept in RAM by the server, and never
// written down.

// imageDialWithin and imageHeaderWithin bound connecting and waiting for the answer's head; the server
// bounds the whole fetch (serve.fetchWithin).
const (
	imageDialWithin   = 5 * time.Second
	imageHeaderWithin = 8 * time.Second
)

// haAddress is Home Assistant's address, as the device was given it ("http://192.168.1.5:8123"), or ""
// with none; a variable for tests.
var haAddress = func() string { return hass.Get().Address() }

// haOrigin is Home Assistant's scheme, host and port, the port its scheme's own when the address names
// none; ok is false with no address, or one that does not read.
func haOrigin() (scheme, host, port string, ok bool) {
	u, err := neturl.Parse(haAddress())
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", "", false
	}
	return u.Scheme, u.Hostname(), portOf(u), true
}

// portOf is the port an address is fetched from: the one it names, or its scheme's.
func portOf(u *neturl.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// atHA is whether host is Home Assistant's own host.
func atHA(host string) bool {
	_, ha, _, ok := haOrigin()
	return ok && strings.EqualFold(strings.Trim(host, "[]"), ha)
}

// haURLProblem is what is wrong with fetching u, or nil: at Home Assistant's own host, only what
// Home Assistant itself serves there, its scheme and port, not another service the host runs.
func haURLProblem(u *neturl.URL) error {
	scheme, host, port, ok := haOrigin()
	if !ok || !strings.EqualFold(u.Hostname(), host) {
		return nil
	}
	if u.Scheme != scheme || portOf(u) != port {
		return fmt.Errorf("an add-on's image at Home Assistant's host is fetched from Home Assistant itself (%s, port %s) only", scheme, port)
	}
	return nil
}

// imageLAN is addonapi.LANAddr; a variable for tests, whose servers are on loopback.
var imageLAN = addonapi.LANAddr

// imageDial connects for an image's fetch: through netguard, and to a LAN address only, unless the
// name dialed is Home Assistant's own host, at its own port.
func imageDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	d := netguard.Dialer(imageDialWithin)
	if atHA(host) {
		if _, _, haPort, _ := haOrigin(); port != haPort {
			return nil, fmt.Errorf("an add-on's image at Home Assistant's host is fetched from its port %s only, not %s", haPort, port)
		}
	} else {
		guard := d.Control
		d.Control = func(network, address string, c syscall.RawConn) error {
			if err := guard(network, address, c); err != nil {
				return err
			}
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(h)
			if err != nil || !imageLAN(ip) {
				return fmt.Errorf("an add-on's image is fetched from the LAN or Home Assistant, not from %s", h)
			}
			return nil
		}
	}
	return d.DialContext(ctx, network, addr)
}

// imageClient fetches add-ons' images: no proxy, imageDial, a few redirects within http and https.
var imageClient = &http.Client{
	Transport: useragent.Transport(&http.Transport{
		DialContext:           imageDial,
		TLSHandshakeTimeout:   imageDialWithin,
		ResponseHeaderTimeout: imageHeaderWithin,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("a redirect to an address that is not plain http or https")
		}
		return haURLProblem(req.URL)
	},
}

// errImageLarge is an image's address serving more than addonapi.MaxImage.
var errImageLarge = fmt.Errorf("an image is at most %d bytes", addonapi.MaxImage)

// fetchImage is the server's FetchImage: the bytes an image's address serves, at most
// addonapi.MaxImage of them, within ctx.
func fetchImage(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if err := haURLProblem(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png, image/jpeg")
	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the image's server answered %d", resp.StatusCode)
	case resp.ContentLength > addonapi.MaxImage:
		return nil, errImageLarge
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, addonapi.MaxImage+1))
	if err != nil {
		return nil, err
	}
	if len(b) > addonapi.MaxImage {
		return nil, errImageLarge
	}
	return b, nil
}
