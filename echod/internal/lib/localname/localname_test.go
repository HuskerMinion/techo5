package localname

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestIsLocal(t *testing.T) {
	for host, want := range map[string]bool{
		"homeassistant.local": true, "HomeAssistant.LOCAL.": true, "homeassistant.example": false,
		"192.0.2.20": false, "local": false, "": false, "my.local.example.com": false,
	} {
		if IsLocal(host) != want {
			t.Errorf("IsLocal(%q) = %v", host, !want)
		}
	}
}

// answer is a responder on loopback standing in for the network: it answers a question for name,
// after skipping the first `skip` questions, as a lossy network would.
func answer(t *testing.T, name string, ip net.IP, skip int) (asked func() int) {
	t.Helper()
	return answerAll(t, name, []net.IP{ip}, skip)
}

// answerAll answers with each of ips for name, in that order.
func answerAll(t *testing.T, name string, ips []net.IP, skip int) (asked func() int) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	was := listen
	listen = func() (*net.UDPConn, *net.UDPAddr, error) {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		return c, conn.LocalAddr().(*net.UDPAddr), err
	}
	t.Cleanup(func() { listen = was })
	count := make(chan int, 1)
	count <- 0
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			c := <-count
			c++
			count <- c
			var q dns.Msg
			if q.Unpack(buf[:n]) != nil || len(q.Question) != 1 || c <= skip {
				continue
			}
			r := new(dns.Msg)
			r.SetReply(&q)
			// A record for something else first, as a busy responder may send.
			r.Answer = append(r.Answer, &dns.A{Hdr: dns.RR_Header{Name: "other.local.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: net.IPv4(198, 51, 100, 9)})
			if q.Question[0].Name == dns.Fqdn(name) {
				for _, ip := range ips {
					r.Answer = append(r.Answer, &dns.A{Hdr: dns.RR_Header{Name: dns.Fqdn(name), Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 120}, A: ip})
				}
			}
			b, _ := r.Pack()
			_, _ = conn.WriteToUDP(b, from)
		}
	}()
	return func() int { c := <-count; count <- c; return c }
}

func TestResolveFindsTheName(t *testing.T) {
	answer(t, "homeassistant.local", net.IPv4(192, 0, 2, 20), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ip, err := Resolve(ctx, "HomeAssistant.local")
	if err != nil {
		t.Fatal(err)
	}
	if !ip.Equal(net.IPv4(192, 0, 2, 20)) {
		t.Errorf("got %v", ip)
	}
}

// A question lost is asked again within the wait.
func TestResolveAsksAgain(t *testing.T) {
	asked := answer(t, "homeassistant.local", net.IPv4(192, 0, 2, 20), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ip, err := Resolve(ctx, "homeassistant.local")
	if err != nil {
		t.Fatal(err)
	}
	if !ip.Equal(net.IPv4(192, 0, 2, 20)) || asked() != 2 {
		t.Errorf("got %v after %d questions", ip, asked())
	}
}

// ownNetwork makes 192.0.2.0/24 the device's own network for the test.
func ownNetwork(t *testing.T) {
	t.Helper()
	was := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.IPv4(192, 0, 2, 5), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	t.Cleanup(func() { interfaceAddrs = was })
}

// Answers that cannot be a host are not taken, and of the rest the ones on the device's own network
// come first.
func TestResolveTakesUsableAnswers(t *testing.T) {
	ownNetwork(t)
	answerAll(t, "homeassistant.local", []net.IP{
		net.IPv4(127, 0, 0, 1), net.IPv4(0, 0, 0, 0), net.IPv4(224, 0, 0, 251), net.IPv4(255, 255, 255, 255),
		net.IPv4(169, 254, 3, 4), net.IPv4(192, 0, 2, 255), net.IPv4(198, 51, 100, 7), net.IPv4(192, 0, 2, 20),
		net.IPv4(198, 51, 100, 7),
	}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := ResolveAll(ctx, "homeassistant.local")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || !ips[0].Equal(net.IPv4(192, 0, 2, 20)) || !ips[1].Equal(net.IPv4(198, 51, 100, 7)) {
		t.Errorf("got %v, want [192.0.2.20 198.51.100.7]", ips)
	}
	if ip, err := Resolve(ctx, "homeassistant.local"); err != nil || !ip.Equal(net.IPv4(192, 0, 2, 20)) {
		t.Errorf("Resolve gave %v, %v", ip, err)
	}
}

// An answer with nothing usable in it is no answer.
func TestResolveRefusesOnlyUnusableAnswers(t *testing.T) {
	ownNetwork(t)
	answerAll(t, "homeassistant.local", []net.IP{net.IPv4(127, 0, 0, 1), net.IPv4(169, 254, 3, 4)}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if ip, err := Resolve(ctx, "homeassistant.local"); err == nil {
		t.Errorf("found %v from answers that cannot be a host", ip)
	}
}

func TestResolveGivesUp(t *testing.T) {
	answer(t, "something.local", net.IPv4(1, 2, 3, 4), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if ip, err := Resolve(ctx, "homeassistant.local"); err == nil {
		t.Errorf("found %v for a name nobody answers", ip)
	}
}

func TestWriteHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := WriteHosts(path, map[string]net.IP{"music.local": net.IPv4(192, 0, 2, 30), "HomeAssistant.local": net.IPv4(192, 0, 2, 20)}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := base + "192.0.2.20\thomeassistant.local HomeAssistant.local\n192.0.2.30\tmusic.local\n"
	if string(b) != want {
		t.Errorf("hosts file:\n%s\nwant:\n%s", b, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".hosts-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
