package speaker

import (
	"embed"
	"encoding/binary"
	"io/fs"
	"math"
	"sync"
)

// Recorded sounds, where a tone is a note made here: the Home Assistant Voice sounds (sounds/LICENSE.md,
// CC BY 4.0, Clayton Charles Tapp), so a TECHO5 device sounds like the Home Assistant satellites
// beside it (techo5#34). They are kept as they play - 16-bit mono at the output's rate, converted from
// the originals' FLAC when they were added - so nothing is decoded on the device.

//go:embed sounds/*.pcm
var clipFiles embed.FS

// Clip is one recorded sound.
type Clip struct {
	file string

	once    sync.Once
	samples []int16 // mono, at Rate
	peak    float64 // the loudest sample, as a share of full scale
	loudMs  int     // how long it stays within 20 dB of its loudest (Audible)
	quietMs int     // how long it stays within 30 dB (Audible, with no canceller)

	// The last rendering and the gain it was made at: a ring plays the same clip at the same level
	// round after round.
	mu       sync.Mutex
	lastGain float64
	last     []int16
}

var (
	ClipWake    = &Clip{file: "wake_word_triggered"}
	ClipTimer   = &Clip{file: "timer_finished"}
	ClipMuteOn  = &Clip{file: "mute_switch_on"}
	ClipMuteOff = &Clip{file: "mute_switch_off"}
)

func (c *Clip) load() {
	c.once.Do(func() {
		b, err := clipFiles.ReadFile("sounds/" + c.file + ".pcm")
		if err != nil {
			return
		}
		c.samples = make([]int16, len(b)/2)
		var most int
		for i := range c.samples {
			s := int16(binary.LittleEndian.Uint16(b[2*i:]))
			c.samples[i] = s
			most = max(most, abs(int(s)))
		}
		c.peak = float64(most) / math.MaxInt16
		c.loudMs = loudFor(c.samples, 20)
		c.quietMs = loudFor(c.samples, 30)
	})
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

// Ms is how long it plays, from the file's size, so listing a clip among the sounds decodes nothing.
func (c *Clip) Ms() int {
	info, err := fs.Stat(clipFiles, "sounds/"+c.file+".pcm")
	if err != nil {
		return 0
	}
	return int(info.Size()/2) * 1000 / Rate
}

// LoudMs is how long it plays loud, before its fade; QuietMs how long before the fade is 30 dB down.
func (c *Clip) LoudMs() int {
	c.load()
	return c.loudMs
}

func (c *Clip) QuietMs() int {
	c.load()
	return c.quietMs
}

// Note is the clip as a note, so it goes wherever notes go: a chime, a ring, one round of an alarm.
func (c *Clip) Note() Note { return Note{Clip: c, Ms: c.Ms()} }

// render is the clip at level. A tone's level is its peak, and a clip is recorded at its own; at the
// feedback tones' level it plays as it was recorded, louder in proportion to a louder level, and
// never past full scale, since a recording mixed near the top has nowhere left to go.
func (c *Clip) render(level float64) []int16 {
	c.load()
	gain := level / toneLevel
	if c.peak > 0 {
		gain = min(gain, 0.98/c.peak)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.last, c.lastGain = out, gain
	return out
}
