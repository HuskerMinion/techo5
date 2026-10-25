// Package loopback keeps what the speaker played, for a device with no loopback channel of its own.
//
// On an Echo the codec hands the playback back to the capture stream, sample-aligned, as the reference
// the echo canceller subtracts. A panel plays through Android, which keeps that to itself, so the
// speaker writes what it plays here and the microphone reads it back: 16 kHz mono, counted in samples
// since the start.
package loopback

import "sync"

// size is how much is kept, in samples: about four seconds, far past any delay worth lining up.
const size = 1 << 16

// Ring is a history of played samples, addressed by how many had been written before each one.
type Ring struct {
	mu  sync.Mutex
	buf [size]int16
	w   uint64
}

// Played is the speaker's history.
var Played = &Ring{}

// Write appends samples.
func (r *Ring) Write(s []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range s {
		r.buf[r.w%size] = v
		r.w++
	}
}

// Written is how many samples have been written in all.
func (r *Ring) Written() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.w
}

// ReadAt fills dst with the samples starting at index start. Anything not written yet, or already
// overwritten, reads as silence.
func (r *Ring) ReadAt(dst []int16, start int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := int64(r.w)
	for i := range dst {
		at := start + int64(i)
		if at < 0 || at >= w || w-at > size {
			dst[i] = 0
			continue
		}
		dst[i] = r.buf[uint64(at)%size]
	}
}
