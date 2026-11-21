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
	if k.micDevice != 9 || k.micChannels != 4 || k.mics() != Mics || k.micRates[0] != 96000 {
		t.Errorf("not UL9 at 96 kHz, four channels: %d %d %d %v", k.micDevice, k.micChannels, k.mics(), k.micRates)
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
