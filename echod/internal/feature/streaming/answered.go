package streaming

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// Watching that avahi still answers for AirPlay (techo5-dot#14). On a Dot, after an AirPlay session
// ended, phones listed the speaker for a minute or two and then never again, while shairport-sync ran
// on (it never stopped, by the log). A minute or two is as long as avahi's records last without being
// asked for again (two minutes for the host and the service's address), and gone without a goodbye
// they were let run out: which fits avahi no longer answering, though why is not known. Turning AirPlay
// off and on, which starts avahi and the receivers afresh, brought it back every time. So the group
// asks avahi, every answerEvery, for AirPlay's service the way a phone does, and when its own avahi has
// not answered for answerMisses asks in a row, starts avahi and the receivers again, as the switch would.
//
// The ask is an mDNS query sent to the network from a port of its own (a legacy unicast query, which a
// responder answers straight back to that port): avahi sees it as it sees a phone's, on the Wi-Fi it
// announces on, and answers only from the services registered with it. An answer counts only from one of
// the device's own addresses, since any other AirPlay speaker on the network answers too.
//
// The watch stands down where it proves nothing. It counts no miss until avahi has answered once since it
// started. An avahi that has not answered at all for neverHeardAfter asks is started again too, since
// one can come up without AirPlay's service registered and stay that way across reboots (techo5-dot#22),
// but only neverHeardTries times in a row: an image where avahi cannot answer the ask stops being
// restarted for it after that, and an answer at any point resets the count. And the watch restarts
// nothing while a receiver is sending audio (receivedWithin), which a restart would cut off: a phone
// playing to the speaker has found it already.
var (
	answerEvery     = time.Minute
	answerMisses    = 3
	neverHeardAfter = 10
	neverHeardTries = int32(3)
	answered        = airplayAnswered
	canAsk          = routeToGroup
)

// neverHeardUsed is how many times in a row avahi has been started again for never answering.
var neverHeardUsed atomic.Int32

// pausedFor is how long after a receiver's last audio a restart still waits: a session that is only
// paused would be cut too, and the phone that holds it has found the speaker already.
var pausedFor = 10 * time.Minute

// routeToGroup is whether an ask can leave at all: with Wi-Fi down there is no route to the group,
// and an outage is not avahi failing. Linux keeps the route of a link that lost its carrier until the
// address goes, so wlan0's own state is asked first where there is one.
func routeToGroup() bool {
	if st, err := os.ReadFile("/sys/class/net/wlan0/operstate"); err == nil && strings.TrimSpace(string(st)) != "up" {
		return false
	}
	c, err := net.DialUDP("udp4", nil, mdnsGroup)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// answerWait is how long an ask waits for avahi's answer.
var answerWait = 2 * time.Second

// mdnsGroup is where the ask is sent: mDNS's IPv4 group. A variable for the tests.
var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// airplayService is the service AirPlay is registered as with avahi (shairport-sync's classic AirPlay).
const airplayService = "_raop._tcp.local"

// watchAnswers asks avahi for AirPlay every answerEvery until ctx ends, and closes gone, and returns,
// once avahi has stopped answering (answered, above).
func watchAnswers(ctx context.Context, gone chan<- struct{}) {
	heard, misses, silent := false, 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(answerEvery):
		}
		if answered(ctx) {
			if !heard {
				slog.Info("streaming: avahi answers for AirPlay; watching that it goes on")
			}
			heard, misses = true, 0
			neverHeardUsed.Store(0)
			continue
		}
		if ctx.Err() != nil || !canAsk() {
			continue
		}
		if !heard {
			silent++
			if silent >= neverHeardAfter && neverHeardUsed.Load() < neverHeardTries && !receivedWithin(pausedFor) {
				n := neverHeardUsed.Add(1)
				slog.Warn("streaming: avahi has not answered for AirPlay since it started; starting it and the receivers again",
					"asked", silent, "try", n, "of", neverHeardTries)
				close(gone)
				return
			}
			continue
		}
		misses++
		if misses >= answerMisses && !receivedWithin(pausedFor) {
			slog.Warn("streaming: avahi no longer answers for AirPlay; starting it and the receivers again", "asked", misses)
			close(gone)
			return
		}
	}
}

// airplayAnswered is whether the device's own avahi answers a query for AirPlay's service with at least
// one record, within answerWait.
func airplayAnswered(ctx context.Context) bool {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return false
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	id := uint16(rand.Uint32())
	if _, err := conn.WriteToUDP(mdnsQuery(id, airplayService), mdnsGroup); err != nil {
		return false
	}
	_ = conn.SetReadDeadline(time.Now().Add(answerWait))
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			// The wait over, or ctx ended.
			return false
		}
		if from.Port == mdnsGroup.Port && netguard.OwnIP(from.IP) && mdnsAnswers(buf[:n], id) {
			return true
		}
	}
}

// mdnsQuery is a DNS query with the id given for name's PTR records, the way a browser asks who offers a
// service.
func mdnsQuery(id uint16, name string) []byte {
	b := make([]byte, 12, 12+len(name)+6)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[4:], 1) // one question
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0)
	return append(b, 0, 12, 0, 1) // PTR, IN
}

// mdnsAnswers is whether p is a response to the query with id that carries at least one answer.
func mdnsAnswers(p []byte, id uint16) bool {
	if len(p) < 12 || binary.BigEndian.Uint16(p[0:]) != id {
		return false
	}
	return p[2]&0x80 != 0 && binary.BigEndian.Uint16(p[6:]) > 0
}

// receivedAt is when a receiver last sent audio, in Unix nanoseconds; 0 for never.
var receivedAt atomic.Int64

// received notes n bytes of a receiver's audio just read.
func received(n int) {
	if n > 0 {
		receivedAt.Store(time.Now().UnixNano())
	}
}

// receivedWithin is whether a receiver sent audio within d.
func receivedWithin(d time.Duration) bool {
	at := receivedAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) < d
}
