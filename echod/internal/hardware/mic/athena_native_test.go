//go:build (!dot && !spot && !panel) || donut

package mic

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
)

func fakePaired() *pairedCapture {
	return &pairedCapture{
		k:       &athenaKernel{micChannels: Mics, micFull: 1 << 23, refRate: Rate},
		micRate: Rate, boost: 1,
	}
}

func TestPairedOriginalStereoBeforeConversion(t *testing.T) {
	c := fakePaired()
	out := make([]byte, 2*Channels*Bits/8)
	mic := make([]byte, 2*Mics*4)
	ref := []byte{0xa1, 0xb2, 0xc3, 0xd4, 0x11, 0x22, 0x33, 0x44}
	for i := 0; i < len(mic); i += 4 {
		binary.LittleEndian.PutUint32(mic[i:], 0x12345)
	}
	visited := 0
	n, _, err := c.readWithNative(out, fixedCut(false), func(b NativeBlock) {
		visited++
		if len(b.Buffers) != 2 || len(b.Geometry.Buffers) != 2 {
			t.Fatalf("paired REF missing: %+v", b.Geometry)
		}
		if !bytes.Equal(b.Buffers[0], mic) || !bytes.Equal(b.Buffers[1], ref) {
			t.Fatal("samples changed before convert")
		}
		if &b.Buffers[0][0] != &c.micBuf[0] || &b.Buffers[1][0] != &c.refBuf[0] {
			t.Fatal("native buffer copied")
		}
		if b.Geometry.Buffers[0].SampleRate != Rate || b.Geometry.Buffers[1].SampleRate != Rate ||
			b.Geometry.Buffers[0].Format != asp.FormatS32 || b.Geometry.Buffers[1].Format != asp.FormatS16 ||
			b.Geometry.Channels[len(b.Geometry.Channels)-2].Role != asp.RoleREF ||
			b.Geometry.Channels[len(b.Geometry.Channels)-1].RoleIndex != 1 {
			t.Fatalf("wrong capture descriptor: %+v", b.Geometry)
		}
		if _, err := asp.Encode(b.Geometry); err != nil {
			t.Fatalf("codec rejected actual geometry: %v", err)
		}
	},
		func(b []byte) error { copy(b, mic); return nil },
		func(b []byte) error { copy(b, ref); return nil },
		func(int) { t.Fatal("unexpected software REF") })
	if err != nil || n != len(out) || visited != 1 {
		t.Fatalf("paired read %d, %v, callbacks %d", n, err, visited)
	}
	if !bytes.Equal(c.refBuf, ref) {
		t.Fatal("stock converter changed original stereo REF")
	}
	// Stock REF first channel is explicitly a mix, so it cannot stand in for native L.
	if bytes.Equal(out[3*RefFirst:3*(RefFirst+1)], ref[:3]) {
		t.Fatal("wrong read order: callback occurred after convert")
	}
}

func TestPairedCutAlsoMutesStockSoftwareReference(t *testing.T) {
	c := fakePaired()
	c.k.softRef = true
	raw := bytes.Repeat([]byte{0x55}, Mics*4)
	out := bytes.Repeat([]byte{0x77}, Channels*3)
	fill := func(n int) {
		c.softL = []float32{0.6}
		c.softR = []float32{-0.4}
		c.softOK = true
	}
	_, _, err := c.readWithNative(out, fixedCut(true), func(b NativeBlock) {
		if len(b.Buffers) != 1 {
			t.Fatal("software REF falsely advertised as hardware REF")
		}
		if !bytes.Equal(b.Buffers[0], make([]byte, len(raw))) {
			t.Fatal("native MIC is audible during SoftwareCut")
		}
	}, func(b []byte) error { copy(b, raw); return nil }, nil, fill)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, make([]byte, len(out))) {
		t.Fatal("stock software REF leaks during SoftwareCut")
	}
	// No filter/history reset: follow-up unmute runs the same capture object.
	_, _, err = c.readWithNative(out, fixedCut(false), nil, func(b []byte) error { copy(b, raw); return nil }, nil, fill)
	if err != nil || bytes.Equal(out, make([]byte, len(out))) {
		t.Fatal("stock unmute/soft REF failed")
	}
	if c.got != 2 {
		t.Fatalf("mute reset frame position: %d", c.got)
	}
}

func TestBorrowedUntilNextReadAndHardwareREFUnchanged(t *testing.T) {
	c := fakePaired()
	out := make([]byte, Channels*3)
	seq := byte(1)
	var first NativeBlock
	readMic := func(b []byte) error {
		for i := range b {
			b[i] = seq
		}
		return nil
	}
	readRef := func(b []byte) error {
		for i := range b {
			b[i] = seq + 0x10
		}
		return nil
	}
	_, _, err := c.readWithNative(out, fixedCut(false), func(b NativeBlock) { first = b }, readMic, readRef, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	if first.Buffers[0][0] != 1 || first.Buffers[1][0] != 0x11 {
		t.Fatal("hardware REF incorrectly muted with MIC")
	}
	seq = 2
	_, _, err = c.readWithNative(out, fixedCut(false), nil, readMic, readRef, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	// The same bytes are borrowed and reused, never retained by Source or copied.
	if first.Buffers[0][0] != 2 || first.Buffers[1][0] != 0x12 {
		t.Fatal("borrowed lifetime is not next read")
	}
	seq = 3
	_, _, err = c.readWithNative(out, fixedCut(true), func(b NativeBlock) {
		if !bytes.Equal(b.Buffers[0], make([]byte, len(b.Buffers[0]))) ||
			!bytes.Equal(b.Buffers[1], make([]byte, len(b.Buffers[1]))) {
			t.Fatal("software cut did not mute BOTH REF channels")
		}
	}, readMic, readRef, func(int) {})
	if err != nil || !bytes.Equal(out, make([]byte, len(out))) {
		t.Fatal("software cut did not mute stock data")
	}
	seq = 4
	_, _, err = c.readWithNative(out, fixedCut(false), nil, readMic, readRef, func(int) {})
	if err != nil || c.refBuf[0] != 0x14 || c.got != 4 {
		t.Fatal("unmute changed capture lifecycle/REF")
	}
}

func TestPairedS32High24Geometry(t *testing.T) {
	c := fakePaired()
	c.k.micFull = 1 << 31
	c.k.refRate = Rate
	out := make([]byte, Channels*3)
	_, _, err := c.readWithNative(out, fixedCut(false), func(b NativeBlock) {
		if b.Geometry.Buffers[0].ValidBits != 24 || b.Geometry.Buffers[0].LSBShift != 8 {
			t.Fatalf("left-aligned S32 geometry: %+v", b.Geometry.Buffers[0])
		}
	}, func(b []byte) error { clear(b); return nil }, nil, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
}

// N03: the policy is sampled once after BOTH ALSA buffers have arrived.
func TestPairedPolicyBoundaryTiming(t *testing.T) {
	c := fakePaired()
	out := make([]byte, Channels*3)
	step, called := 0, 0
	n, cut, err := c.readWithNative(out, func() bool {
		called++
		if step != 2 {
			t.Fatalf("policy read before MIC+REF complete: %d", step)
		}
		return true
	}, nil,
		func(b []byte) error { step = 1; clear(b); return nil },
		func(b []byte) error {
			if step != 1 {
				t.Fatal("REF before MIC")
			}
			step = 2
			clear(b)
			return nil
		},
		func(int) {})
	if err != nil || !cut || n != len(out) || called != 1 {
		t.Fatalf("cut=%v n=%d err=%v calls=%d", cut, n, err, called)
	}
}
