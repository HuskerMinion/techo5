package mic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

type scriptedNativeRead struct {
	data []byte
	err  error
}

type scriptedNativeCapture struct {
	cfg   alsa.Config
	steps []scriptedNativeRead
	reads int
}

func (c *scriptedNativeCapture) Read(dst []byte) (int, error) {
	if c.reads >= len(c.steps) {
		return 0, fmt.Errorf("unexpected capture Read %d", c.reads+1)
	}
	step := c.steps[c.reads]
	c.reads++
	if len(step.data) > len(dst) {
		return 0, fmt.Errorf("scripted read exceeds destination: %d > %d", len(step.data), len(dst))
	}
	return copy(dst, step.data), step.err
}

func (*scriptedNativeCapture) Close() error          { return nil }
func (c *scriptedNativeCapture) Config() alsa.Config { return c.cfg }

func nativeShortReadFixture() (alsa.Config, []byte, []scriptedNativeRead) {
	cfg := alsa.Config{Channels: Channels, Rate: Rate, Bits: 24, Format: alsa.FormatS24_3LE}
	raw := make([]byte, 320*Channels*3)
	for i := range raw {
		raw[i] = byte(i%251 + 1)
	}
	first := 173 * Channels * 3
	return cfg, raw, []scriptedNativeRead{
		{data: raw[:first]},
		{data: raw[first:]},
	}
}

// A partial ALSA success is not a new descriptor: each assembled block
// must be passed to External once, retaining the resident helper.
func TestNativeShortReadFullBlockAndResidentASP(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	if Channels == 6 && (len(steps[0].data) != 3114 || len(steps[1].data) != 2646 || len(want) != 5760) {
		t.Fatal("six-channel 173+147-frame fixture no longer matches Rook")
	}
	// The second block uses three shorter reads but exactly the same format.
	three := []scriptedNativeRead{
		{data: want[:101*Channels*3]},
		{data: want[101*Channels*3 : 214*Channels*3]},
		{data: want[214*Channels*3:]},
	}
	p := aspTestFake(42)
	var launched asp.Descriptor
	s := aspTestSource(t, func(_ context.Context, d asp.Descriptor) (aspProcess, error) {
		launched = d
		return p, nil
	})
	c := &scriptedNativeCapture{cfg: cfg, steps: append(append([]scriptedNativeRead{}, steps...), three...)}
	var baseline asp.Descriptor
	reads, visited := 0, 0
	for blockIndex, parts := range []int{2, 3} {
		dst := make([]byte, len(want))
		var response *aspResponse
		n, cut, err := readCaptured(c, dst, fixedCut(false), func(b NativeBlock) {
			visited++
			if len(b.Buffers) != 1 || len(b.Buffers[0]) != len(want) || &b.Buffers[0][0] != &dst[0] {
				t.Fatal("composite native capture copied or truncated PCM")
			}
			if !bytes.Equal(b.Buffers[0], want) {
				t.Fatal("partial reads were not assembled in sample order")
			}
			d := b.Descriptor()
			if len(d.Buffers) != 1 || d.Buffers[0].Frames != 320 ||
				d.Buffers[0].Length != uint32(len(want)) || d.Buffers[0].SampleRate != Rate ||
				d.Buffers[0].Format != asp.FormatS24_3 || len(d.Channels) != Channels {
				t.Fatalf("bad composite descriptor: %+v", d)
			}
			if blockIndex == 0 {
				baseline = d
			} else if !slices.Equal(baseline.Buffers, d.Buffers) || !slices.Equal(baseline.Channels, d.Channels) {
				t.Fatal("descriptor drift between two-read and three-read blocks")
			}
			response = s.captureNative(b)
		})
		reads += parts
		if err != nil || cut || n != len(want) || c.reads != reads || visited != blockIndex+1 {
			t.Fatalf("capture block %d: bytes=%d cut=%v reads=%d visits=%d err=%v", blockIndex, n, cut, c.reads, visited, err)
		}
		if blockIndex == 0 {
			if response != nil {
				t.Fatal("launch must remain asynchronous")
			}
			aspTestAwait(t, func() bool { return aspTestReady(s) })
			if len(launched.Buffers) != 1 || launched.Buffers[0].Length != uint32(len(want)) {
				t.Fatalf("incomplete descriptor launched helper: %+v", launched)
			}
		} else if response == nil {
			t.Fatal("stable full-block geometry unexpectedly fell back")
		}
	}
	select {
	case <-p.canceled:
		t.Fatal("short reads canceled the resident ASP session")
	default:
	}
}

func TestNativeShortReadSoftwareCutAfterFullBlock(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	c := &scriptedNativeCapture{cfg: cfg, steps: steps}
	dst := make([]byte, len(want))
	policies, visits := 0, 0
	n, cut, err := readCaptured(c, dst, func() bool {
		policies++
		if c.reads != 2 {
			t.Fatal("SoftwareCut sampled before full native block")
		}
		return true
	}, func(b NativeBlock) {
		visits++
		if !bytes.Equal(b.Buffers[0], make([]byte, len(want))) {
			t.Fatal("one of the native partial segments escaped SoftwareCut")
		}
	})
	if err != nil || !cut || n != len(want) || policies != 1 || visits != 1 ||
		!bytes.Equal(dst, make([]byte, len(want))) {
		t.Fatalf("composite mute: n=%d cut=%v policy=%d visit=%d err=%v", n, cut, policies, visits, err)
	}
}

func TestNativeShortReadErrorsDoNotVisitOrSampleCut(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	failure := errors.New("capture driver failure")
	for _, tc := range []struct {
		name  string
		steps []scriptedNativeRead
		cause error
		reads int
	}{
		{"initial zero", []scriptedNativeRead{{}}, nil, 1},
		{"zero after partial", []scriptedNativeRead{steps[0], {}}, nil, 2},
		{"error after partial", []scriptedNativeRead{steps[0], {err: failure}}, failure, 2},
		{"bytes and error on second read", []scriptedNativeRead{steps[0], {data: steps[1].data[:Channels*3], err: failure}}, failure, 2},
		{"XRUN after partial", []scriptedNativeRead{steps[0], {err: alsa.ErrOverrun}}, alsa.ErrOverrun, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &scriptedNativeCapture{cfg: cfg, steps: tc.steps}
			policies, visits := 0, 0
			n, cut, err := readCaptured(c, make([]byte, len(want)), func() bool {
				policies++
				return true
			}, func(NativeBlock) { visits++ })
			if err == nil || (tc.cause != nil && !errors.Is(err, tc.cause)) || cut ||
				c.reads != tc.reads || policies != 0 || visits != 0 {
				t.Fatalf("partial failure: n=%d cut=%v reads=%d policies=%d visits=%d err=%v",
					n, cut, c.reads, policies, visits, err)
			}
			if n != len(tc.steps[0].data) {
				t.Fatalf("unexpected accumulated byte count: got %d want %d", n, len(tc.steps[0].data))
			}
		})
	}
}

func TestNativeShortReadBuiltInSingleRead(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	c := &scriptedNativeCapture{cfg: cfg, steps: steps}
	dst := make([]byte, len(want))
	policies := 0
	n, cut, err := readCaptured(c, dst, func() bool {
		policies++
		return false
	}, nil)
	if err != nil || cut || n != 173*Channels*3 || c.reads != 1 || policies != 1 ||
		!bytes.Equal(dst[:n], want[:n]) {
		t.Fatalf("stock Read changed: n=%d cut=%v reads=%d policies=%d err=%v", n, cut, c.reads, policies, err)
	}
	// The second partial input remains available for the next stock Read,
	// rather than being silently consumed while no native callback exists.
	n, _, err = readCaptured(c, dst, fixedCut(false), nil)
	if err != nil || n != 147*Channels*3 || c.reads != 2 ||
		!bytes.Equal(dst[:n], want[173*Channels*3:]) {
		t.Fatal("stock capture consumed an extra read or changed PCM")
	}
	// A backend without ALSA Config never retries even if a native callback
	// is requested; it reports unsupported native geometry instead.
	stockOnly := &stockOnlyShortCapture{}
	cutCalls := 0
	_, cut, err = readCaptured(stockOnly, make([]byte, 100), func() bool {
		cutCalls++
		return false
	}, func(NativeBlock) { t.Fatal("native callback on non-ALSA backend") })
	if !errors.Is(err, errNativeGeometry) || cut || stockOnly.reads != 1 || cutCalls != 1 {
		t.Fatalf("non-ALSA backend contract changed: err=%v reads=%d cut=%v policies=%d",
			err, stockOnly.reads, cut, cutCalls)
	}
}

type stockOnlyShortCapture struct{ reads int }

func (c *stockOnlyShortCapture) Read(dst []byte) (int, error) {
	c.reads++
	return copy(dst, []byte{1, 2, 3}), nil
}
func (*stockOnlyShortCapture) Close() error { return nil }

func TestNativeShortReadXRUNAndRestartKeepResidentASP(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	stopped := errors.New("capture stopped after recovery")
	p := aspTestFake(21)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	// XRUN happens on Read #2, after 173 valid frames. Source.Run must
	// discard the incomplete block, continue, and keep the resident ASP.
	c := &scriptedNativeCapture{cfg: cfg, steps: []scriptedNativeRead{
		steps[0], {err: alsa.ErrOverrun}, {data: want}, {err: stopped},
	}}
	s.devMu.Lock()
	s.pcm = c
	s.devMu.Unlock()
	if err := s.Run(context.Background()); !errors.Is(err, stopped) {
		t.Fatalf("XRUN was not passed through Source.Run recovery: %v", err)
	}
	if c.reads != 4 {
		t.Fatalf("expected XRUN, successful next block, and final error: %d reads", c.reads)
	}
	select {
	case <-p.canceled:
		t.Fatal("XRUN after a partial read terminated resident ASP")
	default:
	}
	if s.SelectedASP() != config.ASPExternal || !aspTestReady(s) {
		t.Fatal("XRUN changed resident ASP state")
	}
}
