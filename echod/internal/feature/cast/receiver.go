// Package cast receives a phone's screen and audio, straight from the phone: no server in between.
// The phone finds the device, connects, proves it knows the pairing key, and sends JPEG frames and
// PCM audio stamped with its own clock; the device works out the difference between the two clocks
// and shows each frame when its time comes, dropping the ones that are late.
//
// This package is the protocol and the timing. What to do with a frame or a sample, and whether to
// take the cast at all, is the Sink's.
package cast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
)

// Sink is what a cast plays on: the screen and the speaker, and the say on whether to start.
type Sink interface {
	// Begin is asked when a phone has proved it has the key. It answers for the device: a refusal
	// (busy, in a call, the person said no) or the screen's size and how long to buffer. It runs
	// before anything is shown or played.
	Begin(h Hello) (Welcome, error)
	// Frame shows a frame, at the moment it is due. img is the Sink's to keep.
	Frame(img image.Image)
	// Audio takes interleaved S16LE stereo PCM that should be heard at at, on the device's clock.
	// pcm is the Sink's to keep.
	Audio(pcm []byte, at time.Time)
	// End is called once when the cast is over, however it ended.
	End()
}

// Receiver holds a port for phones to connect to, one at a time.
type Receiver struct {
	// Key returns the pairing key; read at each connection so a changed setting applies at once.
	Key  func() string
	Sink Sink

	mu   sync.Mutex
	busy bool
	cur  *secure // the phone being served, once it has proved the key

	// The connections that have not proved the key yet, in all and by address. They are counted apart
	// from the cast itself: a stranger who keeps reconnecting can take these, but never the cast's slot,
	// which only a phone with the key gets into.
	pending     int
	pendingFrom map[string]bool

	// Where a phone was turned away by the person at the device, and when, for declineFor.
	declined map[string]time.Time
}

// ErrDeclined is what a Sink's refusal is (errors.Is) when the person at the device said no, or let
// the question go unanswered. The receiver then turns that address away for declineFor without asking
// again, so a phone that once had the key cannot keep the question up on somebody's screen.
var ErrDeclined = errors.New("cast: declined")

// Declined is a refusal that is ErrDeclined, saying reason to the phone.
func Declined(reason string) error { return declined(reason) }

type declined string

func (d declined) Error() string      { return string(d) }
func (declined) Is(target error) bool { return target == ErrDeclined }

// pendingMax is how many connections may be in the handshake at once, and from one address it is one.
// A phone on its way in needs one, so a flood from one address leaves room for the owner's phone, and
// each of these is closed after handshakeFor whatever it does.
const pendingMax = 4

// declineFor is how long an address the person declined is turned away without asking again. A
// variable so that a test need not wait it out.
var declineFor = 30 * time.Second

// Reasons the receiver itself gives a phone that has the key, in a Welcome the app shows as "refused".
const (
	reasonBusy     = "busy with another phone"
	reasonDeclined = "declined a moment ago; try again in a little while"
)

// remoteIP is the address a connection came from, without the port: what the per-address limits count.
func remoteIP(c net.Conn) string {
	a := c.RemoteAddr()
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	if host, _, err := net.SplitHostPort(a.String()); err == nil {
		return host
	}
	return a.String()
}

// Stop ends the cast in progress, telling the phone why.
func (r *Receiver) Stop(reason string) {
	r.mu.Lock()
	c := r.cur
	r.mu.Unlock()
	if c != nil {
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second)) // a stalled phone must not hold the stop
		_ = Write(c, KindStop, []byte(reason))
		c.Close()
	}
}

// handshakeErr is a connection that did not get through the handshake: it has not proved the key.
type handshakeErr struct{ error }

func (e handshakeErr) Unwrap() error { return e.error }

// watchGone says when the connection has closed, by looking at it without reading (MSG_PEEK): what the
// phone sends is left for the session. The receiver's Sink may wait on a person (the question at the
// screen), and nothing else is reading the connection then, so a phone that gives up or is stopped
// would otherwise be found out only when the wait ends. stop ends the watching.
func watchGone(raw net.Conn) (gone <-chan struct{}, stop func()) {
	g, quit := make(chan struct{}), make(chan struct{})
	sc, ok := raw.(syscall.Conn)
	if !ok {
		return g, func() {} // cannot look: never says
	}
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-tick.C:
				if peerClosed(sc) {
					close(g)
					return
				}
			}
		}
	}()
	var once sync.Once
	return g, func() { once.Do(func() { close(quit) }) }
}

// peerClosed is whether the connection has ended, from either side: a read that would return 0 bytes or
// an error, looked at without taking anything from it.
func peerClosed(sc syscall.Conn) bool {
	rc, err := sc.SyscallConn()
	if err != nil {
		return true
	}
	var n int
	var rerr error
	if err := rc.Control(func(fd uintptr) {
		var b [1]byte
		n, _, rerr = syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
	}); err != nil {
		return true // the descriptor is closed
	}
	switch rerr {
	case nil:
		return n == 0
	case syscall.EAGAIN, syscall.EINTR:
		return false
	}
	return true
}

// lateMax is how far past its time a frame is still shown. Later than that, showing it only makes the
// next one later still.
const lateMax = 100 * time.Millisecond

// idle is how long a phone may say nothing, not even its clock, before the cast is ended.
const idle = 10 * time.Second

// farMax is how far ahead of now a stamp may be: the latency is a fraction of a second, so anything
// past this is a phone with a wrong clock, or one making trouble.
const farMax = 2 * time.Second

// queued is how many frames may wait to be decoded and shown; when it is full the oldest goes. It has to
// hold the whole latency at the fastest rate a phone may send, with room over for an offset that
// settled a little high: 250 ms at 60 fps is 15 frames, and at 8 a phone sending 30 fps lost half of
// them to overflow on some runs (measured on the device).
const queued = 64

// videoMax is the largest video frame taken: a full-screen JPEG is a fraction of this. The queue also
// has a byte budget, so a paired phone cannot fill the device's memory with frames, however many it
// sends or however big (a frame over videoMax is refused, and queued frames over queueBytes push the
// oldest out).
const (
	videoMax   = 1 << 20
	queueBytes = 8 << 20
)

// handshakeFor is how long a connection has to prove it knows the key and say hello. Until it has, it
// holds one of the pendingMax places; a phone on the same network answers in well under a second.
const handshakeFor = 3 * time.Second

// Serve accepts phones on ln until ctx ends.
func (r *Receiver) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		ip := remoteIP(c)
		if !r.enter(ip) {
			c.Close() // too many on their way in, or this address already has one: no handshake spent on it
			continue
		}
		go func() {
			// Left as soon as the key is proved (session calls it), or when the connection ends without.
			leave := sync.OnceFunc(func() { r.leave(ip) })
			defer leave()
			if err := r.session(ctx, c, ip, leave); err != nil && ctx.Err() == nil {
				// A connection that never proved the key is a port scan or a phone on another key: one
				// line each at Debug, not a log full of them at Info.
				if errors.As(err, new(handshakeErr)) {
					slog.Debug("cast: a connection that did not get through the handshake", "from", c.RemoteAddr(), "err", err)
					return
				}
				slog.Info("cast ended", "from", c.RemoteAddr(), "err", err)
			}
		}()
	}
}

// enter takes a place for a connection that has yet to prove the key: false when all pendingMax are
// taken, or ip already has one.
func (r *Receiver) enter(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending >= pendingMax || r.pendingFrom[ip] {
		return false
	}
	if r.pendingFrom == nil {
		r.pendingFrom = map[string]bool{}
	}
	r.pending++
	r.pendingFrom[ip] = true
	return true
}

func (r *Receiver) leave(ip string) {
	r.mu.Lock()
	r.pending--
	delete(r.pendingFrom, ip)
	r.mu.Unlock()
}

// take is the one cast's slot, which covers the question at the screen as well as the cast.
func (r *Receiver) take() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.busy {
		return false
	}
	r.busy = true
	return true
}

func (r *Receiver) give() {
	r.mu.Lock()
	r.busy, r.cur = false, nil
	r.mu.Unlock()
}

// decline remembers that the person turned ip away, and forgets those whose time is up.
func (r *Receiver) decline(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for k, at := range r.declined {
		if now.Sub(at) >= declineFor {
			delete(r.declined, k)
		}
	}
	if r.declined == nil {
		r.declined = map[string]time.Time{}
	}
	r.declined[ip] = now
}

// recentlyDeclined is whether ip was turned away by the person less than declineFor ago.
func (r *Receiver) recentlyDeclined(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.declined[ip]
	return ok && time.Since(at) < declineFor
}

// refuse answers a hello with a refusal, for a phone that has the key but is not let in.
func refuse(raw net.Conn, c *secure, reason string) error {
	out, _ := json.Marshal(Welcome{OK: false, Reason: reason})
	_ = raw.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := Write(c, KindWelcome, out); err != nil {
		return err
	}
	return errors.New("refused: " + reason)
}

// session serves one connection. proved is called once it has shown it has the key and said hello,
// which frees its place among those on their way in.
func (r *Receiver) session(ctx context.Context, raw net.Conn, ip string, proved func()) error {
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()

	_ = raw.SetDeadline(time.Now().Add(handshakeFor))
	key := r.Key()
	if len(key) < KeyLeast {
		return errors.New("no pairing key set")
	}
	c, err := accept(raw, key)
	if err != nil {
		return handshakeErr{err}
	}
	kind, payload, err := Read(c)
	if err != nil {
		return err
	}
	if kind != KindHello {
		return errors.New("the first message was not a hello")
	}
	proved()

	// Only a phone with the key gets this far, so it is told why it is not let in, where a stranger is
	// only ever closed on. The app shows the reason as the Show's refusal.
	if r.recentlyDeclined(ip) {
		return refuse(raw, c, reasonDeclined)
	}
	if !r.take() {
		return refuse(raw, c, reasonBusy)
	}
	defer r.give()
	r.mu.Lock()
	r.cur = c
	r.mu.Unlock()
	var h Hello
	if err := json.Unmarshal(payload, &h); err != nil {
		return errors.New("a hello that is not JSON")
	}
	h.Name = clean(h.Name)
	h.Title = printable(h.Title, 80)
	var stopWatch func()
	h.Gone, stopWatch = watchGone(raw)
	defer stopWatch()
	if h.Scale == 0 {
		h.Scale = 1
	}
	if h.Scale != 1 && h.Scale != 2 {
		return errors.New("a hello with a scale that is neither 1 nor 2")
	}

	// Begin may wait for a person to say yes (askTimeout), so the handshake's deadline is not the one
	// the answer is written under.
	_ = raw.SetDeadline(time.Time{})
	w, err := r.Sink.Begin(h)
	stopWatch() // only Begin waits on a person: the read loop notices a closed phone from here on
	if err != nil {
		w = Welcome{OK: false, Reason: err.Error()}
		if errors.Is(err, ErrDeclined) {
			r.decline(ip)
		}
	}
	out, _ := json.Marshal(w)
	_ = raw.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := Write(c, KindWelcome, out); err != nil {
		if w.OK {
			r.Sink.End() // Begin took the screen and the speaker; the phone left before hearing so
		}
		return err
	}
	_ = raw.SetWriteDeadline(time.Time{}) // only the read side has a deadline for the rest of the cast
	if !w.OK {
		return errors.New("refused: " + w.Reason)
	}
	defer r.Sink.End()
	slog.Info("cast started", "from", raw.RemoteAddr(), "phone", h.Name)

	s := &play{
		clockSync: clockSync{start: time.Now()}, sink: r.Sink, w: w, h: h,
		latency: time.Duration(w.LatencyMs) * time.Millisecond,
		frames:  make(chan frame, queued),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.show() }()
	defer func() { s.over.Store(true); close(s.frames); wg.Wait() }()

	// How it is going, once a second, so the phone can ease off before the picture suffers. A phone that
	// does not know the message ignores it. The writer stops with the connection, which closes when this
	// returns.
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				out, _ := json.Marshal(s.stats())
				if Write(c, KindStats, out) != nil {
					return
				}
			}
		}
	}()

	for {
		_ = raw.SetReadDeadline(time.Now().Add(idle))
		kind, payload, err := Read(c)
		if err != nil {
			return err
		}
		switch kind {
		case KindClock:
			if us, _, ok := Split(payload); ok {
				s.observe(us)
			}
		case KindVideo:
			if h.Video {
				s.video(payload)
			}
		case KindAudio:
			if h.Audio {
				s.audio(payload)
			}
		case KindBye:
			return nil
		}
	}
}

// clean makes a phone's name safe to put on a screen: printable, and short.
func clean(name string) string {
	if name = printable(name, 40); name == "" {
		name = "A phone"
	}
	return name
}

// printable is s with what cannot be drawn taken out, cut to at most n characters.
func printable(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.TrimSpace(s)
}

// clockWindow is how many clock messages the offset is taken over.
const clockWindow = 10

// clockSync is the difference between a sender's clock and this one, worked out from its clock
// messages. Stamps are on the sender's clock; the device only needs the difference.
type clockSync struct {
	start time.Time

	mu      sync.Mutex
	samples [clockWindow]int64 // arrival minus the sender's stamp, µs; the smallest carries least delay
	n       int
	offset  int64
	known   bool
}

// now is this device's clock in µs since the sync began.
func (c *clockSync) now() int64 { return time.Since(c.start).Microseconds() }

// observe takes a clock message. The network only ever adds delay, so of the last few the smallest
// difference is the truest.
func (c *clockSync) observe(peerUs int64) {
	d := c.now() - peerUs
	c.mu.Lock()
	defer c.mu.Unlock()
	c.samples[c.n%clockWindow] = d
	c.n++
	lo := d
	for i := 0; i < min(c.n, clockWindow); i++ {
		lo = min(lo, c.samples[i])
	}
	c.offset, c.known = lo, true
}

// at is when a stamp falls on this device's clock; false before the first clock message, when
// nothing can be placed.
func (c *clockSync) at(peerUs int64) (time.Time, bool) {
	c.mu.Lock()
	off, ok := c.offset, c.known
	c.mu.Unlock()
	if !ok {
		return time.Time{}, false
	}
	return c.start.Add(time.Duration(peerUs+off) * time.Microsecond), true
}

// play is one cast's timing: the offset between the phone's clock and this one, and the frames
// waiting for their moment.
type play struct {
	clockSync
	sink    Sink
	w       Welcome
	h       Hello
	latency time.Duration
	frames  chan frame
	queuedB atomic.Int64 // bytes in frames, for queueBytes
	over    atomic.Bool  // the cast has ended: what is still queued is not shown

	// Why frames did not get shown, for the log: late on arrival, thrown out of a full queue, skipped
	// undecoded behind a newer one, late once decoded, and not decodable.
	lateIn, overflow, skipped, lateOut, bad atomic.Int64
	shown                                   atomic.Int64 // frames put on the screen
}

// due is when a stamp is to be presented, on this device's clock; false before the first clock
// message, when nothing can be placed.
func (p *play) due(phoneUs int64) (time.Time, bool) {
	t, ok := p.at(phoneUs)
	return t.Add(p.latency), ok
}

type frame struct {
	due time.Time
	jpg []byte
}

func (p *play) video(payload []byte) {
	us, jpg, ok := Split(payload)
	if !ok {
		return
	}
	if len(jpg) > videoMax {
		p.bad.Add(1)
		return
	}
	due, ok := p.due(us)
	if !ok {
		return
	}
	if time.Since(due) > lateMax {
		p.lateIn.Add(1)
		return
	}
	if time.Until(due) > farMax {
		p.bad.Add(1) // a stamp from nowhere near now, like audio's: it would sit in the queue
		return
	}
	f := frame{due: due, jpg: jpg}
	for {
		if p.queuedB.Load()+int64(len(jpg)) <= queueBytes {
			select {
			case p.frames <- f:
				p.queuedB.Add(int64(len(jpg)))
				return
			default:
			}
		}
		select { // full, in frames or in bytes: the oldest is the one least worth showing
		case old := <-p.frames:
			p.queuedB.Add(-int64(len(old.jpg)))
			p.overflow.Add(1)
		default:
		}
	}
}

func (p *play) audio(payload []byte) {
	us, pcm, ok := Split(payload)
	if !ok || len(pcm) == 0 || len(pcm)%4 != 0 {
		return
	}
	due, ok := p.due(us)
	if !ok {
		return
	}
	if time.Until(due) > farMax {
		return
	}
	p.sink.Audio(pcm, due)
}

// show decodes frames and puts each up when its time comes. A frame whose time has gone by when the
// next is already waiting is skipped without being decoded: on a slow device that is what keeps the
// picture on the present.
func (p *play) show() {
	report := time.Now()
	var last [5]int64 // the counters at the last line in the log, which says what changed since
	for f := range p.frames {
		p.queuedB.Add(-int64(len(f.jpg)))
		if p.over.Load() {
			continue // the cast ended (a Stop, a Bye, a dropped connection): no more of it on the screen
		}
		if time.Since(report) >= 5*time.Second {
			cur := [5]int64{p.lateIn.Load(), p.overflow.Load(), p.skipped.Load(), p.lateOut.Load(), p.bad.Load()}
			slog.Info("cast frames", "late_on_arrival", cur[0]-last[0], "overflow", cur[1]-last[1],
				"skipped", cur[2]-last[2], "late_after_decode", cur[3]-last[3], "bad", cur[4]-last[4])
			last, report = cur, time.Now()
		}
		if len(p.frames) > 0 && time.Since(f.due) > 0 {
			p.skipped.Add(1)
			continue
		}
		img, err := p.decode(f.jpg)
		if err != nil {
			p.bad.Add(1)
			continue
		}
		if wait := time.Until(f.due); wait > 0 {
			if wait > farMax {
				continue // a stamp from nowhere near now: not something to sleep on
			}
			time.Sleep(wait)
		} else if -wait > lateMax {
			p.lateOut.Add(1)
			continue
		}
		p.sink.Frame(img)
		p.shown.Add(1)
	}
}

// AudioCounter is what a Sink may add to have its audio's misses in the stats it reports.
type AudioCounter interface {
	AudioMisses() (late, dropped int)
}

// stats is the report for the phone: totals since the cast began.
func (p *play) stats() Stats {
	st := Stats{
		Shown:   int(p.shown.Load()),
		Dropped: int(p.lateIn.Load() + p.overflow.Load() + p.skipped.Load() + p.lateOut.Load() + p.bad.Load()),
	}
	if a, ok := p.sink.(AudioCounter); ok {
		st.AudioLate, st.AudioDropped = a.AudioMisses()
	}
	return st
}

// decode reads a frame's size before decoding it, so a picture claiming to be enormous gets nothing
// decoded rather than all of memory.
func (p *play) decode(b []byte) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > p.w.W/p.h.Scale || cfg.Height > p.w.H/p.h.Scale {
		return nil, errors.New("cast: a frame larger than the screen")
	}
	return jpeg.Decode(bytes.NewReader(b))
}
