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
	"github.com/HuskerMinion/techo5/echod/internal/service"
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

// A partial ALSA success is not a new descriptor: the same physical capture
// block spans multiple synchronous reads and must be passed to External once.
func TestNativeShortReadFullBlockAndResidentASP(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	if Channels == 6 && (len(steps[0].data) != 3114 || len(steps[1].data) != 2646 || len(want) != 5760) {
		t.Fatal("six-channel 173+147-frame fixture no longer matches Rook")
	}
	p := aspTestFake(42)
	var launched asp.Descriptor
	s := aspTestSource(t, func(_ context.Context, d asp.Descriptor) (aspProcess, error) {
		launched = d
		return p, nil
	})
	c := &scriptedNativeCapture{cfg: cfg, steps: append(append([]scriptedNativeRead{}, steps...), steps...)}
	// Each block is assembled in the caller-owned buffer: neither a new PCM
	// allocation nor a borrowed-buffer reference survives the next Read.
	visited := 0
	for blockIndex := 0; blockIndex < 2; blockIndex++ {
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
			if desc := b.Descriptor(); len(desc.Buffers) != 1 ||
				desc.Buffers[0].Frames != 320 || desc.Buffers[0].Length != uint32(len(want)) ||
				desc.Buffers[0].SampleRate != Rate || desc.Buffers[0].Format != asp.FormatS24_3 ||
				len(desc.Channels) != Channels {
				t.Fatalf("bad composite descriptor: %+v", desc)
			}
			response = s.captureNative(b)
		})
		if err != nil || cut || n != len(want) || c.reads != (blockIndex+1)*2 || visited != blockIndex+1 {
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

func TestNativeShortReadDescriptorStability(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	var all []scriptedNativeRead
	for range 4 {
		all = append(all, steps...)
	}
	c := &scriptedNativeCapture{cfg: cfg, steps: all}
	var baseline asp.Descriptor
	for i := 0; i < 4; i++ {
		n, _, err := readCaptured(c, make([]byte, len(want)), fixedCut(false), func(b NativeBlock) {
			if i == 0 {
				baseline = b.Descriptor()
			} else if !slices.Equal(baseline.Buffers, b.Geometry.Buffers) ||
				!slices.Equal(baseline.Channels, b.Geometry.Channels) {
				t.Fatalf("native descriptor drift at block %d", i)
			}
		})
		if err != nil || n != len(want) || c.reads != (i+1)*2 {
			t.Fatalf("composite block %d: read=%d totalReads=%d err=%v", i, n, c.reads, err)
		}
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
}

type stockOnlyShortCapture struct{ reads int }

func (c *stockOnlyShortCapture) Read(dst []byte) (int, error) {
	c.reads++
	return copy(dst, []byte{1, 2, 3}), nil
}
func (*stockOnlyShortCapture) Close() error { return nil }

func TestNativeShortReadNoRetryForNonALSABackend(t *testing.T) {
	c := &stockOnlyShortCapture{}
	cutCalls := 0
	_, cut, err := readCaptured(c, make([]byte, 100), func() bool {
		cutCalls++
		return false
	}, func(NativeBlock) { t.Fatal("native callback on non-ALSA backend") })
	if !errors.Is(err, errNativeGeometry) || cut || c.reads != 1 || cutCalls != 1 {
		t.Fatalf("non-ALSA backend contract changed: err=%v reads=%d cut=%v policies=%d",
			err, c.reads, cut, cutCalls)
	}
}

func TestNativeShortReadXRUNAndRestartKeepResidentASP(t *testing.T) {
	cfg, want, steps := nativeShortReadFixture()
	stopped := errors.New("capture stopped after recovery")
	for _, tc := range []struct {
		name       string
		secondErr  error
		wantErr    error
		extraSteps []scriptedNativeRead
		rewiring   bool
	}{
		{
			name:       "XRUN then next full block",
			secondErr:  alsa.ErrOverrun,
			wantErr:    stopped,
			extraSteps: []scriptedNativeRead{{data: want}, {err: stopped}},
		},
		{
			name:      "handover after partial read",
			secondErr: errors.New("device closed on rewire"),
			wantErr:   service.ErrRestart,
			rewiring:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := aspTestFake(21)
			s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
			s.captureNative(aspTestBlock())
			aspTestAwait(t, func() bool { return aspTestReady(s) })
			c := &scriptedNativeCapture{
				cfg: cfg,
				steps: append([]scriptedNativeRead{
					steps[0], {err: tc.secondErr},
				}, tc.extraSteps...),
			}
			s.devMu.Lock()
			s.pcm = c
			s.devMu.Unlock()
			if tc.rewiring {
				s.rewiring.Store(true)
			}
			err := s.Run(context.Background())
			if tc.rewiring {
				if !errors.Is(err, service.ErrRestart) {
					t.Fatalf("capture handover did not request restart: %v", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("XRUN continuation: got error %v", err)
			}
			if c.reads != len(c.steps) {
				t.Fatalf("capture did not read expected recovery sequence: %d/%d", c.reads, len(c.steps))
			}
			select {
			case <-p.canceled:
				t.Fatal("XRUN or capture restart terminated resident ASP")
			default:
			}
			if s.SelectedASP() != config.ASPExternal || !aspTestReady(s) {
				t.Fatal("capture error changed resident ASP state")
			}
		})
	}
}
