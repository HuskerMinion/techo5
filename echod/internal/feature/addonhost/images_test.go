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
	savedOwn, savedHA := netguard.OwnIP, haHost
	t.Cleanup(func() { netguard.OwnIP, haHost = savedOwn, savedHA })
	netguard.OwnIP = func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.168.1.50")) }
	haHost = func() string { return "" }
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
	savedLAN, savedRefused, savedHA := imageLAN, netguard.Refused, haHost
	t.Cleanup(func() { imageLAN, netguard.Refused, haHost = savedLAN, savedRefused, savedHA })
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
	haHost = func() string { return ha.Load().(string) }
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
	ha.Store("localhost")
	if _, err := fetchImage(ctx, "http://localhost:"+port+"/a.png"); err != nil {
		t.Errorf("an image at Home Assistant's host: %v", err)
	}
}
