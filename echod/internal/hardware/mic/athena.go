//go:build (!dot && !spot && !panel) || donut

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
	"github.com/HuskerMinion/techo5/echod/internal/lib/echoref"
)

// The 2nd gen Echo Show 8 (athena) on Amazon's own 32-bit 4.4 kernel. Its one card, mt-snd-card,
// has the microphones and the playback reference on two capture devices, where every other board has
// one device with the loopback inside the frame:
//
//   - hw:0,17 "I2S2ADC2_Capture": a TLV320ADC5140, four microphones, ch1/ch2 on its first data line
//     and ch3/ch4 on the second. Fire OS opens it at 96 kHz in 32-bit slots, four channels (asp.cfg
//     "Mics": 4 at 96000; AFE.cfg "Mics Input SamplingRate": 96000), and so does this.
//   - hw:0,9 "DL1_AWB_Record": the AFE writing back what its DL1 memory interface sends the
//     amplifier, so a digital copy of the playback, sample for sample, at the speaker's 96 kHz.
//
// Both are what Fire OS's own HAL opens for the recording Alexa hears: its PcmMerge capture
// provider (AudioALSACaptureDataProviderPcmMerge::open in vendor audio.primary.mt8183.so) takes
// "I2S2ADC2_Capture" as the main stream, S32_LE, 4 channels, 96 kHz, 4 periods, and
// "DL1_AWB_Record" as the sub stream, S16_LE, 2 channels, 96 kHz, the same period, and merges the
// two into asp.cfg's "6-channel pcm8.24 at 96 kHz" input. hw:0,16 (I2S0AWB) and hw:0,33 (I2S0VUL2)
// read digital zero on the unit with the speaker playing (M5, 2026-10-14); the HAL opens I2S0AWB only
// for a USB/BT echo path and never opens I2S0VUL2.
//
// Both are brought down to 16 kHz and interleaved into the 1st gen Show 8's frame (S24_3LE, six
// channels, the microphones then the reference: see device_cronos.go), so the canceller, the
// beamformer, the leveler and everything raw downstream read athena as they read crown.
//
// Alignment. The two streams run off the same AFE clocks, so they do not drift; what can differ is
// where each starts. Both are configured and prepared first and then started back to back, reference
// first, so the reference is never behind the microphones (a canceller can only remove an echo whose
// cause it has already seen). An overrun on either starts both over the same way. The two decimators
// delay by the same time (lib/audio), so they add nothing. `techo5 echo` reports the lag that is
// left.
const (
	athenaMicDevice   = 17
	athenaMicRate     = 96000
	athenaMicChannels = 4
	athenaMicBits     = 32

	athenaRefDevice   = 9
	athenaRefRate     = 96000
	athenaRefChannels = 2
	athenaRefBits     = 16

	// athenaMicFull is the microphones' full scale. hw:0,17 is S32_LE by name, but the AFE hands over
	// the converter's 24 bits sign-extended in the low three bytes, not left-aligned: on recordings of
	// the unit (2026-10-14) the largest sample was about 2^17.5 and the low byte was set in 99.6% of
	// samples, which a left-aligned 24-bit sample never does. Fire OS reads the same stream as "pcm8.24"
	// (asp.cfg). Read against 2^31, M5 to M7 heard everything 48 dB too quiet: a quiet room at -103 dBFS
	// rather than about -55, and a wake word that never fired. A sample past 24 bits saturates in
	// putS24.
	athenaMicFull = 1 << 23

	// 10 ms periods, and rings that fit what Amazon's driver set aside for each device. hw_params
	// advertises far more than that (16384 frames on hw:0,17) and does not check: asked for a ring
	// larger than its DMA block, the driver clears past the end of it in hw_params and the kernel
	// panics (M4 on the unit, 2026-10-14: a 61440-byte ring on hw:0,17, data abort in set_mem_block
	// 48 KB in, mtk_i2s2_adc2_pcm_hw_params). So the rings are sized here, never left to the driver:
	//   - hw:0,17: at most athenaMicRingMax. Three periods, 46080 bytes, 30 ms.
	//   - hw:0,9: 4 periods, as Fire OS has it, 15360 bytes: the size hw:0,16 opened cleanly with on
	//     the unit. The driver uses a preallocated block for it the same way (set_mem_block), whose
	//     size is not known, so the ring stays this small.
	athenaMicPeriod  = athenaMicRate / 100
	athenaMicPeriods = 3
	athenaRefPeriod  = athenaRefRate / 100
	athenaRefPeriods = 4

	// athenaMicRingMax is the microphone DMA block that is really there: the next device's block
	// starts 48 KB after hw:0,17's, and the fault came at exactly 48 KB.
	athenaMicRingMax = 48 << 10
	athenaRefRingMax = 15360

	// Decimator lengths in the ratio of the rates, so both delay by the same 1 ms.
	athenaMicTaps = 192
	athenaRefTaps = 192

	// athenaMicBoostDB is what Fire OS adds after the same conversion: "Athena AFE mic input (post
	// 96KHz to 16Khz SRC) levels is 5.89dB lower than Crown" (AFE.cfg, MicBoostdB). With it the room
	// reads at crown's level, which is what the leveler and the wake word were set against.
	athenaMicBoostDB = 5.89
)

// athenaControls are the converter's initial settings, as Fire OS's audio HAL writes them at start
// (vendor /etc/audio_device_tas5805m.xml), in its order: ch1/ch2 in the left and right slots of the
// first data line, ch3/ch4 the same on the second (GPIO1 as SDOUT2), digital volume at 0 dB (201),
// and dynamic range enhancement off. The analog gain, which the same file sets to 20 dB, is the
// daemon's own setting (athenaGain). Nothing here is the speaker's: Amazon's kernel driver sets the
// amplifier up itself.
//
// "ADC Reset A" is not a reset. Its handler in Amazon's driver (adc5140_reset_set) does nothing for 0
// and, for anything else, writes SLEEP_CFG (register 0x02) = 0x81: the internal regulator on and the
// converter out of sleep, then waits 10 ms. The chip comes out of its reset asleep, and nothing else in
// the driver wakes it, so without this write the microphones are never sampled. It always reads back
// "off". Its effect shows in the kernel log as two "adc5140_reset" lines.
var athenaControls = []athenaControl{
	{"ADC Reset A", 1},
	{"Ch1 Output A", 0},
	{"Ch1 Slot Offset A", 0},
	{"Ch2 Output A", 0},
	{"Ch2 Slot Offset A", 32},
	{"Ch3 Output A", 1},
	{"Ch3 Slot Offset A", 0},
	{"Ch4 Output A", 1},
	{"Ch4 Slot Offset A", 32},
	{"Enable Secondary Output A", 3},
	{"Ch1 Digital Capture Vol A", 201},
	{"Ch2 Digital Capture Vol A", 201},
	{"Ch3 Digital Capture Vol A", 201},
	{"Ch4 Digital Capture Vol A", 201},
	{"DRE Trigger Level A", 4},
	{"DRE Max Gain A", 11},
	{"Ch1 DRE Enable A", 0},
	{"Ch2 DRE Enable A", 0},
	{"Ch3 DRE Enable A", 0},
	{"Ch4 DRE Enable A", 0},
}

// athenaGainControl is the converter's analog gain, one control per microphone, in dB.
const athenaGainControl = "Ch%d Gain A"

// athenaKernel is what differs between the two kernels athena can run: Amazon's 4.4 (everything
// above) and TECHO5's mainline one (tools/linux/athena/kernel), whose machine driver is
// mt8183-athena. On mainline the converter is the upstream tlv320adcx140 driver, with its own control
// names; the mics come from the Capture_1 front end (UL1, the AFE's VUL12 in its 4-channel mode),
// hw:0,2: ch1/ch2 off the converter's first data line (I2S2_DI) and ch3/ch4 off its second
// (I2S2_DI2), which needs the kernel's AFE patch for the second data line (patches/patch-afe-di2.py
// there); the samples are 24-bit, left-aligned in 32. The reference is hw:0,1 "DL1_AWB_Record", as on
// Amazon's kernel: the AFE's AWB memory interface (UL2) recording what DL1 sends the amplifier's I2S1,
// through the interconnect switches athenaMainlineRefControls sets (the AFE's routes:
// tools/linux/athena's patches/patch-afe-awb-dl1.py). The speaker amplifier's own output reaches no pin
// the SoC reads, so this digital copy is the closest there is. So both kernels run alike: microphones
// and reference at 96 kHz on the same AFE clocks, decimated by filters of the same delay. A kernel
// without that device, or one where it does not open, keeps the microphones at 96 kHz and takes
// lib/echoref's software reference, as before the card had one.
type athenaKernel struct {
	micDevice   int
	micChannels int
	micFull     float32
	refDevice   int // -1: none
	// refName is the reference's PCM id as the kernel lists it (/proc/asound), checked before it is
	// opened, so an older kernel's device at that number is never taken for it. Empty: not checked.
	refName string
	// refControls are written before the reference is opened; one that fails means no reference.
	refControls []athenaControl
	controls    []athenaControl
	gainControl string
	// softRef: without the card's reference, it is lib/echoref's, made from what the speaker sent.
	softRef bool
	// amazon: the PMIC microphone path and the converter's bias write (athena_bias.go), which
	// Amazon's drivers leave to userspace. The mainline driver powers the bias itself.
	amazon bool
	// micRates are the rates to open the microphones at, the first the card takes. Every athena
	// kernel opens them at 96 kHz, and the decimators in convert make the 16 kHz stream (lib/audio).
	micRates []int
	// refRate is the rate the reference is opened at; at Rate it is used as it comes.
	refRate int
	// boostDB is added to the microphones after conversion (athenaMicBoostDB on athena).
	boostDB float64
	// micPeriods and refPeriods are the rings' lengths in 10 ms periods; zero is athena's
	// (athenaMicPeriods, athenaRefPeriods), held to what Amazon's driver set aside.
	micPeriods, refPeriods int
}

type athenaControl = struct {
	name  string
	value uint32
}

var athenaAmazonKernel = athenaKernel{
	micDevice:   athenaMicDevice,
	micChannels: athenaMicChannels,
	micFull:     athenaMicFull,
	refDevice:   athenaRefDevice,
	controls:    athenaControls,
	gainControl: athenaGainControl,
	amazon:      true,
	micRates:    []int{athenaMicRate},
	refRate:     athenaRefRate,
	boostDB:     athenaMicBoostDB,
}

// athenaMainlineControls are the routes and settings the mainline card needs, as used on unit 1 on
// TECHO5's mainline kernel (2026-10-15): I2S2 in the AFE's low-jitter mode, the AFE interconnect from I2S2's four
// channels into UL1's, the converter's ASI channel enables, and digital volume at 0 dB (201). The
// converter's slots and second output are the machine driver's own. Without low-jitter mode the
// converter sees no valid clocks at 96 kHz (ASI_STS 0xff) and every sample is zero; the machine driver
// sets it at probe, and the write here is a second line of defense.
//
// Which physical microphone each channel is has not been checked yet (a tap test); the order here is
// the converter's.
var athenaMainlineControls = []athenaControl{
	{"I2S2_HD_Mux", 1},
	{"UL1_CH1 I2S2_CH1", 1},
	{"UL1_CH2 I2S2_CH2", 1},
	{"UL1_CH3 I2S2_CH3", 1},
	{"UL1_CH4 I2S2_CH4", 1},
	{"CH1_ASI_EN Switch", 1},
	{"CH2_ASI_EN Switch", 1},
	{"CH3_ASI_EN Switch", 1},
	{"CH4_ASI_EN Switch", 1},
	{"Digital CH1 Out Volume", 201},
	{"Digital CH2 Out Volume", 201},
	{"Digital CH3 Out Volume", 201},
	{"Digital CH4 Out Volume", 201},
}

// athenaMainlineRefControls are the AFE interconnect from DL1's two channels into UL2's (O05/O06 from
// I05/I06), the path DL1_AWB_Record records. The switches only take effect while DL1 plays (DAPM
// powers the path with the stream), so with nothing playing the reference reads zero.
var athenaMainlineRefControls = []athenaControl{
	{"UL2_CH1 DL1_CH1", 1},
	{"UL2_CH2 DL1_CH2", 1},
}

var athenaMainlineKernel = athenaKernel{
	micDevice:   2,
	micChannels: 4,
	micFull:     1 << 31,
	refDevice:   1,
	refName:     "DL1_AWB_Record",
	refControls: athenaMainlineRefControls,
	controls:    athenaMainlineControls,
	gainControl: "Analog CH%d Mic Gain Volume",
	softRef:     true,
	// The microphones at 96 kHz, with the reference and without it: without it the software
	// reference is used, as before the card had one.
	micRates: []int{athenaMicRate},
	refRate:  athenaRefRate,
	boostDB:  athenaMicBoostDB,
}

// athenaRunning is the kernel this unit is on; a workstation reads as Amazon's.
func athenaRunning() *athenaKernel {
	if boardKernel != nil {
		return boardKernel
	}
	if layout.AthenaMainline() {
		return &athenaMainlineKernel
	}
	return &athenaAmazonKernel
}

// athenaWrite sets one control, looked up by name alone: this card has controls that do things when
// read (a timestamp, a playback state), so nothing here walks the list. A control the unit does not
// have is said once and skipped.
func athenaWrite(m *alsa.Mixer, name string, v uint32) bool {
	c, err := m.Lookup(name)
	if err != nil {
		slog.Warn("athena: mixer control not on this unit", "control", name)
		return false
	}
	if err := m.Set(c, v); err != nil {
		slog.Error("athena: mixer write failed", "control", name, "err", err)
		return false
	}
	return true
}

// athenaGain sets the analog gain on every microphone, held to what the control allows.
func athenaGain(m *alsa.Mixer, db int) {
	k := athenaRunning()
	for ch := 1; ch <= k.micChannels; ch++ {
		name := fmt.Sprintf(k.gainControl, ch)
		c, err := m.Lookup(name)
		if err != nil {
			slog.Warn("athena: mixer control not on this unit", "control", name)
			continue
		}
		v := int64(db)
		if c.Max > c.Min {
			v = min(max(v, c.Min), c.Max)
		}
		if err := m.Set(c, uint32(v)); err != nil {
			slog.Error("setting the microphone gain failed", "control", name, "err", err)
			return
		}
	}
	slog.Info("microphone gain", "db", db)
}

// athenaInit writes the kernel's controls before the converter is opened, as the HAL does.
func athenaInit(k *athenaKernel) {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		slog.Error("opening the mixer failed", "err", err)
		return
	}
	defer m.Close()
	failed := 0
	for _, c := range k.controls {
		if !athenaWrite(m, c.name, c.value) {
			failed++
		}
	}
	slog.Info("athena: converter set up", "controls", len(k.controls), "failed", failed)
}

// pairedCapture is the two devices read as one.
type pairedCapture struct {
	mics *alsa.Capture
	// ref is nil when the reference could not be opened: the frame then carries silence there, which
	// the canceller reads as nothing playing, and everything else works.
	ref *alsa.Capture
	k   *athenaKernel
	// micRate is the rate the microphones were opened at (athenaKernel.micRates).
	micRate int

	micBuf, refBuf []byte
	micDec         []*audio.Decimator
	refDec         [athenaRefChannels]*audio.Decimator
	boost          float32
	meter          athenaMeter

	// The software reference (athenaKernel.softRef): clock stamps the microphones' frames, got
	// counts them since the stream started, and soft holds what Fill gave for the current read.
	clock        echoref.Clock
	got          uint64
	softL, softR []float32
	softOK       bool
}

func openAthena() (capture, error) {
	k := athenaRunning()
	athenaInit(k)

	ref := openAthenaRef(k)
	rates := k.micRates
	soft := ref == nil && k.softRef
	echoref.Use(soft)

	var mics *alsa.Capture
	var err error
	micRate := 0
	for _, rate := range rates {
		mics, err = alsa.OpenPrepared(Card, k.micDevice, alsa.Config{
			Channels:   k.micChannels,
			Rate:       rate,
			Format:     alsa.FormatS32_LE,
			Bits:       athenaMicBits,
			PeriodSize: rate / 100,
			Periods:    periodsOr(k.micPeriods, athenaMicPeriods),
		})
		if err == nil {
			micRate = rate
			break
		}
		slog.Warn("athena: microphones refused a rate", "rate", rate, "err", err)
	}
	if err != nil {
		if ref != nil {
			_ = ref.Close()
		}
		return nil, err
	}

	c := newPairedCapture(mics, ref, micRate)
	if err := c.start(); err != nil {
		_ = c.Close()
		return nil, err
	}
	// As Fire OS does once both streams run: the PMIC's microphone path, which is where the bias for
	// the analog microphones is read to come from (athena_bias.go).
	if k.amazon {
		athenaPmicMic(true)
		athenaADCBiasOn()
	}
	slog.Info("athena: capture open", "mics", fmt.Sprintf("hw:%d,%d", Card, k.micDevice),
		"channels", k.micChannels, "rate", micRate, "reference", ref != nil, "ref_device", k.refDevice,
		"software_reference", soft)
	return c, nil
}

// openAthenaRef opens the card's reference, prepared and not started, or says why there is none.
func openAthenaRef(k *athenaKernel) *alsa.Capture {
	if k.refDevice < 0 {
		return nil
	}
	if k.refName != "" && !athenaPCMIs(k.refDevice, k.refName) {
		slog.Info("athena: no reference device on this kernel", "device", k.refDevice, "want", k.refName)
		return nil
	}
	if len(k.refControls) > 0 {
		m, err := alsa.OpenMixer(Card)
		if err != nil {
			slog.Warn("athena: no playback reference", "err", err)
			return nil
		}
		ok := true
		for _, c := range k.refControls {
			ok = athenaWrite(m, c.name, c.value) && ok
		}
		_ = m.Close()
		if !ok {
			slog.Warn("athena: no playback reference, its routes could not be set")
			return nil
		}
	}
	ref, err := alsa.OpenPrepared(Card, k.refDevice, alsa.Config{
		Channels:   athenaRefChannels,
		Rate:       k.refRate,
		Format:     alsa.FormatS16_LE,
		Bits:       athenaRefBits,
		PeriodSize: k.refRate / 100,
		Periods:    periodsOr(k.refPeriods, athenaRefPeriods),
	})
	if err != nil {
		slog.Warn("athena: no playback reference", "device", k.refDevice, "err", err)
		return nil
	}
	return ref
}

// periodsOr is n, or athena's own count where the kernel sets none.
func periodsOr(n, athena int) int {
	if n > 0 {
		return n
	}
	return athena
}

// athenaPCMIs is whether capture device dev of the card is the PCM named name: the kernel lists its
// id as the link's stream name, then the codec's DAI ("DL1_AWB_Record snd-soc-dummy-dai-1").
func athenaPCMIs(dev int, name string) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/asound/card%d/pcm%dc/info", Card, dev))
	if err != nil {
		return false
	}
	return athenaInfoNames(string(b), name)
}

func athenaInfoNames(info, name string) bool {
	for line := range strings.SplitSeq(info, "\n") {
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			return id == name || strings.HasPrefix(id, name+" ")
		}
	}
	return false
}

func newPairedCapture(mics, ref *alsa.Capture, micRate int) *pairedCapture {
	k := athenaRunning()
	c := &pairedCapture{mics: mics, ref: ref, k: k, micRate: micRate,
		boost: float32(math.Pow(10, k.boostDB/20)), clock: echoref.NewClock(Rate)}
	if micRate != Rate { // at Rate the converter has decimated already
		c.micDec = make([]*audio.Decimator, k.micChannels)
		for i := range c.micDec {
			c.micDec[i] = audio.NewDecimator(micRate/Rate, athenaMicTaps)
		}
	}
	if k.refRate != Rate { // at Rate the reference is used as it comes
		for i := range c.refDec {
			c.refDec[i] = audio.NewDecimator(k.refRate/Rate, athenaRefTaps)
		}
	}
	return c
}

// start runs both streams, the reference first.
func (c *pairedCapture) start() error {
	if c.ref != nil {
		if err := c.ref.Start(); err != nil {
			return err
		}
	}
	return c.mics.Start()
}

// restart starts both over together after either lost audio, so the two stay lined up.
func (c *pairedCapture) restart() error {
	if c.ref != nil {
		if err := c.ref.Rewind(); err != nil {
			return err
		}
	}
	if err := c.mics.Rewind(); err != nil {
		return err
	}
	for _, d := range c.micDec {
		d.Reset()
	}
	for _, d := range c.refDec {
		if d != nil {
			d.Reset()
		}
	}
	c.clock.Reset()
	c.got = 0
	if err := c.start(); err != nil {
		return err
	}
	// The driver's prepare rewrites PWR_CFG on every start.
	if c.k.amazon {
		athenaADCBiasOn()
	}
	return nil
}

// Read fills buf with whole 16 kHz frames in the 1st gen Show 8's layout. An overrun on either device
// starts both over and is reported as alsa.ErrOverrun, as a single device's is.
func (c *pairedCapture) Read(buf []byte) (int, error) {
	frameBytes := Channels * Bits / 8
	frames := len(buf) / frameBytes
	if frames == 0 {
		return 0, fmt.Errorf("buffer smaller than one frame (%d bytes)", frameBytes)
	}

	micBytes := frames * (c.micRate / Rate) * c.k.micChannels * athenaMicBits / 8
	if len(c.micBuf) < micBytes {
		c.micBuf = make([]byte, micBytes)
	}
	if err := readFull(c.mics, c.micBuf[:micBytes]); err != nil {
		return 0, c.lost(err)
	}

	refBytes := frames * (c.k.refRate / Rate) * athenaRefChannels * athenaRefBits / 8
	if c.ref != nil {
		if len(c.refBuf) < refBytes {
			c.refBuf = make([]byte, refBytes)
		}
		if err := readFull(c.ref, c.refBuf[:refBytes]); err != nil {
			return 0, c.lost(err)
		}
	}

	var ref []byte
	if c.ref != nil {
		ref = c.refBuf[:refBytes]
	} else if c.k.softRef {
		c.softFill(frames)
	}
	c.convert(buf[:frames*frameBytes], c.micBuf[:micBytes], ref)
	c.got += uint64(frames)
	c.meter.frames += frames
	c.meter.report()
	return frames * frameBytes, nil
}

// micDelay is the microphones' decimator delay: a frame out of it is the input from this far back.
// Nothing at Rate, where the converter decimates (its own filter's delay is not counted: `techo5 echo`
// reports the lag that is left).
func (c *pairedCapture) micDelay() time.Duration {
	if c.micDec == nil {
		return 0
	}
	return time.Duration((athenaMicTaps - 1) * int64(time.Second) / 2 / int64(c.micRate))
}

// softFill stamps the read just made, from the capture's own delay, and takes the reference for it.
// A read the delay cannot be had for carries silence, as a missing reference does.
func (c *pairedCapture) softFill(frames int) {
	if len(c.softL) < frames {
		c.softL, c.softR = make([]float32, frames), make([]float32, frames)
	}
	c.softL, c.softR = c.softL[:frames], c.softR[:frames]
	c.softOK = false
	d, err := c.mics.Delay()
	if err != nil || d < 0 {
		return
	}
	// The last frame read was recorded d input frames ago, and the decimator holds it back further.
	last := time.Now().Add(-time.Duration(d)*time.Second/time.Duration(c.micRate) - c.micDelay())
	c.clock.Mark(c.got+uint64(frames)-1, last)
	c.softOK = echoref.Fill(c.softL, c.softR, c.clock.At(c.got))
}

// lost handles a read that failed: an overrun starts both over, anything else is the caller's.
func (c *pairedCapture) lost(err error) error {
	if !errors.Is(err, alsa.ErrOverrun) {
		return err
	}
	if rerr := c.restart(); rerr != nil {
		return fmt.Errorf("mic: starting the capture over: %w", rerr)
	}
	return alsa.ErrOverrun
}

// convert decimates what Read gathered, mics and ref (nil without a reference), into out. The sizes
// agree: every output frame is a whole number of input frames on each.
func (c *pairedCapture) convert(out, mics, ref []byte) {
	frameBytes := Channels * Bits / 8
	frames := len(out) / frameBytes

	f := 0
	step := c.k.micChannels * 4
	if c.micDec == nil { // the converter's own 16 kHz: a frame in is a frame out
		for at := 0; at+step <= len(mics) && f < frames; at, f = at+step, f+1 {
			for ch := range c.k.micChannels {
				y := float32(int32(binary.LittleEndian.Uint32(mics[at+4*ch:]))) / c.k.micFull * c.boost
				putS24(out[f*frameBytes+3*ch:], y)
				c.meter.mic[ch] += float64(y) * float64(y)
				c.meter.micPeak[ch] = max(c.meter.micPeak[ch], abs32(y))
			}
		}
	}
	for at := 0; c.micDec != nil && at+step <= len(mics) && f < frames; at += step {
		var ready bool
		for ch, d := range c.micDec {
			x := float32(int32(binary.LittleEndian.Uint32(mics[at+4*ch:]))) / c.k.micFull
			y, ok := d.Push(x * c.boost)
			if ok {
				putS24(out[f*frameBytes+3*ch:], y)
				c.meter.mic[ch] += float64(y) * float64(y)
				c.meter.micPeak[ch] = max(c.meter.micPeak[ch], abs32(y))
			}
			ready = ok
		}
		if ready {
			f++
		}
	}

	if ref == nil && c.softOK {
		for f := range min(frames, len(c.softL)) {
			putRef(out[f*frameBytes:], c.softL[f], c.softR[f], &c.meter.ref)
		}
		return
	}
	if ref == nil {
		for f := range frames {
			clear(out[f*frameBytes+3*RefFirst : f*frameBytes+3*(RefFirst+Refs)])
		}
		return
	}
	f = 0
	for at := 0; at+athenaRefChannels*2 <= len(ref) && f < frames; at += athenaRefChannels * 2 {
		var ys [athenaRefChannels]float32
		var ready bool
		for ch, d := range c.refDec {
			x := float32(int16(binary.LittleEndian.Uint16(ref[at+2*ch:]))) / (1 << 15)
			if d == nil { // a reference at Rate: a frame in is a frame out
				ys[ch], ready = x, true
				continue
			}
			ys[ch], ready = d.Push(x)
		}
		if ready {
			putRef(out[f*frameBytes:], ys[0], ys[1], &c.meter.ref)
			f++
		}
	}
}

// putRef writes one frame's playback reference: the first channel, the one the echo canceller reads
// (referenceInto), is the mix of the two, and the second is the second as it was played. In mono the
// two are the same and the mix is the first exactly; with the speakers in stereo, a sound mostly on
// one of them is in the reference either way.
func putRef(frame []byte, a, b float32, meter *[athenaRefChannels]float64) {
	meter[0] += float64(a) * float64(a)
	meter[1] += float64(b) * float64(b)
	putS24(frame[3*RefFirst:], (a+b)/2)
	putS24(frame[3*(RefFirst+1):], b)
}

// putS24 writes a sample in [-1, 1) as S24_3LE, held to full scale.
func putS24(b []byte, v float32) {
	s := int32(math.Round(float64(v) * (1 << 23)))
	s = min(max(s, -1<<23), 1<<23-1)
	b[0], b[1], b[2] = byte(s), byte(s>>8), byte(s>>16)
}

// readFull reads until buf is full: a read the runtime's signals cut short is carried on.
func readFull(pcm *alsa.Capture, buf []byte) error {
	for got := 0; got < len(buf); {
		n, err := pcm.Read(buf[got:])
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("mic: the capture device returned nothing")
		}
		got += n
	}
	return nil
}

// Close lets both devices go.
func (c *pairedCapture) Close() error {
	if c.k.amazon {
		athenaPmicMic(false)
	}
	var err error
	if c.ref != nil {
		err = c.ref.Close()
	}
	if merr := c.mics.Close(); merr != nil {
		err = merr
	}
	return err
}

// athenaMeter is what the microphones and the reference measure, said in the log every
// athenaMeterEvery of capture: per microphone the RMS and the peak, and the reference's RMS, in dBFS
// after the boost, as the rest of the daemon hears them. For bring-up: it is how a wake word that
// did not fire is told apart from microphones that heard nothing.
type athenaMeter struct {
	mic     [maxMics]float64
	micPeak [maxMics]float32
	ref     [athenaRefChannels]float64
	frames  int
}

const athenaMeterEvery = 10 * Rate

func (m *athenaMeter) report() {
	if m.frames < athenaMeterEvery {
		return
	}
	n := float64(m.frames)
	db := func(v float64) float64 {
		if v <= 1e-15 {
			return -150
		}
		return math.Round(10*math.Log10(v)*10) / 10
	}
	var rms, peak [maxMics]float64
	for i := range rms {
		rms[i] = db(m.mic[i] / n)
		peak[i] = db(float64(m.micPeak[i]) * float64(m.micPeak[i]))
	}
	var ref [athenaRefChannels]float64
	for i := range ref {
		ref[i] = db(m.ref[i] / n)
	}
	slog.Info("athena: capture levels", "mic_rms_dbfs", rms, "mic_peak_dbfs", peak, "ref_rms_dbfs", ref,
		"seconds", m.frames/Rate)
	*m = athenaMeter{}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
