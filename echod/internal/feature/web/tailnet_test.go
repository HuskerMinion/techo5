package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestFromTailnet(t *testing.T) {
	for addr, want := range map[string]bool{
		"100.64.0.2:5000":          true,
		"100.127.255.255:1":        true,
		"[fd7a:115c:a1e0::5]:8181": true,
		"[::ffff:100.64.0.1]:80":   true,
		"192.0.2.20:5000":          false,
		"100.128.0.1:1":            false,
		"10.0.0.1:1":               false,
		"[fe80::1]:1":              false,
		"garbage":                  false,
	} {
		if got := FromTailnet(addr); got != want {
			t.Errorf("FromTailnet(%q) = %v", addr, got)
		}
	}
}

// Over the tailnet nothing answers; from the house, everything the switches allow.
func TestTailnetGetsNothing(t *testing.T) {
	was := LocalNets
	t.Cleanup(func() { LocalNets = was })
	LocalNets = func() (bool, []netip.Prefix) {
		return true, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} // Tailscale up; the house
	}
	h := tailnetGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, c := range []struct {
		from, path string
		want       int
	}{
		{"100.64.0.2:5000", "/intercom", http.StatusNotFound},
		{"100.64.0.2:5000", "/announce", http.StatusNotFound},
		{"100.64.0.2:5000", "/camera.jpg", http.StatusNotFound},
		{"100.64.0.2:5000", "/setup", http.StatusNotFound},
		{"100.64.0.2:5000", "/intercom/../camera.jpg", http.StatusNotFound},
		// The remote API, nearest claims and ONVIF: this house's only.
		{"100.64.0.2:5000", "/api/v1/entities", http.StatusNotFound},
		{"100.64.0.2:5000", "/nearest", http.StatusNotFound},
		{"100.64.0.2:5000", "/onvif/device_service", http.StatusNotFound},
		// Shared settings: between this house's devices only.
		{"100.64.0.2:5000", "/share", http.StatusNotFound},
		{"192.0.2.20:5000", "/share", http.StatusTeapot},
		{"192.0.2.20:5000", "/nearest", http.StatusTeapot},
		{"192.0.2.20:5000", "/camera.jpg", http.StatusTeapot},
	} {
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		r.RemoteAddr = c.from
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.from, c.path, w.Code, c.want)
		}
	}
}

// A house numbered in the tailnet's range is the house: a device on this network reaches /announce,
// and the camera, as any device here does; one beyond it, over the tailnet, reaches nothing.
func TestAHouseNumberedLikeATailnetIsServed(t *testing.T) {
	was := LocalNets
	t.Cleanup(func() { LocalNets = was })
	LocalNets = func() (bool, []netip.Prefix) { return true, []netip.Prefix{netip.MustParsePrefix("100.64.1.0/24")} }
	h := tailnetGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, c := range []struct {
		from, path string
		want       int
	}{
		{"100.64.1.5:5000", "/announce", http.StatusNoContent},
		{"100.64.1.5:5000", "/camera.jpg", http.StatusNoContent},
		{"100.64.0.20:5000", "/announce", http.StatusNotFound},
		{"100.64.0.20:5000", "/intercom", http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodPost, c.path, nil)
		r.RemoteAddr = c.from
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.from, c.path, w.Code, c.want)
		}
	}
}

// The interface Away looks for is the one tailscaled is started with; and with Tailscale connected, a
// tailnet address beyond this device's networks is from away even with no interface found.
func TestTheTailscaleInterface(t *testing.T) {
	if !strings.HasPrefix(TunName, "tailscale") {
		t.Errorf("TunName %q", TunName)
	}
	was, wasUp := LocalNets, TailscaleUp
	t.Cleanup(func() { LocalNets, TailscaleUp = was, wasUp })
	LocalNets = func() (bool, []netip.Prefix) { return false, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} }
	TailscaleUp = func() bool { return true }
	if !Away("100.64.0.20:80") || Away("192.0.2.9:80") {
		t.Error("with Tailscale connected and no interface found")
	}
}

// With Remote setup on, a browser from away reaches the setup page's own paths, and still nothing else.
func TestTheTailnetReachesTheSetupPageOnlyWithRemoteSetup(t *testing.T) {
	was, wasRemote := LocalNets, RemoteSetup
	t.Cleanup(func() { LocalNets, RemoteSetup = was, wasRemote })
	LocalNets = func() (bool, []netip.Prefix) {
		return true, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	}
	h := tailnetGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, remote := range []bool{false, true} {
		RemoteSetup = func() bool { return remote }
		page := http.StatusNotFound
		if remote {
			page = http.StatusTeapot
		}
		for _, c := range []struct {
			path string
			want int
		}{
			{"/setup", page},
			{"/setup/save", page},
			{"/setup/wait", page},
			{"/setupx", http.StatusNotFound},
			{"/", http.StatusNotFound},
			{"/camera.jpg", http.StatusNotFound},
			{"/screen", http.StatusNotFound},
			{"/intercom", http.StatusNotFound},
		} {
			r := httptest.NewRequest(http.MethodGet, c.path, nil)
			r.RemoteAddr, r.Host = "100.64.0.2:5000", "100.64.0.1:8181"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Errorf("remote setup %v, %s: %d, want %d", remote, c.path, w.Code, c.want)
			}
		}
	}

	// Asked for by a name, which a site of somebody else's could point at the device, or by none: not
	// served, Remote setup or not.
	for _, host := range []string{"[fd7a:115c:a1e0::1]:8181", "fd7a:115c:a1e0::1", "[fe80::1%25tailscale0]:8181"} {
		r := httptest.NewRequest(http.MethodGet, "/setup", nil)
		r.RemoteAddr, r.Host = "100.64.0.2:5000", host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusTeapot {
			t.Errorf("remote setup on, Host %q: %d, want it served", host, w.Code)
		}
	}
	for _, host := range []string{"example.com", "example.com:8181", ""} {
		r := httptest.NewRequest(http.MethodGet, "/setup", nil)
		r.RemoteAddr, r.Host = "100.64.0.2:5000", host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("remote setup on, Host %q: %d, want 404", host, w.Code)
		}
	}
}

// A house numbered across the whole tailnet range takes every tailnet address for its own (Away), but a
// request that reached the device's own tailnet address came over the tailnet: from away, and refused.
func TestARequestToTheTailnetAddressIsFromAway(t *testing.T) {
	was, wasAddrs, wasUp := LocalNets, TailnetAddrs, TailscaleUp
	t.Cleanup(func() { LocalNets, TailnetAddrs, TailscaleUp = was, wasAddrs, wasUp })
	TailscaleUp = func() bool { return true }
	LocalNets = func() (bool, []netip.Prefix) { return true, []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")} }
	TailnetAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("100.64.1.9")} }
	h := tailnetGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, c := range []struct {
		local string
		want  int
	}{
		{"100.64.1.9:8181", http.StatusNotFound}, // over the tailnet
		{"100.64.0.30:8181", http.StatusTeapot},  // the house's own address for the device
		{"[::ffff:100.64.1.9]:8181", http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodGet, "/camera.jpg", nil)
		r.RemoteAddr = "100.64.0.20:5000"
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, addrOf(c.local)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("reaching %s: %d, want %d", c.local, w.Code, c.want)
		}
	}
	if !AwayConn("192.0.2.5:1", "100.64.1.9:554") || AwayConn("192.0.2.5:1", "100.64.0.30:554") {
		t.Error("AwayConn by the address reached")
	}
	// With Tailscale not connected there is no tailnet address to have reached.
	TailscaleUp = func() bool { return false }
	if AwayConn("192.0.2.5:1", "100.64.1.9:554") {
		t.Error("AwayConn with Tailscale down")
	}
}

// addrOf is a net.Addr for host:port, as an http.Server puts the address it reached in a request.
func addrOf(s string) net.Addr {
	ap := netip.MustParseAddrPort(s)
	return net.TCPAddrFromAddrPort(ap)
}
