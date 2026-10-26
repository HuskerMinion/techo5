package streaming

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// runAirPlayReceiver runs shairport-sync under the device's name until ctx ends, playing what it sends
// and showing what it says is playing.
func runAirPlayReceiver(ctx context.Context, name string) {
	r, w, err := os.Pipe()
	if err != nil {
		slog.Error("streaming: AirPlay's audio pipe failed", "err", err)
		return
	}
	defer r.Close()
	defer w.Close()
	cred := receiverCred()
	meta := filepath.Join(runDir, "airplay-metadata")
	_ = os.Remove(meta)
	if err := syscall.Mkfifo(meta, 0o600); err != nil {
		slog.Warn("streaming: AirPlay's metadata pipe failed; no song names", "err", err)
	} else {
		handTo(cred, meta)
		safe.Go("airplay metadata", func() { readAirPlayMetadata(ctx, meta) })
	}
	conf := filepath.Join(runDir, "shairport-sync.conf")
	// The sender is reached only while this receiver runs, and a track left paused for it has nothing to
	// wait for once it stops: the session goes with shairport-sync, and no end of it will come.
	defer func() {
		airplayRemote.forget()
		media.Get().ReceiverGone(home.AirPlayName)
	}()
	safe.Go("airplay audio", func() { pump(ctx, home.AirPlayName, r, nil, airplayStarts) })
	if why := airplay2Missing(); why != "" {
		slog.Info("streaming: AirPlay is the classic receiver here", "why", why)
	} else if writeConf(conf, shairportConf(name, meta, true)) {
		if !runAirPlay2(ctx, conf, w, cred) {
			return
		}
		slog.Warn("streaming: AirPlay 2 would not start; the classic receiver takes over until AirPlay starts again")
	}
	if writeConf(conf, shairportConf(name, meta, false)) {
		supervise(ctx, program{name: "shairport-sync", path: shairportPath, args: []string{"-c", conf}, stdout: w, cred: cred})
	}
}

// writeConf writes shairport-sync's configuration, and says whether it could.
func writeConf(path, conf string) bool {
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		slog.Error("streaming: writing shairport-sync's configuration failed", "err", err)
		return false
	}
	return true
}

// airplay2Missing says what keeps AirPlay 2 from running here, or nothing when it can: both programs, the
// AirPlay 2 switch on, the loopback nqptp and the receiver talk to each other over, and /dev/shm, where nqptp
// keeps the clock for the receiver to read. A variable for the tests.
var airplay2Missing = func() string {
	if !Has2() {
		return "no AirPlay 2 in this image"
	}
	if config.Get().Streaming.AirPlayClassic {
		return "AirPlay 2 is turned off"
	}
	if _, err := os.Stat(noAirPlay2); err == nil {
		return "the firewall could not let AirPlay 2 in (techo5-firewall)"
	}
	if lo, err := net.InterfaceByName("lo"); err != nil || lo.Flags&net.FlagUp == 0 {
		return "the loopback is down"
	}
	if fi, err := os.Stat("/dev/shm"); err != nil || !fi.IsDir() {
		return "no /dev/shm"
	}
	return ""
}

// noAirPlay2 is where the firewall says it could not let AirPlay 2's sessions in (techo5-firewall: a kernel
// without the recent match): its receiver would be found by an iPhone and never play. A variable for tests.
var noAirPlay2 = "/run/techo5-no-airplay2"

// airplay2Tries is how many times in a row nqptp or the AirPlay 2 receiver may stop soon after starting
// before the classic receiver is run instead.
const airplay2Tries = 3

// nqptpArgs is nqptp's arguments: -u, which our build adds (tools/linux/nqptp-run-as-user.patch), to drop
// root for the receivers' user once PTP's ports are bound. On an image without that user it stays root, as
// the receivers do (receiverCred).
func nqptpArgs(cred *syscall.Credential) []string {
	if cred == nil {
		return nil
	}
	return []string{"-u", receiverUser}
}

// nqptpShm is where nqptp keeps the clock the receiver reads; a variable for the tests.
var nqptpShm = "/dev/shm/nqptp"

// runAirPlay2 runs nqptp and the AirPlay 2 receiver until ctx ends, and says whether either of them kept
// stopping as it started (airplay2Tries); both have stopped by the time it returns. nqptp starts as root,
// since PTP's ports (319 and 320) are privileged, and becomes the receivers' user as soon as it has them
// (nqptpArgs), before it reads anything from the network.
func runAirPlay2(ctx context.Context, conf string, out io.Writer, cred *syscall.Credential) (failed bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var clockFailed atomic.Bool
	clockDone := make(chan struct{})
	safe.Go("nqptp", func() {
		defer close(clockDone)
		if superviseTries(ctx, program{name: "nqptp", path: nqptpPath, args: nqptpArgs(cred)}, airplay2Tries) {
			clockFailed.Store(true)
			cancel()
		}
	})
	// The receiver finds the clock as it starts, so nqptp is given a moment to set it up.
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end) && ctx.Err() == nil; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(nqptpShm); err == nil {
			break
		}
	}
	failed = superviseTries(ctx, program{name: "shairport-sync", path: shairport2Path, args: []string{"-c", conf}, stdout: out, cred: cred}, airplay2Tries)
	cancel()
	<-clockDone
	return failed || clockFailed.Load()
}

// The ports the classic receiver is reached on, fixed so a firewall can let them through (the Dot's does):
// the session on airplayPort, and the audio, control and timing on airplayUDPCount ports from
// airplayUDPBase. AirPlay 2 is reached on airplay2Port whatever the configuration says, and opens ports of
// its own choosing for each session; a firewall lets those in for the sender that reached airplay2Port,
// and PTP's 319 and 320 for nqptp.
const (
	airplayPort     = 5000
	airplayUDPBase  = 6001
	airplayUDPCount = 10
	airplay2Port    = 7000
)

// airplay2Offset is how much earlier than its time the AirPlay 2 receiver hands over audio, so the device
// plays together with the rest of a group: the receiver takes what it writes as heard at once, but it waits
// in the daemon a while first (receivedLag), and then for the speaker's own ring (speaker.OutputLatency).
const airplay2Offset = -(speaker.OutputLatency + receivedLag)

// receivedLag is how long received audio waits in the daemon before the speaker's queue takes it, as heard
// on an Echo 2nd gen (radar, Dot image) grouped with a Sonos Amp: 0.18 s with the speaker's ring. A fixed
// figure for a first version; it varies some from one start to the next with how full the queue gets.
const receivedLag = 75 * time.Millisecond

// shairportConf is shairport-sync's configuration: the name phones show, the audio to standard output
// as 16-bit stereo at 44.1 kHz, and what is playing to the metadata pipe, with its cover where there is
// a screen to show it. Its volume stays its own: the phone's slider scales what it sends, and the
// device's volume is applied on top as for anything else. A second sender is turned away while one
// plays (allow_session_interruption is left off): the metadata then tells of one session at a time,
// which airplaySession counts on, and allows for the end of one coming late.
//
// The name is cut to fit the mDNS instance it is advertised as (airplayName). airplay2 is the configuration
// for the AirPlay 2 receiver: AirPlay 2 only, so it never quietly falls back to classic AirPlay under a name
// with "(Classic)" after it, and earlier by airplay2Offset.
func shairportConf(name, metaPipe string, airplay2 bool) string {
	name = airplayName(name)
	listen := fmt.Sprintf("\tport = %d;\n\tudp_port_base = %d;\n\tudp_port_range = %d;\n", airplayPort, airplayUDPBase, airplayUDPCount)
	if airplay2 {
		listen = fmt.Sprintf("\tservice_type = \"airplay2\";\n\taudio_backend_latency_offset_in_seconds = %.3f;\n", airplay2Offset.Seconds())
	}
	return fmt.Sprintf(`general = {
	name = %s;
	output_backend = "stdout";
	mdns_backend = "avahi";
	interpolation = "soxr";
%s};
sessioncontrol = {
	session_timeout = 60;
};
stdout = {
	output_rate = %d;
	output_format = "S16_LE";
	output_channels = %d;
};
metadata = {
	enabled = "yes";
	include_cover_art = "%s";
	cover_art_cache_directory = "";
	pipe_name = %s;
	pipe_timeout = 5000;
};
`, confString(name), listen, audioRate, audioChannels, yesNo(home.HasScreen()), confString(metaPipe))
}

// airplayName is the device's name as AirPlay advertises it: cut to fit one DNS label
// (layout.LabelMax), less the 12 hex digits of the device id and the @ that start the RAOP instance
// name before it, "AABBCCDDEEFF@Kitchen".
func airplayName(name string) string {
	return layout.CutName(name, layout.LabelMax-len("AABBCCDDEEFF@"))
}

// yesNo is b as shairport-sync's configuration writes it.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// confString is s as a libconfig string: quoted, with quotes and backslashes escaped and anything
// unprintable left out, since a device's name is whatever somebody typed.
func confString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// metaItem is one of shairport-sync's metadata items: a type and a code, each four characters written
// as hex, and data in base64.
type metaItem struct {
	Type string `xml:"type"`
	Code string `xml:"code"`
	Data string `xml:"data"`
}

// readAirPlayMetadata follows the metadata pipe, telling the media player the song as it changes. The
// pipe is opened for writing as well as reading, so it neither waits for shairport-sync nor sees an
// end each time shairport-sync restarts.
func readAirPlayMetadata(ctx context.Context, path string) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		slog.Warn("streaming: opening AirPlay's metadata pipe failed", "err", err)
		return
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	defer f.Close()
	// Covers are laid out apart from the reading, so a slow one never holds the pipe up: shairport-sync
	// drops what it cannot write.
	covers := &latest{do: func(b []byte) { home.ReceivedPicture(home.AirPlayName, b) }}
	session := &airplaySession{remote: airplayRemote, starts: airplayStarts,
		paused: func(paused bool) { media.Get().ReceiverPaused(home.AirPlayName, paused) },
		gone:   func() { media.Get().ReceiverGone(home.AirPlayName) },
	}
	followAirPlayMetadata(f, func(title, artist, album string) {
		media.Get().SetReceivedTrack(home.AirPlayName, title, artist, album)
	}, covers.put, session)
	// The receiver stopping ends the wait for a paused sender along with the sender (runAirPlayReceiver).
	session.stopKept()
}

// airplaySession follows the session as the metadata tells of it: who the sender is and how to reach
// it (dacpRemote), and whether it has paused.
//
// An iPhone pausing says so (core/caps 2), and some seconds later ends the session (ssnc/pend); until
// then shairport-sync goes on sending silence. Played again before the end it only says it plays (caps
// 1); after it, it starts a new session (pbeg, prsm) and says so. A session ended after the pause is
// waited on for airplayKeptFor; one ended without it is the phone gone: it chose another speaker, or
// left.
//
// shairport-sync (5.0.4, classic AirPlay) tells of a session in one order: the sender announcing it
// (snam, snua, each only if the sender names itself), setting it up (acre, daid, each only if the
// sender gives it, then clip), and playing (pbeg); dapo comes from avahi apart from these, once the
// session plays, and pend ends it. The next session's items all come after that pend (shairportConf).
// 5.5.1 can send the pend of a sender that vanished after the next session's setup has begun, which is
// why next is not reset at pend. disc (AirPlay 2) carries the address of the client leaving, and can
// come after the next session has started: it ends the session only if that is the one it came from
// (from). So what a session says of its sender is gathered as it is set up (next), and taken whole as
// it plays (take): a session that gave no DACP-ID or token has none, rather than the last one's.
// shairport-sync starting afresh (svna) ends a session it had open, which no pend will.
//
// caps, next, from, open and kept belong to the goroutine reading the metadata; the timer that ends the
// wait for a paused sender touches only remote and gone.
type airplaySession struct {
	remote *dacpRemote
	starts *startCount       // counts the times the sender starts to play, for the audio (pump)
	paused func(paused bool) // the sender pausing, or playing again, on its own
	gone   func()            // the sender gone for good: a track still paused here for it ends
	caps   byte              // what the sender last said it is doing: 1 playing, 2 paused, 0 nothing yet
	next   dacpSender        // what the session being set up has said of its sender so far
	from   netip.Addr        // where the open session came from (clip), as it was given
	open   bool              // a session has begun (pbeg) and not ended (pend, disc)
	kept   *time.Timer       // the wait for a sender that ended its session as it paused, if one is running
}

// airplayStarts counts the times the AirPlay receiver's sender starts to play; there is one receiver.
var airplayStarts = &startCount{}

// airplayKeptFor is how long a sender that ended its session as it paused is waited for, its song on
// the screen and play there reaching it: as long as a track stopped from Home Assistant stays paused
// (media's stoppedFor). One that has not played again by then has gone elsewhere, or been put away:
// its track ends, and the way back to it is dropped, rather than the screen and Home Assistant saying
// "paused" forever and a play hours later starting the phone wherever it is by then.
const airplayKeptFor = 30 * time.Minute

// airplayKeptAfter starts the timer for that; a variable for the tests.
var airplayKeptAfter = time.AfterFunc

// heard takes one of the items about the session, and says whether it was a session starting to play
// for another sender than the last (take): a song kept from the last one's pause is not this one's.
func (s *airplaySession) heard(typ, code string, d []byte) (another bool) {
	if s == nil {
		return false
	}
	switch {
	case typ == "core" && code == "caps" && len(d) == 1 && (d[0] == 1 || d[0] == 2):
		if d[0] == 1 && s.caps == 2 {
			// Playing again within the session: an iPhone that pauses keeps it a while before it
			// ends it, and one played again meanwhile says no more than this.
			s.starts.start()
		}
		s.caps = d[0]
		s.paused(d[0] == 2)
	case typ != "ssnc":
	case code == "snam", code == "snua":
		// A session announced: whatever was said of a sender before it is from a setup that never
		// played.
		s.next = dacpSender{}
	case code == "acre":
		s.next.token = string(d)
	case code == "daid":
		s.next.id = string(d)
	case code == "clip":
		s.next.client = senderAddr(string(d))
		s.next.from = clientAddr(string(d))
	case code == "dapo":
		s.remote.setPort(d)
	case code == "pbeg", code == "prsm":
		// The sender playing: a session starting, or going on after a flush.
		if code == "pbeg" {
			// A session playing is the sender back, or another: either way the wait for the last
			// one is over.
			s.stopKept()
			// A session's sender is the one it named as it was set up, and no other.
			another = s.remote.take(s.next)
			s.from, s.next, s.open = s.next.from, dacpSender{}, true
		}
		if code == "pbeg" || s.caps == 2 {
			s.starts.start()
		}
		s.caps = 1
		s.paused(false)
	}
	return another
}

// ended is the session ending (pend, or disc from the session's own address), and says whether it is
// over for good. One the sender ended as it paused is not, for airplayKeptFor: the song stays for the
// screen, with play on it, and the way back to the sender is kept for that play to reach it. If no
// session has played by then, the sender has gone: the way back is dropped, and its track, if still
// paused here, ends. A session ended without the pause is the sender gone at once, and a track the
// device paused for it ends with it. A wait already running is the last session's, and is replaced.
func (s *airplaySession) ended() bool {
	if s == nil {
		return true
	}
	s.open = false
	s.stopKept()
	if s.caps == 2 {
		n := s.remote.sessions()
		s.kept = airplayKeptAfter(airplayKeptFor, func() {
			if s.remote.forgetIf(n) {
				slog.Info("streaming: an AirPlay sender paused this long is taken as gone", "after", airplayKeptFor)
				s.gone()
			}
		})
		return false
	}
	s.remote.forget()
	s.gone()
	return true
}

// disconnected is a client leaving (disc), with its address as shairport-sync gives it, and says whether
// the session is over for good (ended). Only the session that client began ends with it: one from
// another address, or one whose address was never given, is not touched, and the pend that ends it
// comes in its own time. With no session followed, any client leaving ends it.
func (s *airplaySession) disconnected(addr string) bool {
	if s == nil {
		return s.ended()
	}
	if a := clientAddr(addr); !s.open || !a.IsValid() || a != s.from {
		return false
	}
	return s.ended()
}

// stopKept ends the wait for a paused sender, if one is running: the sender playing again answers it,
// a later end replaces it, and so does the receiver stopping, so there is one wait at a time rather
// than one for each pause. One whose time came as a session started has run already, and forgetIf
// passes it over (took).
func (s *airplaySession) stopKept() {
	if s != nil && s.kept != nil {
		s.kept.Stop()
		s.kept = nil
	}
}

// restarted is shairport-sync starting (svna), and says whether a session it had open is over for good:
// one still open when it stopped has ended without its pend, and is ended here as pend would have. With
// none open there is nothing to end, and the song stays as it is.
func (s *airplaySession) restarted() bool {
	if s == nil {
		return false
	}
	// A setup the restart cut short never plays.
	s.next = dacpSender{}
	if !s.open {
		return false
	}
	return s.ended()
}

// cutField is a song field far longer than any name cut to 1024 bytes, back to the start of the
// character the cut would split; anything shorter is left exactly as it came.
func cutField(d []byte) []byte {
	const most = 1024
	if len(d) <= most {
		return d
	}
	cut := most
	for cut > most-utf8.UTFMax && !utf8.RuneStart(d[cut]) {
		cut--
	}
	return d[:cut]
}

// latest hands what it is given to do one at a time and in order, on a goroutine of its own; one still
// waiting when a newer one comes is dropped for it.
type latest struct {
	do      func([]byte)
	mu      sync.Mutex
	next    []byte
	waiting bool
	running bool
}

func (l *latest) put(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next, l.waiting = b, true
	if !l.running {
		l.running = true
		safe.Go("airplay cover", l.run)
	}
}

// run does what is waiting until nothing is. running goes false in the same hold of mu that finds
// nothing waiting, so a put just after it starts another, and a do that panics is let go of here, so
// the loop goes on to whatever was put meanwhile.
func (l *latest) run() {
	for {
		l.mu.Lock()
		if !l.waiting {
			l.running = false
			l.mu.Unlock()
			return
		}
		b := l.next
		l.next, l.waiting = nil, false
		l.mu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("streaming: showing an AirPlay cover panicked", "panic", r, "stack", string(debug.Stack()))
				}
			}()
			l.do(b)
		}()
	}
}

// largestPicture is the most a cover sent over the metadata pipe may hold, as for one fetched
// (home.fetchArt); a bigger one is passed over.
const largestPicture = 4 << 20

// largestItem is the most the decoder may read for one item: a largest picture in base64, with room
// for the line breaks and the markup around it. An item past it is given up on part way, so it is
// never held whole, and reading picks up after it.
var largestItem int64 = largestPicture * 3 / 2

// errItemTooBig is what the decoder is given once an item has had all it may read.
var errItemTooBig = errors.New("metadata item too big")

// itemBudget is the decoder's reader, allowing it left more bytes before it fails. It reads a byte at
// a time where the decoder asks, so the decoder reads no further ahead than it would from br itself.
type itemBudget struct {
	br   *bufio.Reader
	left int64
}

func (b *itemBudget) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errItemTooBig
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.br.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *itemBudget) ReadByte() (byte, error) {
	if b.left <= 0 {
		return 0, errItemTooBig
	}
	c, err := b.br.ReadByte()
	if err == nil {
		b.left--
	}
	return c, err
}

// followAirPlayMetadata reads items from r until it ends, calling told with the song whenever a field of
// it changes, and pictured with its cover as the phone sends one; both with nothing when the session
// ends for good, or a session starts for another sender than the one whose song a pause kept. What the
// items say of the session goes to session, which may be nil. Anything the decoder cannot take is
// passed over, and reading goes on after it: only r failing ends it.
func followAirPlayMetadata(r io.Reader, told func(title, artist, album string), pictured func(picture []byte), session *airplaySession) {
	src := &readErr{r: r}
	br := bufio.NewReaderSize(src, 64<<10)
	budget := &itemBudget{br: br}
	newDecoder := func() *xml.Decoder {
		// The budget is a ByteReader, so the decoder reads no further than it has to: a new one
		// picks up where an old one gave up.
		d := xml.NewDecoder(budget)
		d.Strict = false
		return d
	}
	dec := newDecoder()
	var title, artist, album string
	covered := false // a cover is up for the song: given, and not taken down since
	// stuck counts decoder errors in a row that took nothing from the pipe.
	stuck, at := 0, int64(-1)
	for {
		var it metaItem
		budget.left = largestItem
		if err := dec.Decode(&it); err != nil {
			// The pipe itself failed (closed as the receivers stop), or the decoder keeps giving up
			// without taking anything: the end.
			taken := src.n - int64(br.Buffered())
			if taken == at {
				stuck++
			} else {
				stuck, at = 0, taken
			}
			if src.err != nil || stuck > 100 {
				return
			}
			dec = newDecoder()
			continue
		}
		typ, code := fourCC(it.Type), fourCC(it.Code)
		if typ == "ssnc" && code == "PICT" {
			// The cover, which phones send apart from the song's fields. An empty one is the phone's
			// word for a song without a cover (its image/none), and takes the last one down. One past
			// the limit, or that will not decode, is passed over: the last one stays.
			if b64 := strings.TrimSpace(it.Data); base64.StdEncoding.DecodedLen(len(b64)) <= largestPicture {
				if b, err := base64.StdEncoding.DecodeString(b64); err == nil {
					pictured(b)
					covered = len(b) > 0
				}
			}
			continue
		}
		d, err := base64.StdEncoding.DecodeString(strings.TrimSpace(it.Data))
		if err != nil && (typ == "core" || (typ == "ssnc" && usesData(code))) {
			// A field that cannot be read leaves the song as it was, and an item whose data is used as
			// it comes (a token, an address) leaves what it would have set: the decoder gives what it
			// read before the error, and that cut short is not what was sent (192.0.2.10 as 192.0.2.1
			// would send the commands to another host).
			continue
		}
		data := string(cutField(d))
		switch {
		case typ == "core" && code == "minm":
			title = data
		case typ == "core" && code == "asar":
			artist = data
		case typ == "core" && code == "asal":
			album = data
		case typ == "ssnc" && (code == "pend" || code == "disc"):
			var over bool
			if code == "disc" {
				over = session.disconnected(data)
			} else {
				over = session.ended()
			}
			if !over {
				continue
			}
			title, artist, album, covered = "", "", "", false
			pictured(nil)
		case typ == "ssnc" && code == "svna":
			if !session.restarted() {
				continue
			}
			title, artist, album, covered = "", "", "", false
			pictured(nil)
		default:
			// A session starting for another sender than the one whose song a pause kept: that song and
			// its cover are not what this one plays.
			if !session.heard(typ, code, cutField(d)) || (title == "" && artist == "" && album == "" && !covered) {
				continue
			}
			title, artist, album, covered = "", "", "", false
			pictured(nil)
		}
		told(title, artist, album)
	}
}

// readErr is a reader that keeps the error its reader last gave, so a decoder's own complaints can be
// told from the pipe failing, and how much it has read.
type readErr struct {
	r   io.Reader
	n   int64 // bytes read so far
	err error
}

func (e *readErr) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	e.n += int64(n)
	if err != nil {
		e.err = err
	}
	return n, err
}

// usesData is whether the session item code is used for what its data says: the sender's token, its
// DACP-ID, address and port, and the address of a client leaving. The other items (pbeg, pend and the
// rest) say all they do by coming.
func usesData(code string) bool {
	switch code {
	case "acre", "daid", "clip", "dapo", "disc":
		return true
	}
	return false
}

// fourCC is a four-character code written as eight hex digits.
func fourCC(h string) string {
	b, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil || len(b) != 4 {
		return ""
	}
	return string(b)
}
