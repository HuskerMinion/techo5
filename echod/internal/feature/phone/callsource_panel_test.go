//go:build panel

package phone

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
)

// A panel rings for a call only when it came from the provider.
func TestAPanelRefusesACallFromElsewhere(t *testing.T) {
	p := newProvider("sip.provider.example")
	p.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}, nil
	}
	if !p.admits("192.0.2.10:5061") {
		t.Error("a panel refused the provider's call")
	}
	if p.admits("198.51.100.7:5060") {
		t.Error("a panel rang for a call from elsewhere on the network")
	}
}

// Anything on the home network can send the panel's listener a REGISTER's 200 it never asked for, so
// one does not make its sender the provider: that sender's INVITE is still refused. The provider here
// is 127.0.0.1, named by its address (localhost can resolve to ::1 first, which would put the listener
// on IPv6), and the stranger another loopback address.
func TestAPanelDoesNotTakeAStrangersRegisterOKForTheProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rang := make(chan string, 4)
	l, err := open(ctx, Account{Server: "127.0.0.1", Username: "panel", Password: "x", Plain: true}, func(d *diago.DialogServerSession) {
		rang <- d.InviteRequest.MessageData.Source()
		_ = d.Respond(sip.StatusBusyHere, "Busy Here", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	// The port is put down just after the listener reports ready, so it is waited for (ListenPorts
	// takes the lock GetListenPort does not).
	var port int
	for range 100 {
		if ports := l.ua.TransportLayer().ListenPorts("udp"); len(ports) > 0 {
			port = ports[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if port == 0 {
		t.Fatal("the listener has no port")
	}
	listener := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	stranger, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.2")})
	if err != nil {
		t.Skip("no second loopback address here:", err)
	}
	defer stranger.Close()
	provider, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()

	ok := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:" + strconv.Itoa(port) + ";branch=z9hG4bK-unasked\r\n" +
		"From: <sip:panel@localhost>;tag=a\r\n" +
		"To: <sip:panel@localhost>;tag=b\r\n" +
		"Call-ID: unasked-register\r\n" +
		"CSeq: 2 REGISTER\r\n" +
		"Contact: <sip:panel@127.0.0.1:" + strconv.Itoa(port) + ">;expires=300\r\n" +
		"Content-Length: 0\r\n\r\n"
	if _, err := stranger.WriteToUDP([]byte(ok), listener); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the 200 is taken in before the INVITE

	if got := invite(t, stranger, listener, "stranger"); got != sip.StatusForbidden {
		t.Errorf("a stranger that sent a REGISTER's 200 had its call answered %d, want %d", got, sip.StatusForbidden)
	}
	select {
	case from := <-rang:
		t.Errorf("a panel rang for a call from %s", from)
	default:
	}

	// The same INVITE from the provider rings, so the refusal above was the source check.
	if got := invite(t, provider, listener, "provider"); got != sip.StatusBusyHere {
		t.Errorf("the provider's call was answered %d, want %d", got, sip.StatusBusyHere)
	}
}

// invite sends an INVITE from c to the listener and returns the final response's status.
func invite(t *testing.T, c *net.UDPConn, listener *net.UDPAddr, id string) int {
	t.Helper()
	me := c.LocalAddr().String()
	req := "INVITE sip:panel@" + listener.String() + " SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP " + me + ";branch=z9hG4bK-" + id + "\r\n" +
		"Max-Forwards: 70\r\n" +
		"From: <sip:caller@" + me + ">;tag=" + id + "\r\n" +
		"To: <sip:panel@" + listener.String() + ">\r\n" +
		"Call-ID: " + id + "-call\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Contact: <sip:caller@" + me + ">\r\n" +
		"Content-Length: 0\r\n\r\n"
	if _, err := c.WriteToUDP([]byte(req), listener); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("no answer to the %s's INVITE: %v", id, err)
		}
		var code int
		if _, err := fmt.Sscanf(string(buf[:n]), "SIP/2.0 %d", &code); err != nil {
			t.Fatalf("not a response to the %s's INVITE: %q", id, strings.SplitN(string(buf[:n]), "\r\n", 2)[0])
		}
		if code >= 200 {
			return code
		}
	}
}
