package phone

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/sealed"
)

// The intercom: a call from one device in the house to another, with no provider, no Home Assistant
// and no internet (docs/intercom-plan.md). It is a second kind of line on the phone: the same call
// state, the same ringing, the same call page and answer button, only reached another way.
//
// A call is one connection to the other device's web port, taken over from HTTP at /intercom and
// then sealed with the house word: the word that announcing already uses. A device with no word takes
// no calls, and a caller with the wrong word fails the handshake before anything is said. Inside,
// each message is a type, a length and the payload; the audio is the microphone's 16 kHz as it is.

const (
	intercomPath     = "/intercom"
	intercomProtocol = "techo5-intercom/1" // the Upgrade asked for, and the handshake's prologue
	intercomLabel    = "techo5-intercom psk"

	// intercomRingFor is how long a call from another room rings: someone in the house is either
	// there or not, and half a minute is long enough to walk to it.
	intercomRingFor = 30 * time.Second

	// handshakeFor bounds the part of a call before it rings, so a connection that says nothing
	// does not hold the line.
	handshakeFor = 5 * time.Second

	// declineHold is how long a device whose call was turned down must wait before ringing here again.
	declineHold = 30 * time.Second

	nameMost    = 40 // runes of a caller's name that are shown
	payloadMost = 2 * wideFrame
)

// What goes back and forth, one byte each.
const (
	msgHello   = 'H' // the caller's name
	msgRinging = 'R'
	msgBusy    = 'B' // already on a call
	msgAnswer  = 'A'
	msgDecline = 'D' // declined, or nobody answered
	msgNotNow  = 'N' // do not disturb
	msgAudio   = 'S' // 20 ms of 16 kHz
	msgBye     = 'X' // hung up
)

func intercomOpen() bool { return config.Get().Home.HouseWord != "" }

func intercomKey() []byte { return sealed.Key(intercomLabel, config.Get().Home.HouseWord) }

func writeMsg(w io.Writer, t byte, payload []byte) error {
	b := make([]byte, 3+len(payload))
	b[0] = t
	binary.BigEndian.PutUint16(b[1:], uint16(len(payload)))
	copy(b[3:], payload)
	_, err := w.Write(b)
	return err
}

func readMsg(r io.Reader) (byte, []byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[1:]))
	if n > payloadMost {
		return 0, nil, fmt.Errorf("intercom: a message of %d bytes", n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return hdr[0], b, err
}

// shownName is a caller's name as the screen may show it: printable, and not too long.
func shownName(b []byte) string {
	s := strings.Map(func(r rune) rune {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, string(b))
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > nameMost {
		s = string([]rune(s)[:nameMost])
	}
	if s == "" {
		return "Another room"
	}
	return s
}

// link is one intercom call's connection, read by one goroutine that sorts what arrives: the audio
// to the speaker's side, everything else to whoever is waiting on the call.
type link struct {
	c       *sealed.Conn
	audio   chan []byte
	control chan byte
	gone    chan struct{} // closed when the other end hangs up or the connection ends

	closeOnce sync.Once
}

func newLink(c *sealed.Conn) *link {
	l := &link{c: c, audio: make(chan []byte, 25), control: make(chan byte, 4), gone: make(chan struct{})}
	safe.Go("intercom: read", func() {
		defer close(l.gone)
		for {
			t, b, err := readMsg(c)
			if err != nil || t == msgBye {
				return
			}
			switch t {
			case msgAudio:
				select {
				case l.audio <- b:
				default: // the speaker is behind; this frame is lost rather than everything after it late
				}
			default:
				select {
				case l.control <- t:
				default:
				}
			}
		}
	})
	return l
}

func (l *link) say(t byte) error { return writeMsg(l.c, t, nil) }

func (l *link) close() {
	l.closeOnce.Do(func() {
		_ = l.say(msgBye)
		_ = l.c.Close()
	})
}

// Write is the microphones going out.
func (l *link) Write(p []byte) (int, error) {
	if err := writeMsg(l.c, msgAudio, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read is the far end coming in, until it hangs up.
func (l *link) Read(p []byte) (int, error) {
	select {
	case b := <-l.audio:
		return copy(p, b), nil
	case <-l.gone:
		return 0, io.EOF
	}
}

// goneContext is a context that ends when the other end does.
func (l *link) goneContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-l.gone
		cancel()
	}()
	return ctx
}

// intercomIn is a call from another device, on the web port.
func (p *Phone) intercomIn(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), intercomProtocol) {
		http.Error(w, "an intercom call only", http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot take the connection", http.StatusInternalServerError)
		return
	}
	raw, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer raw.Close()
	conn := &bufConn{Conn: raw, r: rw.Reader}
	_ = raw.SetDeadline(time.Now().Add(handshakeFor))
	if _, err := fmt.Fprintf(raw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n", intercomProtocol); err != nil {
		return
	}
	c, err := sealed.Server(conn, intercomProtocol, intercomKey())
	if err != nil {
		slog.Warn("intercom: call refused: the house word did not match", "from", r.RemoteAddr, "err", err)
		return
	}
	t, name, err := readMsg(c)
	if err != nil || t != msgHello {
		return
	}
	_ = raw.SetDeadline(time.Time{})
	caller := shownName(name)

	home := config.Get().Home
	if home.DoNotDisturb {
		slog.Info("intercom: call turned away: do not disturb", "from", caller)
		_ = writeMsg(c, msgNotNow, nil)
		return
	}
	p.mu.Lock()
	recent := time.Since(p.declined[caller]) < declineHold
	p.mu.Unlock()
	if recent {
		slog.Info("intercom: call turned away: declined moments ago", "from", caller)
		_ = writeMsg(c, msgDecline, nil)
		return
	}

	answered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.mu.Lock()
	busy := p.state.Phase != Idle
	if !busy {
		p.answered, p.end = answered, cancel
		p.claim(Ringing, caller, true)
		p.state.Intercom, p.state.DropIn = true, home.DropIn
	}
	p.mu.Unlock()
	if busy {
		slog.Info("intercom: call refused, already on one", "from", caller)
		_ = writeMsg(c, msgBusy, nil)
		return
	}

	l := newLink(c)
	defer l.close()
	_ = l.say(msgRinging)
	p.set(func(*State) {})
	slog.Info("intercom: ringing", "from", caller)
	fire("ringing", p.State())

	rctx, stopRing := context.WithCancel(ctx)
	if home.DropIn {
		// Drop In: a chime rather than a ring, and then it is answered by itself. The call page says
		// who is listening, and hanging up or declining still ends it.
		slog.Info("intercom: drop in", "from", caller)
		safe.Go("intercom: chime", func() {
			chime(rctx)
			p.Answer()
		})
	} else {
		safe.Go("intercom: ring", func() { ring(rctx) })
	}
	timeout := time.NewTimer(intercomRingFor)
	defer timeout.Stop()
	select {
	case <-answered:
		stopRing()
		if err := l.say(msgAnswer); err != nil {
			fire("ended", p.State(), "reason", "answer failed")
			p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
			return
		}
		p.talk(ctx, l.goneContext(), func(tctx context.Context) error { return carry(tctx, l, l, true, p.say) },
			func(context.Context) error { l.close(); return nil })
	case <-ctx.Done():
		stopRing()
		_ = l.say(msgDecline)
		p.mu.Lock()
		if p.declined == nil {
			p.declined = map[string]time.Time{}
		}
		p.declined[caller] = time.Now()
		p.mu.Unlock()
		fire("declined", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	case <-l.gone:
		stopRing()
		slog.Info("intercom: missed call", "from", caller)
		fire("missed", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	case <-timeout.C:
		stopRing()
		_ = l.say(msgDecline)
		slog.Info("intercom: missed call", "from", caller)
		fire("missed", p.State())
		p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
	}
}

// CallDevice calls another device in the house by its name. It returns once the call is under way;
// what happens to it is reported through State and the Home Assistant event, as a phone call's is.
func (p *Phone) CallDevice(name string) error {
	name = strings.TrimSpace(name)
	if !intercomOpen() {
		return errors.New("intercom: this device has no house word; set one on the setup page")
	}
	peer, ok := findPeer(name)
	if !ok {
		return fmt.Errorf("intercom: no device called %q in the house right now", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	busy := p.state.Phase != Idle
	if !busy {
		p.end = cancel
		p.claim(Dialing, peer.Name, false)
		p.state.Intercom = true
	}
	p.mu.Unlock()
	if busy {
		cancel()
		return errors.New("intercom: already on a call")
	}
	p.set(func(*State) {})
	slog.Info("intercom: calling", "device", peer.Name)
	fire("dialing", p.State())

	safe.Go("intercom: call", func() {
		defer cancel()
		unanswered := time.AfterFunc(dialFor, cancel)
		l, err := dialDevice(ctx, peer, intercomKey())
		if err != nil {
			unanswered.Stop()
			// A house word that does not match ends the connection at the other end, which is all
			// this end sees of it; the other end's log says why.
			reason := "failed"
			if ctx.Err() != nil {
				reason = "cancelled"
			}
			slog.Info("intercom: call not answered", "device", peer.Name, "err", err)
			fire("not_answered", p.State(), "reason", reason)
			p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
			return
		}
		defer l.close()
		reason := ""
	wait:
		for {
			select {
			case t := <-l.control:
				switch t {
				case msgAnswer:
					break wait
				case msgBusy:
					reason = "busy"
				case msgDecline:
					reason = "declined"
				case msgNotNow:
					reason = "do not disturb"
				default:
					continue
				}
				break wait
			case <-l.gone:
				reason = "failed"
				break wait
			case <-ctx.Done():
				reason = "cancelled"
				break wait
			}
		}
		unanswered.Stop()
		if reason != "" {
			slog.Info("intercom: call not answered", "device", peer.Name, "reason", reason)
			fire("not_answered", p.State(), "reason", reason)
			p.set(func(s *State) { s.Phase, s.Peer = Idle, "" })
			return
		}
		p.talk(ctx, l.goneContext(), func(tctx context.Context) error { return carry(tctx, l, l, true, p.say) },
			func(context.Context) error { l.close(); return nil })
	})
	return nil
}

// chime is Drop In's sound, once: two short rising notes, where a call rings.
func chime(ctx context.Context) {
	const rate = speaker.VoiceRate
	var tone []int16
	for _, n := range []struct {
		f1, f2 float64
		ms     int
	}{{660, 880, 160}, {0, 0, 70}, {880, 1100, 220}} {
		count := rate * n.ms / 1000
		for i := 0; i < count; i++ {
			if n.f1 == 0 {
				tone = append(tone, 0)
				continue
			}
			t := float64(i) / rate
			env := math.Min(1, math.Min(float64(i), float64(count-i))/(rate/100))
			tone = append(tone, int16(8000*env*(0.5*math.Sin(2*math.Pi*n.f1*t)+0.5*math.Sin(2*math.Pi*n.f2*t))))
		}
	}
	claim := speaker.Sound().Claim("drop in", func(cctx context.Context, pl *speaker.Player) error {
		pl.PlayVoice(tone)
		for pl.Queued() > 0 {
			select {
			case <-cctx.Done():
				pl.Drain()
				return nil
			case <-ctx.Done():
				pl.Drain()
				return nil
			case <-time.After(50 * time.Millisecond):
			}
		}
		return nil
	})
	<-claim.Done()
}

// findPeer is the device in the house with this name, however it is capitalized.
func findPeer(name string) (announce.Peer, bool) {
	for _, pe := range announce.Peers() {
		if strings.EqualFold(pe.Name, name) {
			return pe, true
		}
	}
	return announce.Peer{}, false
}

// dialDevice opens a call's connection to a device, and returns once it is ringing there, or says why
// it is not.
func dialDevice(ctx context.Context, peer announce.Peer, key []byte) (*link, error) {
	port := peer.Port
	if port == 0 {
		port = web.Port
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(peer.Address, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(handshakeFor))
	if _, err := fmt.Fprintf(raw, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n",
		intercomPath, peer.Address, intercomProtocol); err != nil {
		raw.Close()
		return nil, err
	}
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		raw.Close()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		raw.Close()
		return nil, fmt.Errorf("intercom: %s takes no calls (%s)", peer.Name, resp.Status)
	}
	c, err := sealed.Client(&bufConn{Conn: raw, r: br}, intercomProtocol, key)
	if err != nil {
		raw.Close()
		return nil, err
	}
	if err := writeMsg(c, msgHello, []byte(config.Get().Device.Name)); err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return newLink(c), nil
}

// bufConn reads through the buffer HTTP left behind, so nothing that arrived with the headers is lost.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }
