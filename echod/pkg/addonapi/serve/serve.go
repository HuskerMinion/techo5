// Package serve is the daemon's end of the add-on API (docs/addon-api.md): a Unix socket for each
// running add-on, the check of who connects, the framing, the limits, and the check of every request
// against what the add-on was granted. What a request does on the device is the Device's: the daemon
// gives it the real one (feature/addonapi), and the fake daemon for authors (cmd/techo5-addon-fake) one
// that logs, so an author tests against this very code.
//
// What it trusts is the kernel. The add-on's id comes from the user the connection's other end runs as
// (SO_PEERCRED), checked against the one its socket was made for, never from anything in a message; the
// socket's folder belongs to that user alone, mode 0700; and root is refused, so a root shell cannot
// pose as an add-on, though nothing could keep it out of the folder.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// DefaultDir is where the sockets are: /run is RAM, so nothing of the API touches the flash.
const DefaultDir = "/run/techo5-addons"

// SocketName is the socket's name in each add-on's folder.
const SocketName = "api.sock"

// Abuse: an add-on whose requests are refused for its limits this many times within strikeWindow is
// disconnected, and its connections refused, for banFor.
var (
	strikeWindow = time.Minute
	banFor       = time.Minute
)

// strikeLimit is how many refusals within strikeWindow disconnect an add-on. Atomic, as a test changes
// it while the connections of the test before may still be reading it.
var strikeLimit = func() *atomic.Int64 { var n atomic.Int64; n.Store(20); return &n }()

// Deadlines: the rest of a message once its length has arrived, and each write to an add-on. One that
// sends half a message, or stops reading, is dropped rather than holding a goroutine for ever.
var (
	bodyWithin  = 5 * time.Second
	writeWithin = 5 * time.Second
)

// skipMost is the most of an oversized message read and thrown away to answer it too_large and carry
// on; one claiming more is hung up on, as reading it would be the flood the limit is there to stop.
const skipMost = 1 << 20

// outQueue is how many messages may wait to be written to one connection. An add-on that leaves that
// many unread is not reading, and is disconnected rather than letting its queue grow.
const outQueue = 128

// Grant is an installed add-on as the API serves it: who it runs as, and what it was granted when the
// owner agreed to it, with what its addon.json declares for those.
type Grant struct {
	ID       string
	UID      uint32
	Caps     []string
	Commands []addonapi.Command
	Entities []addonapi.Entity
	Settings *addonapi.Settings
	// Files are its extra files' names, and BinDir the folder they are in: a WAV among them is a sound
	// it may play.
	Files  []string
	BinDir string
}

// Has is whether the grant includes the capability.
func (g Grant) Has(c string) bool { return slices.Contains(g.Caps, c) }

// Device is what the API does on the device. Its methods are called outside the server's lock, from
// the goroutine serving the connection, and may take a moment; Say blocks until the words are said.
type Device interface {
	// Info is what hello tells an add-on about the device.
	Info() addonapi.Device
	// Settings is the add-on's settings, as saved.
	Settings(id string) map[string]any
	// Connected is whether Home Assistant is connected to the device.
	Connected() bool
	// Quiet is whether it is quiet hours, when an add-on makes no sound.
	Quiet() bool
	// Say has Home Assistant say text on the device, returning once it has been said (or stopped), or
	// when ctx ends.
	Say(ctx context.Context, id, text string) error
	// PlaySound plays one of addonapi.Sounds by name, or wav when it is not nil.
	PlaySound(id, name string, wav *addonapi.WAV) error
	// StartTimer, CancelTimer and Timers are the device's own timers, owned by the add-on: CancelTimer
	// is false for a timer that is not the add-on's.
	StartTimer(id, name string, d time.Duration) (string, error)
	CancelTimer(id, timer string) bool
	Timers(id string) []addonapi.Timer
	// SetAwake keeps the screen from dimming and going out while held is true.
	SetAwake(held bool)
	// Fire puts esphome.techo5_addon on Home Assistant's bus.
	Fire(id, name string, data map[string]string) error
	// SetEntity sets one of the add-on's entities, checked against its declaration.
	SetEntity(id string, e addonapi.Entity, state any) error
	// Media plays (op media.play, with p), pauses, resumes or stops the add-on's video or audio, and
	// only its own. A play answers the id its media.state events carry.
	Media(id, op string, p addonapi.MediaPlay) (string, error)
	// MediaStatus is what the add-on's latest media is doing: State idle when it started none. It is
	// about the add-on's own media only, never what else plays. The device tells the server when it may
	// have changed (MediaChanged) and when it ends (MediaEnded).
	MediaStatus(id string) addonapi.MediaState
}

// Server serves every running add-on's socket.
type Server struct {
	// Dir is where each add-on's folder is made (DefaultDir).
	Dir    string
	Device Device
	// Allowed is whether uid is an installed add-on's that is switched on, asked at every connection.
	Allowed func(uid uint32) bool
	// Chown gives a path to the add-on's user; a variable for tests, which cannot.
	Chown func(path string, uid int) error
	// PeerUID is who is at the other end of a connection (SO_PEERCRED); a variable for tests.
	PeerUID func(net.Conn) (uint32, error)
	Now     func() time.Time
	// Trace, when set, is told every request and how it ended: the fake daemon's log of them.
	Trace func(id string, req addonapi.Request, err error)

	mu     sync.Mutex
	addons map[string]*addon
	holds  int // keep-awake holds alive, every add-on's

	logs logLimit
}

// New is a server with the device given, sockets in dir.
func New(dir string, d Device, allowed func(uint32) bool) *Server {
	return &Server{Dir: dir, Device: d, Allowed: allowed, Chown: chown, PeerUID: peerUID, Now: time.Now,
		addons: map[string]*addon{}}
}

// chown gives a path to an add-on's user and its group, which has the same number (feature/addons,
// credFor).
func chown(path string, uid int) error { return os.Lchown(path, uid, uid) }

// addon is one add-on's socket and everything the server holds for it, guarded by the server's mu.
type addon struct {
	grant  Grant
	ln     net.Listener
	dir    string
	conns  map[*conn]bool
	nextID uint64
	ctx    context.Context
	cancel context.CancelFunc

	strikes []time.Time
	banned  time.Time

	speech   []string
	speaking bool

	soundAt time.Time
	events  bucket
	states  bucket             // every entity's sets together
	sets    map[string]*bucket // each entity's
	shown   map[string]string  // each entity's last state, as JSON, so one set again is not sent on
	timerOp bucket             // timer.start and timer.cancel together
	plays   bucket             // media.play
	timerMu sync.Mutex         // held across the count and the start, so connections cannot pass MaxTimers together

	holds    map[string]*hold
	calls    map[uint64]*pending
	nextCall uint64

	media mediaWatch
}

// pending is a call waiting for the add-on's reply: a command's, or a settings test's, which is
// answered by its own op and held to its own length.
type pending struct {
	ch   chan addonapi.CommandReply
	test bool
}

// ErrRoot is a grant for an add-on that runs as root, which the API never serves: a root shell
// could pose as it.
var ErrRoot = errors.New("serve: an add-on that runs as root cannot use the API")

// Open makes the add-on's folder and socket and starts serving it, and answers the socket's path for
// TECHO5_ADDON_API. An add-on already open is closed first, its connections with it.
func (s *Server) Open(g Grant) (string, error) {
	if g.UID == 0 {
		return "", ErrRoot
	}
	s.Close(g.ID)
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	_ = os.Chmod(s.Dir, 0o755)
	dir := filepath.Join(s.Dir, g.ID)
	_ = os.RemoveAll(dir)
	// The folder stays root's, 0711: the add-on can reach its socket through it and can put nothing in
	// it, so it cannot fill /run, which is RAM and has no quota of its own.
	if err := os.Mkdir(dir, 0o711); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o711)
	path := filepath.Join(dir, SocketName)
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	// The socket is the add-on's user's alone, mode 0600: nobody else but root can connect to it, and
	// root is refused at the door.
	err = os.Chmod(path, 0o600)
	if err == nil {
		err = s.Chown(path, int(g.UID))
	}
	if err != nil {
		ln.Close()
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("serve: giving the socket to the add-on's user: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &addon{grant: g, ln: ln, dir: dir, conns: map[*conn]bool{}, ctx: ctx, cancel: cancel,
		events:  bucket{rate: addonapi.EventsPerSecond, burst: addonapi.EventsPerSecond},
		states:  bucket{rate: addonapi.StatesPerSecond, burst: addonapi.StatesPerSecond},
		timerOp: bucket{rate: 1 / addonapi.TimerOpEvery.Seconds(), burst: addonapi.TimerOpBurst},
		plays:   bucket{rate: 1 / addonapi.MediaPlayEvery.Seconds(), burst: addonapi.MediaPlayBurst},
		sets:    map[string]*bucket{}, shown: map[string]string{}, holds: map[string]*hold{}, calls: map[uint64]*pending{}}
	s.mu.Lock()
	if s.addons == nil {
		s.addons = map[string]*addon{}
	}
	s.addons[g.ID] = a
	s.mu.Unlock()
	go s.accept(a)
	return path, nil
}

// Close stops serving an add-on: its socket and folder go, its connections are dropped, its keep-awake
// holds end, and its words not yet said are dropped. Its timers are the device's and stay.
func (s *Server) Close(id string) {
	s.mu.Lock()
	a := s.addons[id]
	delete(s.addons, id)
	var conns []*conn
	if a != nil {
		for c := range a.conns {
			conns = append(conns, c)
		}
		a.speech = nil
	}
	s.mu.Unlock()
	if a == nil {
		return
	}
	a.cancel()
	s.mediaClose(a)
	a.ln.Close()
	for _, c := range conns {
		c.close()
	}
	s.releaseAll(a)
	_ = os.RemoveAll(a.dir)
}

// Grant is what an open add-on was granted.
func (s *Server) Grant(id string) (Grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.addons[id]
	if !ok {
		return Grant{}, false
	}
	return a.grant, true
}

// Connected is how many connections of the add-on have said hello.
func (s *Server) Connected(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.addons[id]
	if !ok {
		return 0
	}
	n := 0
	for c := range a.conns {
		if c.greeted() {
			n++
		}
	}
	return n
}

// Banned is until when an add-on is disconnected for abusing the API, when it is now.
func (s *Server) Banned(id string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.addons[id]
	if !ok || !s.Now().Before(a.banned) {
		return time.Time{}, false
	}
	return a.banned, true
}

func (s *Server) accept(a *addon) {
	for {
		nc, err := a.ln.Accept()
		if err != nil {
			return // closed
		}
		if why := s.admit(a, nc); why != "" {
			nc.Close()
			if why != quietly {
				s.warn(a.grant.ID, "refused", "addonapi: a connection was refused", "why", why)
			}
			continue
		}
	}
}

// quietly is admit's answer for a connection refused without a word: one from an add-on that is
// disconnected for a minute, or already has all its connections, which said so once already.
const quietly = "\x00"

// admit decides about one connection, and serves it when it may stay: "" or why not.
func (s *Server) admit(a *addon, nc net.Conn) string {
	uid, err := s.PeerUID(nc)
	switch {
	case err != nil:
		return "its user cannot be told: " + err.Error()
	case uid == 0:
		return "it is root, which never speaks for an add-on"
	case uid != a.grant.UID:
		return fmt.Sprintf("it is user %d, not the add-on's", uid)
	case s.Allowed != nil && !s.Allowed(uid):
		return "its user is not an installed add-on that is switched on"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addons[a.grant.ID] != a {
		return "the add-on has stopped"
	}
	if s.Now().Before(a.banned) {
		return quietly
	}
	if len(a.conns) >= addonapi.MaxConns {
		return quietly
	}
	a.nextID++
	c := &conn{s: s, a: a, nc: nc, id: a.nextID, out: make(chan []byte, outQueue),
		work: make(chan addonapi.Request, addonapi.MaxOutstanding), gone: make(chan struct{}),
		tokens: bucket{rate: addonapi.Rate, burst: addonapi.Burst}}
	a.conns[c] = true
	go c.writer()
	go c.worker()
	go c.reader()
	return ""
}

// strike counts a request refused for the add-on's limits, and disconnects it once there are too many.
func (s *Server) strike(a *addon) {
	now := s.Now()
	s.mu.Lock()
	kept := a.strikes[:0]
	for _, t := range a.strikes {
		if now.Sub(t) < strikeWindow {
			kept = append(kept, t)
		}
	}
	a.strikes = append(kept, now)
	ban := int64(len(a.strikes)) >= strikeLimit.Load()
	var conns []*conn
	if ban {
		a.banned, a.strikes = now.Add(banFor), nil
		for c := range a.conns {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	if ban {
		s.warn(a.grant.ID, "banned", "addonapi: an add-on kept going past its limits and is disconnected for a minute")
		for _, c := range conns {
			c.close()
		}
	}
}

// conn is one connection of an add-on's.
type conn struct {
	s  *Server
	a  *addon
	nc net.Conn
	id uint64

	out  chan []byte
	work chan addonapi.Request
	gone chan struct{}
	once sync.Once

	tokens bucket // touched by the reader alone

	mu    sync.Mutex
	hello bool
}

func (c *conn) greeted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello
}

// close drops the connection, and with it everything it held: its keep-awake holds.
func (c *conn) close() {
	c.once.Do(func() {
		close(c.gone)
		c.nc.Close()
		c.s.mu.Lock()
		delete(c.a.conns, c)
		c.s.mu.Unlock()
		c.s.releaseConn(c)
	})
}

// send queues a message for the add-on; one that has stopped reading is dropped.
func (c *conn) send(v any) {
	b, err := addonapi.Marshal(v)
	if err != nil {
		slog.Error("addonapi: a message would not encode", "err", err)
		return
	}
	select {
	case <-c.gone:
	case c.out <- b:
	default:
		c.s.warn(c.a.grant.ID, "unread", "addonapi: an add-on is not reading what is sent to it; disconnected")
		c.close()
	}
}

func (c *conn) refuse(id uint64, e *addonapi.Error) {
	c.send(addonapi.Response{ID: id, Error: e})
}

func (c *conn) writer() {
	for {
		select {
		case <-c.gone:
			return
		case b := <-c.out:
			_ = c.nc.SetWriteDeadline(time.Now().Add(writeWithin))
			if err := addonapi.WriteFrame(c.nc, b); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *conn) reader() {
	defer c.close()
	for {
		n, err := addonapi.ReadHeader(c.nc)
		if err != nil {
			return
		}
		_ = c.nc.SetReadDeadline(time.Now().Add(bodyWithin))
		// The request's token is taken here, before anything of it is read or parsed: a flood of
		// messages that never parse costs the add-on its rate as much as one of good ones.
		allowed := c.tokens.take(c.s.Now())
		if n > addonapi.MaxMessage {
			if n > skipMost {
				c.s.warn(c.a.grant.ID, "huge", "addonapi: an add-on sent a message far past the limit; disconnected", "bytes", n)
				c.s.strike(c.a)
				return
			}
			if _, err := io.CopyN(io.Discard, c.nc, int64(n)); err != nil {
				return
			}
			_ = c.nc.SetReadDeadline(time.Time{})
			c.refuse(0, addonapi.Errorf(addonapi.CodeTooLarge, "a message is at most %d bytes", addonapi.MaxMessage))
			c.s.strike(c.a)
			continue
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(c.nc, b); err != nil {
			return
		}
		_ = c.nc.SetReadDeadline(time.Time{})
		if !allowed {
			// Its id alone is read, so the add-on can tell which request was refused: a plain decode of
			// one field, which costs no more than the message is long.
			var head struct {
				ID uint64 `json:"id"`
			}
			_ = json.Unmarshal(b, &head)
			c.refuse(head.ID, addonapi.Errorf(addonapi.CodeRateLimited, "at most %d requests a second", addonapi.Rate))
			c.s.strike(c.a)
			continue
		}
		var req addonapi.Request
		if err := addonapi.Unmarshal(b, &req); err != nil {
			c.refuse(0, addonapi.Errorf(addonapi.CodeBadRequest, "the message does not read: %v", err))
			c.s.strike(c.a)
			continue
		}
		select {
		case c.work <- req:
		default:
			c.refuse(req.ID, addonapi.Errorf(addonapi.CodeRateLimited, "at most %d requests may wait for an answer", addonapi.MaxOutstanding))
			c.s.strike(c.a)
		}
	}
}

func (c *conn) worker() {
	for {
		select {
		case <-c.gone:
			return
		case req := <-c.work:
			result, err := c.s.handle(c, req)
			if c.s.Trace != nil {
				c.s.Trace(c.a.grant.ID, req, err)
			}
			if err != nil {
				var e *addonapi.Error
				if !errors.As(err, &e) {
					// The device's own words stay in its log, and the add-on is told only that it failed:
					// an error can carry a path, an address or a name the add-on has no business with.
					c.s.warn(c.a.grant.ID, "failed "+req.Op, "addonapi: a request failed", "op", req.Op, "err", err)
					e = addonapi.Errorf(addonapi.CodeInternal, "the device could not do it")
				}
				c.refuse(req.ID, e)
				continue
			}
			resp := addonapi.Response{ID: req.ID, OK: true}
			if result != nil {
				b, err := addonapi.Marshal(result)
				if err != nil {
					c.refuse(req.ID, addonapi.Errorf(addonapi.CodeInternal, "the answer would not encode"))
					continue
				}
				resp.Result = b
			}
			c.send(resp)
		}
	}
}

// bucket is a token bucket: rate a second, at most burst at once.
type bucket struct {
	rate, burst float64
	tokens      float64
	last        time.Time
	started     bool
}

func (b *bucket) take(now time.Time) bool {
	if !b.started {
		b.tokens, b.last, b.started = b.burst, now, true
	}
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
