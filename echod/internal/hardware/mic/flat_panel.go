//go:build panel

package mic

// flatWatch notices a microphone that hands back nothing: Android gives an app zeros, not an error,
// when its microphone is taken away (the privacy switch, another app holding it, a background
// limit), and the daemon would otherwise go on listening to silence with no sign of it.
//
// It looks at whole seconds. A second is flat when every sample in it is within flatSpan of every
// other: 2 steps of 65536, about -84 dBFS from top to bottom. A working microphone should never do
// that, however quiet the room: a MEMS microphone's own noise is around -90 dBFS, which on the voice
// recognition path the app records from (no noise gate) spans several steps in any second, and a room
// adds more. Zeros, or a stream stuck at one value, are flat. Three consecutive flat seconds make
// the likely physical cut visible as mute; 20 seconds raise the diagnostic problem, so a short
// Android audio handoff does not raise it.
type flatWatch struct {
	lo, hi int16
	n      int // samples in the second being looked at
	flat   int // flat seconds in a row
	cut    bool
	silent bool
}

const (
	flatSpan  = 2
	cutAfter  = 3  // seconds: mute state
	flatAfter = 20 // seconds: diagnostic problem
)

// seed starts a watcher where the last one left off: a new connection to the app after the microphone
// went silent is still silent until it hears something, and the first second of sound clears it.
func (w *flatWatch) seed(cut, silent bool) {
	w.cut = cut
	w.silent = silent
	if silent {
		w.flat = flatAfter
	} else if cut {
		w.flat = cutAfter
	}
}

// add takes the next samples (16-bit little-endian mono) and returns whether the stream is silent now
// and whether that just changed.
func (w *flatWatch) add(in []byte) (silent, changed bool) {
	was := w.silent
	for i := 0; i+1 < len(in); i += 2 {
		v := int16(uint16(in[i]) | uint16(in[i+1])<<8)
		if w.n == 0 {
			w.lo, w.hi = v, v
		} else {
			w.lo, w.hi = min(w.lo, v), max(w.hi, v)
		}
		if w.n++; w.n < Rate {
			continue
		}
		if int(w.hi)-int(w.lo) <= flatSpan {
			w.flat++
		} else {
			w.flat = 0
		}
		w.n = 0
		switch {
		case w.flat >= flatAfter:
			w.cut = true
			w.silent = true
		case w.flat >= cutAfter:
			w.cut = true
		case w.flat == 0:
			w.cut = false
			w.silent = false
		}
	}
	return w.silent, w.silent != was
}
