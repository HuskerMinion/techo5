package dns

import (
	"context"
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// nameserver answers every A query on loopback with addr, and any other with no records. It counts the
// queries it was sent. A silent one reads them and never answers, like a router that is down.
type nameserver struct {
	conn    net.PacketConn
	queries atomic.Int32
}

func serve(t *testing.T, silent bool, addr net.IP) *nameserver {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ns := &nameserver{conn: conn}
	t.Cleanup(func() { conn.Close() })
	go func() {
		b := make([]byte, 512)
		for {
			n, from, err := conn.ReadFrom(b)
			if err != nil {
				return
			}
			ns.queries.Add(1)
			if !silent {
				if a := answer(b[:n], addr); a != nil {
					conn.WriteTo(a, from)
				}
			}
		}
	}()
	return ns
}

func (ns *nameserver) addr() string { return ns.conn.LocalAddr().String() }

// answer is the response to query: its header and question, and for an A query one record.
func answer(query []byte, addr net.IP) []byte {
	if len(query) < 12 {
		return nil
	}
	end := 12
	for end < len(query) && query[end] != 0 {
		end += int(query[end]) + 1
	}
	end += 5 // the root label, then the type and class
	if end > len(query) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(query[end-4:])

	out := append([]byte(nil), query[:end]...)
	binary.BigEndian.PutUint16(out[2:], 0x8180) // a response, recursion desired and available
	binary.BigEndian.PutUint16(out[4:], 1)
	binary.BigEndian.PutUint16(out[6:], 0)
	binary.BigEndian.PutUint16(out[8:], 0)
	binary.BigEndian.PutUint16(out[10:], 0)
	if qtype == 1 {
		binary.BigEndian.PutUint16(out[6:], 1)
		out = append(out, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		out = append(out, addr.To4()...)
	}
	return out
}

func lookup(t *testing.T, addrs []string) ([]string, time.Duration) {
	t.Helper()
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialAny(ctx, network, addrs, 200*time.Millisecond)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	got, err := r.LookupHost(ctx, "router.home.test")
	if err != nil {
		t.Fatalf("the lookup failed: %v", err)
	}
	return got, time.Since(start)
}

// A nameserver that is down does not take the lookup with it: the query goes on to the next one. An
// Echo used to send every query to the first nameserver, so with it down nothing resolved at all.
func TestALookupGoesOnWhenTheFirstNameserverIsDown(t *testing.T) {
	down := serve(t, true, nil)
	up := serve(t, false, net.IPv4(192, 0, 2, 7))

	got, took := lookup(t, []string{down.addr(), up.addr()})
	if len(got) != 1 || got[0] != "192.0.2.7" {
		t.Errorf("resolved to %v, want [192.0.2.7]", got)
	}
	if down.queries.Load() == 0 {
		t.Error("the first nameserver was never asked")
	}
	if took > 2*time.Second {
		t.Errorf("the lookup took %v", took)
	}
}

// While the first nameserver answers, the others are never asked.
func TestTheFirstNameserverIsTheOnlyOneAskedWhileItAnswers(t *testing.T) {
	first := serve(t, false, net.IPv4(192, 0, 2, 7))
	second := serve(t, false, net.IPv4(192, 0, 2, 8))

	got, _ := lookup(t, []string{first.addr(), second.addr()})
	if len(got) != 1 || got[0] != "192.0.2.7" {
		t.Errorf("resolved to %v, want [192.0.2.7]", got)
	}
	if n := second.queries.Load(); n != 0 {
		t.Errorf("the second nameserver was asked %d times", n)
	}
}

// A nameserver that refuses outright (nothing listening, so the port is unreachable) is passed over
// at once.
func TestANameserverThatIsNotThereIsPassedOver(t *testing.T) {
	gone := serve(t, true, nil)
	addr := gone.addr()
	gone.conn.Close()
	up := serve(t, false, net.IPv4(192, 0, 2, 7))

	got, _ := lookup(t, []string{addr, up.addr()})
	if len(got) != 1 || got[0] != "192.0.2.7" {
		t.Errorf("resolved to %v, want [192.0.2.7]", got)
	}
}
