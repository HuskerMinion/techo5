//go:build !dot

package camstream

import (
	"errors"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
)

type fakeChroma struct {
	runs   int
	closed bool
	err    error
	last   vpu.NRParams
}

func (f *fakeChroma) nv12(uv []byte, stride int, p vpu.NRParams) error {
	f.runs++
	f.last = p
	return f.err
}

func (f *fakeChroma) close() { f.closed = true }

func withFakeNR(t *testing.T) (opened *int, last **fakeChroma, failOpen *error) {
	t.Helper()
	n, f, e := 0, (*fakeChroma)(nil), error(nil)
	old := openNR
	openNR = func(w, h int) (chromaFilter, error) {
		if e != nil {
			return nil, e
		}
		n++
		f = &fakeChroma{}
		return f, nil
	}
	t.Cleanup(func() { openNR = old })
	return &n, &f, &e
}

// The color is smoothed from nrOnGain until the gain falls under nrOffGain; the VPU is taken at the
// first dim frame and let go nrIdle after the last. Frames go as the stream has them: the gain set
// before the frame is encoded, then start, then the work it returns (none in good light with the VPU
// let go, so the stream keeps its pictures undelayed).
func TestStreamNRGain(t *testing.T) {
	opened, last, _ := withFakeNR(t)
	n := &streamNR{w: 1456, h: 1088}
	uv := make([]byte, 1456*544)
	var work bool
	frame := func(gain int) {
		n.next = gain
		w := n.start()
		if work = w != nil; work {
			w(uv, 1456)
		}
	}
	frame(2 * 64)
	if *opened != 0 || work {
		t.Fatal("the VPU taken, or work, in good light")
	}
	frame(nrOnGain)
	if *opened != 1 || (*last).runs != 1 {
		t.Fatal("not smoothed at nrOnGain")
	}
	frame(nrOffGain) // between the two: still on
	if (*last).runs != 2 {
		t.Fatal("hysteresis: off too soon")
	}
	frame(nrOffGain - 1)
	if (*last).runs != 2 || (*last).closed {
		t.Fatal("smoothed under nrOffGain, or let go at once")
	}
	n.used = time.Now().Add(-nrIdle)
	frame(64)
	if !(*last).closed || n.f != nil {
		t.Fatal("the VPU kept after nrIdle")
	}
	if frame(64); work {
		t.Fatal("work in good light with the VPU let go")
	}
	frame(nrMaxGain)
	if *opened != 2 || (*last).last != nrHigh {
		t.Fatalf("taken again: %d; at the ceiling: %+v", *opened, (*last).last)
	}
	n.release()
}

// A failure, opening or running, turns it off for the rest of the stream.
func TestStreamNRFails(t *testing.T) {
	opened, last, failOpen := withFakeNR(t)
	n := &streamNR{w: 1456, h: 1088}
	uv := make([]byte, 1456*544)
	n.gain = nrMaxGain
	n.denoise(uv, 1456)
	(*last).err = errors.New("timeout")
	n.denoise(uv, 1456)
	if !n.failed || !(*last).closed {
		t.Fatal("a failed job left it on")
	}
	n.denoise(uv, 1456)
	if *opened != 1 {
		t.Fatal("tried again after a failure")
	}
	m := &streamNR{w: 1456, h: 1088, gain: nrMaxGain}
	*failOpen = errors.New("busy")
	m.denoise(uv, 1456)
	m.denoise(uv, 1456)
	if !m.failed || *opened != 1 {
		t.Fatal("a VPU that would not open was tried again")
	}
}

func TestNRParams(t *testing.T) {
	if p := nrParams(nrOnGain); p != nrLow {
		t.Fatalf("at nrOnGain: %+v", p)
	}
	if p := nrParams(4 * nrMaxGain); p != nrHigh {
		t.Fatalf("past the ceiling: %+v", p)
	}
	if p := nrParams((nrOnGain + nrMaxGain) / 2); p.Strength <= nrLow.Strength || p.Strength >= nrHigh.Strength {
		t.Fatalf("half way: %+v", p)
	}
}
