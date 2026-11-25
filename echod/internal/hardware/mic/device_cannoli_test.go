//go:build dot && cannoli

package mic

import "testing"

// The converter's four channels off eTDM1, on UL9 of the speaker's card, with the converter woken and
// the interconnect set; the three that are microphones go into the frame, in athena's layout: the
// microphones, then the two reference channels.
func TestTheDot5ReadsTheConvertersFourChannels(t *testing.T) {
	k := athenaRunning()
	if k != &cannoliKernel {
		t.Fatal("the paired capture is not the Dot 5's")
	}
	if k.micDevice != 9 || k.micChannels != 6 || k.mics() != Mics || k.micRates[0] != 96000 {
		t.Errorf("not UL9 at 96 kHz, six channels: %d %d %d %v", k.micDevice, k.micChannels, k.mics(), k.micRates)
	}
	if len(k.streamRef) != 2 || k.streamRef[0] != 4 || k.streamRef[1] != 5 {
		t.Errorf("the playback is not read from UL9's channels 4 and 5: %v", k.streamRef)
	}
	ref := map[string]uint32{"O30 I40 Switch": 1, "O31 I41 Switch": 1}
	for _, c := range k.refControls {
		if ref[c.name] != c.value {
			t.Errorf("reference control %s = %d", c.name, c.value)
		}
		delete(ref, c.name)
	}
	if len(ref) != 0 {
		t.Errorf("reference controls not written: %v", ref)
	}
	if Channels != Mics+Refs || RefFirst != Mics {
		t.Errorf("frame: %d channels, references from %d", Channels, RefFirst)
	}
	want := map[string]uint32{"ADC Reset A": 1, "O26 I42 Switch": 1, "O27 I43 Switch": 1, "O28 I44 Switch": 1, "O29 I45 Switch": 1}
	for _, c := range k.controls {
		if want[c.name] != c.value {
			t.Errorf("control %s = %d", c.name, c.value)
		}
		delete(want, c.name)
	}
	if len(want) != 0 {
		t.Errorf("controls not written: %v", want)
	}
}

// micUse: seven channels in, three out (a stream wider than its microphones): the first three come out at
// 16 kHz and the rest of the stream is left out of the frame.
func TestTheDot5FrameCarriesOnlyItsMicrophones(t *testing.T) {
	k := &athenaKernel{micChannels: 7, micUse: 3, micFull: 1 << 31, refDevice: -1, refRate: Rate}
	c := &pairedCapture{k: k, micRate: Rate, boost: 1}
	frames := 4
	mics := make([]byte, frames*7*4)
	for f := range frames {
		for ch := range 7 {
			v := int32((ch + 1) << 27)
			at := (f*7 + ch) * 4
			mics[at], mics[at+1], mics[at+2], mics[at+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		}
	}
	out := make([]byte, frames*Channels*3)
	c.convert(out, mics, nil)
	for f := range frames {
		for ch := range 3 {
			at := f*Channels*3 + ch*3
			got := int32(uint32(out[at])<<8|uint32(out[at+1])<<16|uint32(out[at+2])<<24) >> 8
			want := int32((ch + 1) << 19)
			if got < want-2 || got > want+2 {
				t.Errorf("frame %d ch%d = %d, want %d", f, ch, got, want)
			}
		}
	}
}

// The playback looped into UL9 comes out as the frame's reference, at the microphones' full scale and
// without their boost, decimated with them; the converter's unwired fourth slot goes nowhere.
func TestTheDot5ReferenceIsTheLoopbackInTheStream(t *testing.T) {
	k := &athenaKernel{micChannels: 6, micUse: 3, micFull: 1 << 31, refDevice: -1, streamRef: []int{4, 5}, refRate: Rate}
	c := newPairedCaptureFor(k, Rate)
	c.inStream = true
	c.boost = 4
	frames := 4
	mics := make([]byte, frames*6*4)
	put := func(f, ch int, v int32) {
		at := (f*6 + ch) * 4
		mics[at], mics[at+1], mics[at+2], mics[at+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	for f := range frames {
		put(f, 0, 1<<27)
		put(f, 3, 1<<30) // the unwired slot
		put(f, 4, 1<<29)
		put(f, 5, -(1 << 28))
	}
	out := make([]byte, frames*Channels*3)
	c.convert(out, mics, nil)
	s24 := func(at int) int32 { return int32(uint32(out[at])<<8|uint32(out[at+1])<<16|uint32(out[at+2])<<24) >> 8 }
	near := func(got, want int32) bool { return got >= want-2 && got <= want+2 }
	for f := range frames {
		base := f * Channels * 3
		if got := s24(base); !near(got, 4*(1<<19)) {
			t.Errorf("frame %d mic0 = %d", f, got)
		}
		// The first reference channel is the mix of the two (putRef), the second the second as played.
		if got, want := s24(base+3*RefFirst), int32((1<<21-1<<20)/2); !near(got, want) {
			t.Errorf("frame %d ref mix = %d, want %d", f, got, want)
		}
		if got := s24(base + 3*(RefFirst+1)); !near(got, -(1 << 20)) {
			t.Errorf("frame %d ref R = %d", f, got)
		}
	}
}
