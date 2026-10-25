//go:build !dot && !spot && !panel

package mic

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

// asShow8 sets the frame to the Show 8's for one test: a workstation reads as a Show 5.
func asShow8(t *testing.T) {
	c, m, r := Channels, Mics, RefFirst
	Channels, Mics, RefFirst = show8Channels, show8Mics, show8RefFirst
	t.Cleanup(func() { Channels, Mics, RefFirst = c, m, r })
}

// athenaSignal is the same few tones at any rate, so the two devices can be given the same sound.
func athenaSignal(i, rate int) float64 {
	t := float64(i) / float64(rate)
	return 0.3*math.Sin(2*math.Pi*310*t) + 0.2*math.Sin(2*math.Pi*1730*t+1) + 0.1*math.Sin(2*math.Pi*3190*t+2)
}

// athenaBlock is one Read's worth of both devices: athenaSignal at level on microphone mic, and on the
// reference's channel ref.
func athenaBlock(frames, block, mic int, micLevel float64, ref int, refLevel float64) (mics, refs []byte) {
	micN := frames * athenaMicRate / Rate
	for i := range micN {
		at := block*micN + i
		for ch := range athenaMicChannels {
			v := int32(0)
			if ch == mic {
				// 24 bits, sign-extended in the low three bytes, as the AFE hands them over.
				v = int32(micLevel * athenaSignal(at, athenaMicRate) * (athenaMicFull - 1))
			}
			mics = binary.LittleEndian.AppendUint32(mics, uint32(v))
		}
	}
	refN := frames * athenaRefRate / Rate
	for i := range refN {
		at := block*refN + i
		for ch := range athenaRefChannels {
			v := int16(0)
			if ch == ref {
				v = int16(refLevel * athenaSignal(at, athenaRefRate) * math.MaxInt16)
			}
			refs = binary.LittleEndian.AppendUint16(refs, uint16(v))
		}
	}
	return mics, refs
}

func rmsInt16(xs []int16, skip int) float64 {
	var sum float64
	for _, x := range xs[skip:] {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum / float64(len(xs)-skip))
}

func asFloats(xs []int16) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = float64(x)
	}
	return out
}

// The two devices come out as the 1st gen Show 8's frame: each microphone in its own channel at
// Fire OS's level after its boost, the reference's mix and right after them, and the two lined up.
func TestAthenaMakesTheShow8Frame(t *testing.T) {
	asShow8(t)
	c := newPairedCapture(nil, nil, athenaMicRate)

	const frames, blocks = FrameSamples, 50
	var all []byte
	for b := range blocks {
		mics, refs := athenaBlock(frames, b, 2, 0.5, 1, 0.5)
		out := make([]byte, frames*Channels*Bits/8)
		c.convert(out, mics, refs)
		all = append(all, out...)
	}

	got, ref := Decode(all), Reference(all)
	// Well past the filters filling.
	const skip = 2 * FrameSamples

	boost := math.Pow(10, athenaMicBoostDB/20)
	want := 0.5 * rmsOfSignal() * boost * math.MaxInt16
	for ch := range Mics {
		l := rmsInt16(got[ch], skip)
		switch {
		case ch == 2 && math.Abs(l-want) > 0.02*want:
			t.Errorf("microphone 2: level %.0f, want %.0f", l, want)
		case ch != 2 && l > 1:
			t.Errorf("microphone %d is silent in, but came out at %.0f", ch, l)
		}
	}
	wantRef := 0.5 * rmsOfSignal() * math.MaxInt16
	// The first is the mix the echo canceller reads (putRef): a silent left and the right make half.
	if l := rmsInt16(ref[0], skip); math.Abs(l-wantRef/2) > 0.02*wantRef {
		t.Errorf("reference mix: level %.0f, want %.0f", l, wantRef/2)
	}
	if l := rmsInt16(ref[1], skip); math.Abs(l-wantRef) > 0.02*wantRef {
		t.Errorf("reference right: level %.0f, want %.0f", l, wantRef)
	}

	// The same sound on both devices comes out at the same moment.
	lag, _ := audio.BestLag(asFloats(got[2][skip:]), asFloats(ref[1][skip:]), 40)
	if lag != 0 {
		t.Errorf("the reference is %d samples off the microphones", lag)
	}
}

// Without a reference the frame still comes out whole, with silence where it would be.
func TestAthenaWithoutAReference(t *testing.T) {
	asShow8(t)
	c := newPairedCapture(nil, nil, athenaMicRate)

	mics, _ := athenaBlock(FrameSamples, 0, 0, 0.5, 0, 0)
	out := make([]byte, FrameSamples*Channels*Bits/8)
	for i := range out {
		out[i] = 0xff // whatever the buffer held before
	}
	c.convert(out, mics, nil)
	for _, ch := range Reference(out) {
		if l := rmsInt16(ch, 0); l != 0 {
			t.Errorf("reference without a device came out at %.0f", l)
		}
	}
	if l := rmsInt16(Decode(out)[0], FrameSamples/2); l == 0 {
		t.Error("microphone 0 came out silent")
	}
}

// Lookup takes a control's name whole, so every name written has to fit the kernel's 44 bytes.
func TestAthenaControlNamesFit(t *testing.T) {
	names := []string{athenaGainControl}
	for _, c := range athenaControls {
		names = append(names, c.name)
	}
	for _, n := range names {
		if len(n) >= 44 {
			t.Errorf("%q is too long for a control id", n)
		}
	}
	if athenaControls[0].name != "ADC Reset A" {
		t.Errorf("Fire OS resets the converter first; the list starts with %q", athenaControls[0].name)
	}
}

// rmsOfSignal is athenaSignal's RMS: three tones, each amplitude over root two.
func rmsOfSignal() float64 {
	return math.Sqrt((0.3*0.3 + 0.2*0.2 + 0.1*0.1) / 2)
}

// Amazon's driver does not hold a ring to the DMA block it set aside, and one that does not fit is a
// kernel panic (athena.go). Both rings have to fit, with the period count the reader relies on.
func TestAthenaRingsFitTheDriver(t *testing.T) {
	mic := athenaMicPeriod * athenaMicPeriods * athenaMicChannels * athenaMicBits / 8
	if mic > athenaMicRingMax {
		t.Errorf("microphone ring %d bytes, the driver has %d", mic, athenaMicRingMax)
	}
	ref := athenaRefPeriod * athenaRefPeriods * athenaRefChannels * athenaRefBits / 8
	if ref > athenaRefRingMax {
		t.Errorf("reference ring %d bytes, the driver has %d", ref, athenaRefRingMax)
	}
	// The reader takes 20 ms at a time, so a ring has to hold more than that.
	if athenaMicPeriods*athenaMicPeriod*Rate/athenaMicRate <= FrameSamples {
		t.Errorf("a %d-period microphone ring is no deeper than one read", athenaMicPeriods)
	}
}

// The PMIC path is Fire OS's builtin_Mic_SingleMic (audio_device_tas5805m.xml) after its mic type,
// and every name has to fit a control id.
func TestAthenaPmicPath(t *testing.T) {
	if athenaPmicMicOn[0].name != "Audio_MIC1_Mode_Select" || athenaPmicMicOn[0].item != "ACCMODE" {
		t.Errorf("the mic type goes first: %+v", athenaPmicMicOn[0])
	}
	for _, c := range append(append([]struct{ name, item string }{}, athenaPmicMicOn...), athenaPmicMicOff...) {
		if len(c.name) >= 44 || c.item == "" {
			t.Errorf("%+v", c)
		}
	}
	if athenaADCMicBias {
		t.Error("the converter's MICBIAS is on by default; Fire OS never turns it on")
	}
}

// hw:0,17's samples are 24 bits in the low bytes of each word: a sample at 2^23 - 1 is full scale, a
// steady one comes out at its level after the boost, and one past 24 bits saturates rather than wraps.
func TestAthenaMicSamplesAre24BitLowAligned(t *testing.T) {
	asShow8(t)
	run := func(v int32) int16 {
		c := newPairedCapture(nil, nil, athenaMicRate)
		var mics []byte
		for range 20 * FrameSamples * athenaMicRate / Rate {
			for range athenaMicChannels {
				mics = binary.LittleEndian.AppendUint32(mics, uint32(v))
			}
		}
		out := make([]byte, 20*FrameSamples*Channels*Bits/8)
		c.convert(out, mics, nil)
		d := Decode(out)[0]
		return d[len(d)-1]
	}
	// A quarter of 24-bit full scale, boosted by 5.89 dB, in the 16-bit terms Decode returns.
	want := 0.25 * math.Pow(10, athenaMicBoostDB/20) * math.MaxInt16
	if got := float64(run(1 << 21)); math.Abs(got-want) > 0.01*want {
		t.Errorf("a quarter of 24-bit full scale came out at %.0f, want %.0f", got, want)
	}
	if got := run(1 << 30); got != math.MaxInt16 {
		t.Errorf("a sample past 24 bits came out at %d, want saturation at %d", got, math.MaxInt16)
	}
}

// At the converter's own 16 kHz nothing is decimated: each microphone sample comes out as itself after
// the boost, in its channel, with no filter delay.
func TestAthenaAtRateIsPassedThrough(t *testing.T) {
	asShow8(t)
	c := newPairedCapture(nil, nil, Rate)
	if c.micDec != nil || c.micDelay() != 0 {
		t.Fatal("a capture at Rate decimates")
	}
	const frames = FrameSamples
	var mics []byte
	for i := range frames {
		for ch := range athenaMicChannels {
			v := int32(0)
			if ch == 1 {
				v = int32((i%200 - 100) * 1000)
			}
			mics = binary.LittleEndian.AppendUint32(mics, uint32(v))
		}
	}
	out := make([]byte, frames*Channels*Bits/8)
	c.convert(out, mics, nil)
	got := Decode(out)
	boost := math.Pow(10, athenaMicBoostDB/20)
	for i := range frames {
		want := float64((i%200-100)*1000) / float64(c.k.micFull) * boost * math.MaxInt16
		if d := math.Abs(float64(got[1][i]) - want); d > 1.5 {
			t.Fatalf("frame %d: %d, want %.0f", i, got[1][i], want)
		}
		if got[0][i] != 0 || got[2][i] != 0 {
			t.Fatalf("frame %d: a silent microphone came out at %d/%d", i, got[0][i], got[2][i])
		}
	}
}

// On mainline the reference is DL1_AWB_Record, told apart from an older kernel's device at the same
// number by its id.
func TestAthenaRefInfoNames(t *testing.T) {
	const info = "card: 0\ndevice: 1\nsubdevice: 0\nstream: CAPTURE\nid: DL1_AWB_Record snd-soc-dummy-dai-1\nname: \n"
	if !athenaInfoNames(info, "DL1_AWB_Record") {
		t.Error("DL1_AWB_Record was not recognized")
	}
	old := strings.Replace(info, "DL1_AWB_Record", "Capture_2", 1)
	if athenaInfoNames(old, "DL1_AWB_Record") {
		t.Error("an older kernel's Capture_2 was taken for the reference")
	}
	if athenaInfoNames(strings.Replace(info, "DL1_AWB_Record", "DL1_AWB_Record2", 1), "DL1_AWB_Record") {
		t.Error("a longer id matched")
	}
}

// With the card's reference the mainline microphones run at its rate, so both go through the same
// decimation; without it they stay at that 96 kHz, as before the card had one, and lib/echoref stands in.
func TestAthenaMainlineRates(t *testing.T) {
	k := athenaMainlineKernel
	if k.refDevice < 0 || k.refDevice == k.micDevice || k.refName == "" {
		t.Fatalf("mainline reference device %d (%q), microphones %d", k.refDevice, k.refName, k.micDevice)
	}
	if len(k.micRates) != 1 || k.micRates[0] != athenaRefRate {
		t.Errorf("with the reference the microphones open at %v, want the reference's %d", k.micRates, athenaRefRate)
	}
	if !k.softRef {
		t.Error("without the reference there is no software reference")
	}
	if athenaMicTaps*athenaRefRate != athenaRefTaps*athenaMicRate {
		t.Error("the two decimators do not delay by the same time")
	}
	for _, c := range append(append([]athenaControl{}, k.refControls...), k.controls...) {
		if len(c.name) >= 44 {
			t.Errorf("%q is too long for a control id", c.name)
		}
	}
	// Amazon's kernel is as it was.
	if a := athenaAmazonKernel; a.refDevice != athenaRefDevice || a.softRef || a.refName != "" ||
		len(a.micRates) != 1 || a.micRates[0] != athenaMicRate {
		t.Errorf("Amazon's kernel changed: %+v", a)
	}
}

// Mainline's microphones (24 bits left-aligned in 32) and its reference, both at 96 kHz, come out
// lined up: the same sound on both at the same moment, sample for sample at 16 kHz.
func TestAthenaMainlineReferenceLinesUp(t *testing.T) {
	asShow8(t)
	c := newPairedCapture(nil, nil, athenaMicRate)
	c.k = &athenaMainlineKernel

	const frames, blocks = FrameSamples, 30
	var all []byte
	for b := range blocks {
		mics, refs := athenaBlock(frames, b, 1, 0.5, 0, 0.5)
		for at := 0; at+4 <= len(mics); at += 4 { // as mainline hands them over
			binary.LittleEndian.PutUint32(mics[at:], uint32(int32(binary.LittleEndian.Uint32(mics[at:]))<<8))
		}
		out := make([]byte, frames*Channels*Bits/8)
		c.convert(out, mics, refs)
		all = append(all, out...)
	}
	got, ref := Decode(all), Reference(all)
	const skip = 2 * FrameSamples
	boost := math.Pow(10, athenaMicBoostDB/20)
	want := 0.5 * rmsOfSignal() * boost * math.MaxInt16
	if l := rmsInt16(got[1], skip); math.Abs(l-want) > 0.02*want {
		t.Errorf("microphone 1: level %.0f, want %.0f", l, want)
	}
	lag, _ := audio.BestLag(asFloats(got[1][skip:]), asFloats(ref[0][skip:]), 40)
	if lag != 0 {
		t.Errorf("the reference is %d samples off the microphones", lag)
	}
}

// One second of mainline's capture with the card's reference, four microphones and the reference at
// 96 kHz, decimated to the daemon's frame: what the hardware reference costs an idle unit.
func BenchmarkAthenaConvert96k(b *testing.B) {
	asShow8B(b)
	c := newPairedCapture(nil, nil, athenaMicRate)
	c.k = &athenaMainlineKernel
	mics, refs := athenaBlock(FrameSamples, 0, 1, 0.01, 0, 0)
	for at := 0; at+4 <= len(mics); at += 4 { // a quiet room on every microphone, never exactly zero
		binary.LittleEndian.PutUint32(mics[at:], uint32(int32(at%997+1)<<12))
	}
	out := make([]byte, FrameSamples*Channels*Bits/8)
	b.SetBytes(int64(len(mics) + len(refs)))
	for b.Loop() {
		for range Rate / FrameSamples {
			c.convert(out, mics, refs)
		}
	}
}

func asShow8B(b *testing.B) {
	c, m, r := Channels, Mics, RefFirst
	Channels, Mics, RefFirst = show8Channels, show8Mics, show8RefFirst
	b.Cleanup(func() { Channels, Mics, RefFirst = c, m, r })
}
