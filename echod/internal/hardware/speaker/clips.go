package speaker

import (
	"embed"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Recorded sounds, where a tone is a note made here: the Home Assistant Voice sounds (sounds/LICENSE.md,
// CC BY 4.0, Clayton Charles Tapp), so a TECHO5 device sounds like the Home Assistant satellites
// beside it (techo5#34). They are kept as they play - 16-bit mono at the output's rate, converted from
// the originals' FLAC when they were added - so nothing is decoded on the device.

//go:embed sounds/*.pcm
var clipFiles embed.FS

// SoundsDir is where recordings of the owner's own are kept. A 16-bit WAVE file at Rate named for a
// clip, wake_word_triggered.wav say, plays in its place, and taking it away puts the stock one back.
// Either is noticed the next time the clip plays, so neither needs a restart.
var SoundsDir = filepath.Join(layout.StateDir, "sounds")

// longestOwn is the most a recording of the owner's own may hold, in samples: a cue, not a song, and
// the whole of it is held in memory. largestOwn keeps a file far past that from being read at all;
// it is twice a 10-second stereo file, so headers and extra chunks never decide it.
const (
	longestOwn = 10 * Rate
	largestOwn = 2 * longestOwn * 2 * 2
)

// Clip is one recorded sound.
type Clip struct {
	file string
	made bool // made in code (Synth): no file of its own or of the owner's, its samples are all there is

	// Everything below is guarded by mu, and is what load last read.
	mu      sync.Mutex
	from    recording
	loaded  bool
	own     bool    // samples are the owner's recording rather than the stock one
	samples []int16 // mono, at Rate
	peak    float64 // the loudest sample, as a share of full scale
	loudMs  int     // how long it stays within 20 dB of its loudest (Audible)
	quietMs int     // how long it stays within 30 dB (Audible, with no canceller)

	// The last rendering and the gain it was made at: a ring plays the same clip at the same level
	// round after round. Kept for the stock recordings only: an owner's can be ten seconds of
	// stereo, and is quick enough to render again.
	lastGain float64
	last     []int16
}

// recording is the file of the owner's own a clip was read from, as of when it was read; the zero
// value is none. A file replaced or taken away no longer matches it: a copy that keeps the old
// file's time and size still changes the inode or the change time.
type recording struct {
	path         string
	mtime, ctime int64
	ino          uint64
	size         int64
}

var (
	ClipWake    = &Clip{file: "wake_word_triggered"}
	ClipTimer   = &Clip{file: "timer_finished"}
	ClipMuteOn  = &Clip{file: "mute_switch_on"}
	ClipMuteOff = &Clip{file: "mute_switch_off"}

	// ClipFailure and ClipCanceled have no stock recording: they are notes (tones.go) until the owner
	// puts one of their own in SoundsDir. ClipAlarm is the owner's alarm sound (#284), the Your own
	// alarm sound; without one that rings with the default alarm sound.
	ClipFailure  = &Clip{file: "failure"}
	ClipCanceled = &Clip{file: "canceled"}
	ClipAlarm    = &Clip{file: "alarm"}
)

func (c *Clip) ownPath() string { return filepath.Join(SoundsDir, c.file+".wav") }

// ownRecording is the owner's file for this clip as it stands now, the zero value where there is none.
func (c *Clip) ownRecording() recording {
	info, err := os.Stat(c.ownPath())
	if err != nil {
		return recording{}
	}
	r := recording{path: c.ownPath(), mtime: info.ModTime().UnixNano(), size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		r.ino, r.ctime = st.Ino, st.Ctim.Nano()
	}
	return r
}

// load reads the clip if it has not been read, or if the owner's recording has changed since. It is
// called with mu held.
func (c *Clip) load() {
	if c.made {
		return
	}
	from := c.ownRecording()
	if c.loaded && from == c.from {
		return
	}
	c.loaded, c.from, c.own, c.last = true, from, false, nil
	if from != (recording{}) {
		samples, err := readOwn(from.path)
		if err == nil {
			c.own = true
			c.set(samples)
			return
		}
		slog.Warn("playing the stock sound instead of the owner's", "file", from.path, "err", err)
	}
	b, err := clipFiles.ReadFile("sounds/" + c.file + ".pcm")
	if err != nil {
		c.set(nil)
		return
	}
	samples := make([]int16, len(b)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	c.set(samples)
}

func (c *Clip) set(samples []int16) {
	var most int
	for _, s := range samples {
		most = max(most, abs(int(s)))
	}
	c.samples = samples
	c.peak = float64(most) / math.MaxInt16
	c.loudMs = loudFor(samples, 20)
	c.quietMs = loudFor(samples, 30)
}

// readOwn is the owner's recording at path as the device plays it. It has to be at the output's rate
// already: a recording at another rate played as if it were right is the right sound at the wrong
// speed.
//
// What is checked is what was opened, so a file swapped for something else on the way is still
// caught: opening does not wait on a pipe, and no more is read than the size allowed.
func readOwn(path string) ([]int16, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a file: %v", info.Mode())
	}
	if info.Size() > largestOwn {
		return nil, fmt.Errorf("%d bytes is longer than a sound needs to be", info.Size())
	}
	b, err := io.ReadAll(io.LimitReader(file, largestOwn+1))
	if err != nil {
		return nil, err
	}
	if len(b) > largestOwn {
		return nil, fmt.Errorf("more than %d bytes is longer than a sound needs to be", largestOwn)
	}
	return checkOwn(b)
}

// checkOwn is a recording's bytes as the device plays them, or why it cannot play them. The setup
// page asks the same of a file before it keeps it, so nothing is kept that would only play the stock
// sound in its place.
func checkOwn(b []byte) ([]int16, error) {
	samples, f, err := MonoWAV(b)
	if err != nil {
		return nil, err
	}
	if f.Rate != Rate {
		return nil, fmt.Errorf("recorded at %d Hz; the device plays %d", f.Rate, Rate)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("holds no sound")
	}
	if len(samples) > longestOwn {
		return nil, fmt.Errorf("%d ms is longer than a sound needs to be", len(samples)*1000/Rate)
	}
	return samples, nil
}

// loudFor is how long samples stay within db of their loudest 10 ms: a recorded sound ends in a fade
// long after it has stopped being loud, and the fade is not what anything has to wait out.
func loudFor(samples []int16, db float64) int {
	const win = Rate / 100
	var levels []float64
	for i := 0; i < len(samples); i += win {
		var sum float64
		n := 0
		for _, s := range samples[i:min(i+win, len(samples))] {
			sum += float64(s) * float64(s)
			n++
		}
		levels = append(levels, sum/float64(n))
	}
	top := 0.0
	for _, l := range levels {
		top = max(top, l)
	}
	last := 0
	for i, l := range levels {
		if l >= top*math.Pow(10, -db/10) {
			last = i + 1
		}
	}
	return last * 10
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Ms is how long it plays. The stock one's comes from the file's size, so listing a clip among the
// sounds decodes nothing unless the owner has a recording of their own for it.
func (c *Clip) Ms() int {
	if c.made {
		return len(c.samples) * 1000 / Rate
	}
	if c.ownRecording() != (recording{}) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.load()
		if c.own {
			return len(c.samples) * 1000 / Rate
		}
	}
	return c.stockMs()
}

// stockMs is how long the stock recording plays, from the file's size; 0 where there is none.
func (c *Clip) stockMs() int {
	info, err := fs.Stat(clipFiles, "sounds/"+c.file+".pcm")
	if err != nil {
		return 0
	}
	return int(info.Size()/2) * 1000 / Rate
}

// LoudMs is how long it plays loud, before its fade; QuietMs how long before the fade is 30 dB down.
func (c *Clip) LoudMs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.loudMs
}

func (c *Clip) QuietMs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.quietMs
}

// Own reports whether the owner's recording is what plays.
func (c *Clip) Own() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.own
}

// Note is the clip as a note, so it goes wherever notes go: a chime, a ring, one round of an alarm.
func (c *Clip) Note() Note { return Note{Clip: c, Ms: c.Ms()} }

// stockNote is the clip as a note without reading anything, for the tables made as the program
// starts: an owner's file read then would be read before there is a log to say why it cannot play,
// and not read again. Length and Audible ask the clip itself, so the length in it is only a default.
func (c *Clip) stockNote() Note { return Note{Clip: c, Ms: c.stockMs()} }

// ownNote is the owner's recording as a note, and whether there is one, read once so that the answer
// and the length agree.
func (c *Clip) ownNote() (Note, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if !c.own {
		return Note{}, false
	}
	return Note{Clip: c, Ms: len(c.samples) * 1000 / Rate}, true
}

// render is the clip at level. A tone's level is its peak, and a clip is recorded at its own; at the
// feedback tones' level it plays as it was recorded, louder in proportion to a louder level, and
// never past full scale, since a recording mixed near the top has nowhere left to go.
func (c *Clip) render(level float64) []int16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	gain := level / toneLevel
	if c.peak > 0 {
		gain = min(gain, 0.98/c.peak)
	}
	if c.last != nil && c.lastGain == gain {
		return c.last // callers copy it into what they queue (Chime, Bell); nothing writes to it
	}
	out := make([]int16, len(c.samples)*Channels)
	for i, s := range c.samples {
		v := int16(math.Round(float64(s) * gain))
		for ch := range Channels {
			out[i*Channels+ch] = v
		}
	}
	if !c.own {
		c.last, c.lastGain = out, gain
	}
	return out
}
