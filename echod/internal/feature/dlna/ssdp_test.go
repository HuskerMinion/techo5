package dlna

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// ssdpMessage is one SSDP datagram read back: its first line and its headers, keys in upper case.
type ssdpMessage struct {
	first string
	h     map[string]string
}

func readSSDP(b []byte) ssdpMessage {
	lines := strings.Split(string(b), "\r\n")
	m := ssdpMessage{first: lines[0], h: map[string]string{}}
	for _, l := range lines[1:] {
		if k, v, ok := strings.Cut(l, ":"); ok {
			m.h[strings.ToUpper(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// collect reads what reaches conn until quiet has gone by with nothing more.
func collect(t *testing.T, conn *net.UDPConn, quiet time.Duration) []ssdpMessage {
	t.Helper()
	var got []ssdpMessage
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(quiet))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return got
		}
		got = append(got, readSSDP(buf[:n]))
	}
}

// responder starts the search side of the renderer on a loopback port, and a controller to search from.
func responder(t *testing.T) (device *net.UDPAddr, controller *net.UDPConn) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	udn() // made and saved now, so a slow disk does not hold up the first answer
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		respond(ctx, srv)
	}()
	t.Cleanup(func() {
		cancel()
		srv.Close()
		<-done
	})
	controller, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { controller.Close() })
	return srv.LocalAddr().(*net.UDPAddr), controller
}

func mSearch(host, st string, mx int) string {
	return "M-SEARCH * HTTP/1.1\r\nHOST: " + host + "\r\nMAN: \"ssdp:discover\"\r\nMX: " + strconv.Itoa(mx) +
		"\r\nST: " + st + "\r\n\r\n"
}

// TestEverySearchIsAnsweredWithItsOwnTarget: a controller may look for the device by any name SSDP
// gives it, each service included, and each answer names what was looked for (#779).
func TestEverySearchIsAnsweredWithItsOwnTarget(t *testing.T) {
	device, controller := responder(t)
	id := udn()
	all := []string{"upnp:rootdevice", id, deviceType, avtType, rcType, cmType}
	cases := []struct {
		st   string
		want []string
	}{
		{"ssdp:all", all},
		{"upnp:rootdevice", []string{"upnp:rootdevice"}},
		{id, []string{id}},
		{deviceType, []string{deviceType}},
		{avtType, []string{avtType}},
		{rcType, []string{rcType}},
		{cmType, []string{cmType}},
		{"urn:schemas-upnp-org:device:MediaServer:1", nil},
		{"urn:schemas-upnp-org:service:AVTransport:2", nil},
	}
	for _, c := range cases {
		for _, host := range []string{ssdpAddr, device.String()} {
			if _, err := controller.WriteToUDP([]byte(mSearch(host, c.st, 1)), device); err != nil {
				t.Fatal(err)
			}
			quiet := 700 * time.Millisecond // past the most an MX of 1 waits
			if host != ssdpAddr {
				quiet = 200 * time.Millisecond
			}
			got := collect(t, controller, quiet)
			if len(got) != len(c.want) {
				t.Fatalf("ST %s to %s: %d answers, want %d", c.st, host, len(got), len(c.want))
			}
			for i, m := range got {
				want := c.want[i]
				wantUSN := id + "::" + want
				if want == id {
					wantUSN = id
				}
				if m.first != "HTTP/1.1 200 OK" || m.h["ST"] != want || m.h["USN"] != wantUSN {
					t.Errorf("ST %s: answer %q ST %q USN %q, want ST %q USN %q", c.st, m.first, m.h["ST"], m.h["USN"], want, wantUSN)
				}
				if m.h["LOCATION"] != location("127.0.0.1") || m.h["CACHE-CONTROL"] != "max-age=1800" {
					t.Errorf("ST %s: LOCATION %q CACHE-CONTROL %q", c.st, m.h["LOCATION"], m.h["CACHE-CONTROL"])
				}
				if _, ok := m.h["EXT"]; !ok {
					t.Errorf("ST %s: no EXT", c.st)
				}
			}
		}
	}
}

// TestASearchSentToTheDeviceIsAnsweredAtOnce: unicast M-SEARCH has no MX to wait out.
func TestASearchSentToTheDeviceIsAnsweredAtOnce(t *testing.T) {
	device, controller := responder(t)
	for range 5 {
		start := time.Now()
		if _, err := controller.WriteToUDP([]byte(mSearch(device.String(), "upnp:rootdevice", 5)), device); err != nil {
			t.Fatal(err)
		}
		_ = controller.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := controller.ReadFromUDP(make([]byte, 2048)); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 300*time.Millisecond {
			t.Fatalf("answered after %v", d)
		}
	}
}

func TestLooseSearchesAreStillSearches(t *testing.T) {
	cases := []struct {
		name, msg string
		want      search
		ok        bool
	}{
		{"no blank line at the end", "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 3\r\nST: ssdp:all\r\n", search{"ssdp:all", 3, false}, true},
		{"bare newlines, any case", "m-search * HTTP/1.1\nhost: 239.255.255.250:1900\nman: \"ssdp:discover\"\nmx: 2\nst: upnp:rootdevice\n\n", search{"upnp:rootdevice", 2, false}, true},
		{"no spaces, unquoted MAN", "M-SEARCH * HTTP/1.1\r\nHOST:239.255.255.250:1900\r\nMAN:ssdp:discover\r\nST:" + avtType + "\r\nMX:1\r\n\r\n", search{avtType, 1, false}, true},
		{"MX capped at 5", mSearch(ssdpAddr, "ssdp:all", 120), search{"ssdp:all", 5, false}, true},
		{"no MX", "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nST: ssdp:all\r\n\r\n", search{"ssdp:all", 1, false}, true},
		{"to the device alone", mSearch("192.168.1.20:1900", "ssdp:all", 3), search{"ssdp:all", 3, true}, true},
		{"not a discover", "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:other\"\r\nST: ssdp:all\r\n\r\n", search{}, false},
		{"no ST", "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\n\r\n", search{}, false},
		{"another device's NOTIFY", "NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nNT: upnp:rootdevice\r\nNTS: ssdp:alive\r\n\r\n", search{}, false},
		{"another device's answer", "HTTP/1.1 200 OK\r\nST: upnp:rootdevice\r\n\r\n", search{}, false},
		{"empty", "", search{}, false},
	}
	for _, c := range cases {
		got, ok := parseSearch([]byte(c.msg))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if w := (search{st: "ssdp:all", mx: 5, unicast: true}).wait(); w != 0 {
		t.Errorf("a search to the device alone waits %v", w)
	}
	for range 50 {
		if w := (search{st: "ssdp:all", mx: 5}).wait(); w < 0 || w >= 5*time.Second {
			t.Fatalf("a wait of %v for MX 5", w)
		}
	}
}

// TestEveryTargetIsAnnounced: a NOTIFY for the root device, the device's own id, its type and each
// service, from the address given, alive and then goodbye.
func TestEveryTargetIsAnnounced(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	group, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	id := udn()
	for _, nts := range []string{"ssdp:alive", "ssdp:byebye"} {
		notify([]net.IP{net.IPv4(127, 0, 0, 1), net.ParseIP("::1")}, group.LocalAddr().(*net.UDPAddr), nts)
		got := collect(t, group, 300*time.Millisecond)
		want := []string{"upnp:rootdevice", id, deviceType, avtType, rcType, cmType}
		if len(got) != len(want) {
			t.Fatalf("%s: %d NOTIFYs, want %d", nts, len(got), len(want))
		}
		for i, m := range got {
			wantUSN := id + "::" + want[i]
			if want[i] == id {
				wantUSN = id
			}
			if m.first != "NOTIFY * HTTP/1.1" || m.h["NT"] != want[i] || m.h["USN"] != wantUSN || m.h["NTS"] != nts {
				t.Errorf("%s: %q NT %q USN %q NTS %q", nts, m.first, m.h["NT"], m.h["USN"], m.h["NTS"])
			}
			if m.h["HOST"] != ssdpAddr || m.h["LOCATION"] != location("127.0.0.1") || m.h["CACHE-CONTROL"] != "max-age=1800" {
				t.Errorf("%s: HOST %q LOCATION %q CACHE-CONTROL %q", nts, m.h["HOST"], m.h["LOCATION"], m.h["CACHE-CONTROL"])
			}
		}
	}
}

// TestTheDeviceSaysItIsHereOftenAndTwice: each round joins the group again and announces twice, the
// rounds come well inside max-age, and stopping says goodbye twice.
func TestTheDeviceSaysItIsHereOftenAndTwice(t *testing.T) {
	if aliveEvery+20*time.Second > maxAge*time.Second/2 {
		t.Fatalf("announced every %v with a max-age of %ds", aliveEvery, maxAge)
	}
	var mu sync.Mutex
	var said []string
	joins := 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		herald(ctx, func() { mu.Lock(); joins++; mu.Unlock() },
			func(nts string) { mu.Lock(); said = append(said, nts); mu.Unlock() },
			func() time.Duration { return 50 * time.Millisecond })
	}()
	time.Sleep(2*sayAgain + 2*50*time.Millisecond + 100*time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	alive := 0
	for _, s := range said[:len(said)-2] {
		if s != "ssdp:alive" {
			t.Fatalf("said %v", said)
		}
		alive++
	}
	if alive < 4 || alive%2 != 0 || joins != alive/2 {
		t.Errorf("%d alive in %d rounds: %v", alive, joins, said)
	}
	if said[len(said)-1] != "ssdp:byebye" || said[len(said)-2] != "ssdp:byebye" {
		t.Errorf("did not say goodbye twice: %v", said)
	}
}
