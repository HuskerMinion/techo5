//go:build panel

package mic

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/loopback"
	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

// dump records what is heard while files/dump-echo exists: echo-mic.raw holds 16 kHz S16LE pairs,
// the microphone then the reference as the canceller got it, and echo-mic.txt the loopback index of
// the first pair's reference and the measured lag. A debug build only (lib/loopback's Dump).
var dump = loopback.Dump{FlagPath: layout.StateDir + "/dump-echo"}

// micSocket is the abstract socket the app serves the microphone on. The app reads it from Android
// as 16 kHz mono S16LE and writes it here as it comes; nothing else is said on it.
const micSocket = "@techo5.mic"

// dialWait is how long the daemon waits for the app's socket before giving up on this start. The app
// starts the daemon after the socket is listening, so this only covers a slow start.
const dialWait = 10 * time.Second

// Lining the speaker's history up with the microphone. Android keeps both ends of the path to itself
// and the delay between them differs by device, route and buffer, so it is measured while something
// plays: the loudness of what was played against the loudness of what was heard, a few milliseconds
// at a time, at every lag up to maxLag. The canceller's filter reaches 64 ms (cancelTaps), so the
// reference has to arrive at most that far ahead of its echo and never after it: it is placed lead
// ahead.
const (
	envBlock    = 64              // samples per loudness block, 4 ms
	envWindow   = Rate / envBlock // blocks compared at once, one second
	maxLag      = Rate / envBlock // a second of lag, in blocks
	lead        = 2 * envBlock    // 8 ms
	minScore    = 0.8             // correlation a measurement needs to be believed
	moveAtLeast = envBlock * 2    // a smaller correction is jitter, not a different delay
	driftLimit  = Rate / 8        // 125 ms: further than this from the writer, line up again
	wantSettle  = 0.02            // how much of each read's view of the writer the average takes
)

// socketCapture turns the app's mono S16LE stream into the frames the Source expects: S24_3LE, the
// microphone in ch0 and what the speaker played, lined up, in ch1.
type socketCapture struct {
	conn net.Conn
	in   []byte
	ref  []int16

	// read is how many microphone samples have been read. The reference for microphone sample m is
	// loopback sample m+offset; lag is the delay measured into it, so the writer's position less lag
	// is where offset should sit.
	read    int64
	offset  int64
	lag     int64
	started bool
	// want is where offset should sit, averaged over about a second of reads: the writer moves a
	// period at a time and is held up by the app now and then, so one read's view of it jitters.
	want float64
	// written is the writer's position at the last line-up.
	written uint64

	// Loudness of the microphone, one value per envBlock, since the last time offset moved.
	env   []float64
	block float64
	fill  int
	from  int64 // the microphone sample env starts at

	dumpFile *os.File

	// flat notices Android handing over nothing but silence (flat_panel.go).
	flat flatWatch

	// quiet is set while the app's stream has sent nothing for quietLimit (readFull).
	quiet bool
}

// quietLimit is how long the app's stream may send nothing at all before the log says so. Silence
// still comes as samples; nothing means Android never started recording for the app, or stopped
// handing it anything, and without a line here the only trace is the ambient noise sensor finding no
// frames.
var quietLimit = 5 * time.Second

// readFull fills in from the app's stream. It waits as long as it takes, as io.ReadFull would, but says
// once in the log when nothing has come for quietLimit, and again when sound comes back.
func (c *socketCapture) readFull(in []byte) error {
	for got := 0; got < len(in); {
		_ = c.conn.SetReadDeadline(time.Now().Add(quietLimit))
		n, err := c.conn.Read(in[got:])
		got += n
		if n > 0 && c.quiet {
			c.quiet = false
			slog.Info("the app's microphone is sending sound again")
		}
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			if !c.quiet {
				c.quiet = true
				slog.Error("the app's microphone has sent nothing: Android has not started recording for the app, or has stopped; the app's log (tag techo5-panel) says what Android answered",
					"seconds", int(quietLimit/time.Second))
			}
			continue
		}
		if got > 0 && errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

// openDevice dials the app's microphone socket: on a panel the capture is the app's stream, not ALSA.
func openDevice() (capture, error) {
	deadline := time.Now().Add(dialWait)
	for {
		conn, err := panelsock.Dial(micSocket)
		if err == nil {
			c := &socketCapture{conn: conn, lag: savedLag()}
			// Keep both thresholds across an app-socket reconnect; sound clears them (flat_panel.go).
			c.flat.seed(micsilent.Cut(), micsilent.Silent())
			if c.lag > 0 {
				slog.Info("echo reference delay remembered", "delay_ms", c.lag*1000/Rate)
			}
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connecting to the app's microphone: %w", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// afterOpen has nothing to do: Android sets the microphone up for the app.
func afterOpen() {}

// Read fills p with whole frames. It reads one mono sample for every frame p holds, so a frame is
// never split across reads.
func (c *socketCapture) Read(p []byte) (int, error) {
	const frameBytes = Channels * Bits / 8
	frames := len(p) / frameBytes
	if frames == 0 {
		return 0, errors.New("mic: buffer smaller than one frame")
	}
	if cap(c.in) < frames*2 {
		c.in = make([]byte, frames*2)
		c.ref = make([]int16, frames)
	}
	in := c.in[:frames*2]
	if err := c.readFull(in); err != nil {
		return 0, err
	}
	wasCut := c.flat.cut
	if silent, changed := c.flat.add(in); changed {
		if silent {
			slog.Error("the microphone has been silent for a while: Android may have taken it away (its microphone switch, or another app); the wake word cannot be heard", "seconds", flatAfter)
		} else {
			slog.Info("the microphone hears again")
		}
		micsilent.Set(silent)
	}
	if c.flat.cut != wasCut {
		micsilent.SetCut(c.flat.cut)
	}

	c.lineUp()
	ref := c.ref[:frames]
	loopback.Played.ReadAt(ref, c.read+c.offset)

	for f := range frames {
		// A 16-bit sample is the top two bytes of a 24-bit one.
		o := f * frameBytes
		p[o], p[o+1], p[o+2] = 0, in[2*f], in[2*f+1]
		r := uint16(ref[f])
		p[o+3], p[o+4], p[o+5] = 0, byte(r), byte(r>>8)

		v := float64(int16(uint16(in[2*f]) | uint16(in[2*f+1])<<8))
		c.block += v * v
		if c.fill++; c.fill == envBlock {
			c.env = append(c.env, math.Sqrt(c.block/envBlock))
			c.block, c.fill = 0, 0
		}
	}
	c.record(in, ref)
	c.read += int64(frames)

	if len(c.env) >= envWindow {
		c.measure()
	}
	return frames * frameBytes, nil
}

// lineUp keeps offset where the writer says it should be, on average: the writer moves a period at a
// time and stalls while the app catches up, and following every jump would tear the reference.
func (c *socketCapture) lineUp() {
	// While the speaker is down (the app gone, or the write loop held) the writer stands still and the
	// microphone reads on, so every read would seem to drift and line up again. Nothing is played then
	// to line up against: the line-up waits for the writer to move, and catches up once when it does.
	w := loopback.Played.Written()
	if c.started && w == c.written {
		return
	}
	c.written = w
	now := float64(int64(w) - c.read - c.lag)
	if !c.started {
		c.want, c.offset, c.started = now, int64(now), true
		c.restart()
		return
	}
	c.want += (now - c.want) * wantSettle
	if drift := int64(c.want) - c.offset; abs64(drift) > driftLimit {
		slog.Info("echo reference lined up again", "drift_ms", drift*1000/Rate)
		c.offset = int64(c.want)
		c.restart()
	}
}

// measure finds the delay from the speaker's history to the microphone over the last second, and
// moves the reference to it when the answer is clear and different.
func (c *socketCapture) measure() {
	mic := c.env[len(c.env)-envWindow:]
	start := c.from + int64(len(c.env)-envWindow)*envBlock

	// The speaker's loudness from maxLag blocks before the window to its end, as the reference stood.
	span := (envWindow + maxLag) * envBlock
	played := make([]int16, span)
	loopback.Played.ReadAt(played, start+c.offset-int64(maxLag*envBlock))
	spk := make([]float64, envWindow+maxLag)
	for b := range spk {
		var e float64
		for _, v := range played[b*envBlock : (b+1)*envBlock] {
			e += float64(v) * float64(v)
		}
		spk[b] = math.Sqrt(e / envBlock)
	}

	best, score := 0, 0.0
	for lag := 0; lag <= maxLag; lag++ {
		// spk[maxLag+i-lag] is what was played lag blocks before microphone block i was heard.
		if s := correlate(mic, spk[maxLag-lag:maxLag-lag+envWindow]); s > score {
			best, score = lag, s
		}
	}
	c.restart()
	if lvl := level(spk[maxLag:]); lvl > -60 {
		// How loud the speaker's own voice comes back, against how loud it was played: lower is more
		// taken out before the daemon sees it, which is what a platform echo canceller does.
		slog.Info("echo level", "played_db", math.Round(lvl), "heard_db", math.Round(level(mic)),
			"score", math.Round(score*100)/100)
	}
	if score < minScore {
		return
	}

	// The echo comes best blocks after the reference as it stands; move the reference that much
	// later, less the lead.
	move := int64(best*envBlock) - lead
	if abs64(move) < moveAtLeast {
		return
	}
	// The delay is the whole path, measured from where the writer is, so it is never negative and
	// never past what is searched. A faint echo that a platform canceller has already worked on can
	// still correlate now and then at some other lag; moving there would walk the reference away.
	if next := c.lag + move; next < 0 || next > maxLag*envBlock {
		return
	}
	c.lag += move
	c.offset -= move
	c.want -= float64(move)
	slog.Info("echo reference delay measured", "delay_ms", c.lag*1000/Rate, "score", math.Round(score*100)/100)
	saveLag(c.lag)
}

// restart forgets the microphone's loudness: after the reference moves, what was gathered against the
// old position measures the wrong thing.
func (c *socketCapture) restart() {
	c.env = c.env[:0]
	c.block, c.fill = 0, 0
	c.from = c.read
}

// correlate is the Pearson correlation of two equal runs, 0 where either is flat: a speaker playing
// silence, or a quiet room, says nothing about the delay.
func correlate(a, b []float64) float64 {
	n := float64(len(a))
	var sa, sb float64
	for i := range a {
		sa += a[i]
		sb += b[i]
	}
	ma, mb := sa/n, sb/n
	var ab, aa, bb float64
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		ab += x * y
		aa += x * x
		bb += y * y
	}
	// A reference this flat is silence or a steady hum: nothing to line up against.
	if aa == 0 || bb < n*100 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

// lagPath keeps the measured delay across restarts. The path from the speaker to the microphone
// belongs to the tablet and its Android build, not to this run, and without it the first reply after
// a start plays with the reference half a second off: the canceller learns nothing until the delay
// has been measured, and a wake word said over that reply is missed.
var lagPath = layout.StateDir + "/echo-delay"

func savedLag() int64 {
	b, err := os.ReadFile(lagPath)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || v < 0 || v > maxLag*envBlock {
		return 0
	}
	return v
}

func saveLag(v int64) {
	tmp := lagPath + ".new"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(v, 10)+"\n"), 0o600); err == nil {
		_ = os.Rename(tmp, lagPath)
	}
}

// level is the mean loudness of a run of block levels, in dBFS.
func level(env []float64) float64 {
	var e float64
	for _, v := range env {
		e += v * v
	}
	e /= float64(len(env))
	if e == 0 {
		return -120
	}
	return 10 * math.Log10(e/(32768*32768))
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (c *socketCapture) record(in []byte, ref []int16) {
	if !dump.On() {
		if c.dumpFile != nil {
			_ = c.dumpFile.Close()
			c.dumpFile = nil
		}
		return
	}
	if c.dumpFile == nil {
		f, err := os.Create(layout.StateDir + "/echo-mic.raw")
		if err != nil {
			return
		}
		c.dumpFile = f
		_ = os.WriteFile(layout.StateDir+"/echo-mic.txt",
			[]byte(fmt.Sprintf("%d %d\n", c.read+c.offset, c.lag)), 0o600)
	}
	buf := make([]byte, 0, len(ref)*4)
	for i, r := range ref {
		buf = append(buf, in[2*i], in[2*i+1], byte(uint16(r)), byte(uint16(r)>>8))
	}
	_, _ = c.dumpFile.Write(buf)
}

func (c *socketCapture) Close() error { return c.conn.Close() }
