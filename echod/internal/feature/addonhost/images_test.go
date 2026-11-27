package addonhost

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// An add-on's image address is the device fetching for it: refused, as it is dialed, for the device
// itself, loopback, link-local and any public address, and allowed on the LAN or at Home Assistant's
// own host. None of these is dialed for real: the refusal comes before the connection is made.
func TestImageFetchRefuses(t *testing.T) {
	savedOwn, savedHA := netguard.OwnIP, haAddress
	t.Cleanup(func() { netguard.OwnIP, haAddress = savedOwn, savedHA })
	netguard.OwnIP = func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.168.1.50")) }
	haAddress = func() string { return "" }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, addr := range []string{
		"127.0.0.1:80", "[::1]:80", "169.254.1.1:80", "[fe80::1]:80", "224.0.0.1:80", "0.0.0.0:80",
		"192.168.1.50:80", // the device's own LAN address
		"8.8.8.8:80", "[2001:4860:4860::8888]:80", "1.1.1.1:443",
	} {
		if c, err := imageDial(ctx, "tcp", addr); err == nil {
			c.Close()
			t.Errorf("an image was fetched from %s", addr)
		} else if !strings.Contains(err.Error(), "may not reach") && !strings.Contains(err.Error(), "not from") {
			t.Errorf("%s refused for another reason: %v", addr, err)
		}
	}
	// What the server's checks refuse before anything is dialed: an address that is a number off the LAN.
	for _, u := range []string{"http://8.8.8.8/a.png", "http://127.0.0.1/a.png", "http://[fe80::1]/a.png", "ftp://192.168.1.2/a.png"} {
		if addonapi.ImageURL(u) == nil {
			t.Errorf("%s was allowed in a scene", u)
		}
	}
}

// A LAN server's image is fetched, held to MaxImage; Home Assistant's own host is fetched from whatever
// it resolves to, so long as that is not the device itself.
func TestImageFetchServes(t *testing.T) {
	savedLAN, savedRefused, savedHA := imageLAN, netguard.Refused, haAddress
	t.Cleanup(func() { imageLAN, netguard.Refused, haAddress = savedLAN, savedRefused, savedHA })
	png := []byte("\x89PNG\r\n\x1a\n....")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big.png":
			w.Write(make([]byte, addonapi.MaxImage+1))
		case "/gone.png":
			http.NotFound(w, r)
		default:
			w.Write(png)
		}
	}))
	defer srv.Close()
	// The test's server is on loopback: let loopback stand in for the LAN here.
	loop := netip.MustParseAddr("127.0.0.1")
	// Set once and switched by atomics: a dial the transport started for an earlier fetch may still be
	// reading them.
	var offLAN atomic.Bool
	var ha atomic.Value
	ha.Store("")
	netguard.Refused = func(ip net.IP) bool { return !ip.Equal(net.ParseIP("127.0.0.1")) }
	imageLAN = func(ip netip.Addr) bool { return !offLAN.Load() && ip == loop }
	haAddress = func() string { return ha.Load().(string) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if b, err := fetchImage(ctx, srv.URL+"/a.png"); err != nil || string(b) != string(png) {
		t.Fatalf("a LAN image: %q %v", b, err)
	}
	if _, err := fetchImage(ctx, srv.URL+"/big.png"); err == nil {
		t.Error("an image past MaxImage was taken")
	}
	if _, err := fetchImage(ctx, srv.URL+"/gone.png"); err == nil {
		t.Error("a 404 was taken")
	}
	// Off the LAN, refused; at Home Assistant's own host, by its name, fetched.
	offLAN.Store(true)
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	if _, err := fetchImage(ctx, "http://localhost:"+port+"/a.png"); err == nil {
		t.Error("an image off the LAN was fetched")
	}
	ha.Store("http://localhost:" + port)
	if _, err := fetchImage(ctx, "http://localhost:"+port+"/a.png"); err != nil {
		t.Errorf("an image at Home Assistant's host: %v", err)
	}
	// At Home Assistant's host, only Home Assistant: another port there, or another scheme, is another
	// service the host runs, and refused, as is a redirect to one.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(png) }))
	defer other.Close()
	otherPort := other.URL[strings.LastIndex(other.URL, ":")+1:]
	if _, err := fetchImage(ctx, "http://localhost:"+otherPort+"/a.png"); err == nil {
		t.Error("an image from another port at Home Assistant's host was fetched")
	}
	if _, err := imageDial(ctx, "tcp", "localhost:"+otherPort); err == nil {
		t.Error("another port at Home Assistant's host was dialed")
	}
	if _, err := fetchImage(ctx, "https://localhost:"+port+"/a.png"); err == nil || !strings.Contains(err.Error(), "Home Assistant itself") {
		t.Errorf("another scheme at Home Assistant's host: %v", err)
	}
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+otherPort+"/a.png", http.StatusFound)
	}))
	defer hop.Close()
	hopPort := hop.URL[strings.LastIndex(hop.URL, ":")+1:]
	ha.Store("http://localhost:" + hopPort)
	if _, err := fetchImage(ctx, "http://localhost:"+hopPort+"/a.png"); err == nil {
		t.Error("a redirect from Home Assistant to another port at its host was followed")
	}
	// An address with no port is its scheme's.
	ha.Store("http://localhost")
	if _, err := imageDial(ctx, "tcp", "localhost:"+port); err == nil || !strings.Contains(err.Error(), "port 80 only") {
		t.Errorf("Home Assistant at port 80, dialed at %s: %v", port, err)
	}
}
