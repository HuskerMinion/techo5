//go:build linux

// audioprobe proves raw capture and playback on the Echo Show 5 (cronos) without the vendor
// audio HAL: it records from the TLV320AIC3101 capture device, reports per-channel levels,
// writes a WAV, and plays a tone (or the recording) through the MAX98396 playback device.
//
// Run as root on the device with the satellite app stopped:
//
//	audioprobe -seconds 5 -out /data/local/tmp/cap.wav -tone
//	audioprobe -seconds 5 -out /data/local/tmp/cap.wav -loop   # play the recording back
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// toneLevel is the tone amplitude, set from -level.
var toneLevel = 0.3

// captureChannels is the capture device's channel count, set from -cap-channels: 4 on the Echo Show 5
// (two microphones and the stereo loopback), 6 on the Echo Spot (four microphones and the loopback).
var captureChannels = 4

const (
	card = 0

	captureDevice = 22
	captureRate   = 16000
	captureBits   = 24

	playbackDevice   = 23
	playbackRate     = 48000
	playbackChannels = 2
	playbackBits     = 16
)

func main() {
	seconds := flag.Int("seconds", 3, "seconds to capture")
	out := flag.String("out", "", "write the capture as a 16-bit multichannel WAV to this path")
	tone := flag.Bool("tone", false, "play a 1 kHz tone for one second after capturing")
	loop := flag.Bool("loop", false, "play channel 0 of the recording back through the speaker")
	capPeriod := flag.Int("cap-period", 320, "capture period size in frames")
	capPeriods := flag.Int("cap-periods", 8, "capture periods in the ring")
	pbPeriod := flag.Int("pb-period", 768, "playback period size in frames")
	pbPeriods := flag.Int("pb-periods", 4, "playback periods in the ring")
	mixer := flag.Bool("mixer", false, "dump every mixer control and exit")
	skipCapture := flag.Bool("no-capture", false, "skip the capture step")
	concurrent := flag.Bool("concurrent", false, "play the tone during the capture instead of after it")
	setEnum := flag.String("set-enum", "", "set an enumerated mixer control before capturing, as NAME=ITEM")
	setInt := flag.String("set-int", "", "set an integer or boolean mixer control before capturing, as NAME=VALUE")
	perPeriod := flag.Bool("per-period", false, "play a tone filling a fixed 768-frame stack buffer per write, the way the daemon's play tool does")
	hold := flag.String("hold", "/dev/snd/pcmC0D1c", "hold this AFE node open so the DL1 driver takes its DRAM ring, not the SRAM ring that panics this kernel; empty for none")
	level := flag.Float64("level", 0.3, "tone amplitude, 0 to 1")
	capChannels := flag.Int("cap-channels", 4, "capture channels: 4 on the Echo Show 5, 6 on the Echo Spot")
	flag.Parse()
	toneLevel = *level
	captureChannels = *capChannels

	if *hold != "" {
		h, err := os.OpenFile(*hold, os.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			fatal("holding %s: %v", *hold, err)
		}
		defer h.Close()
	}

	if *mixer {
		dumpMixer()
		return
	}

	if *setEnum != "" {
		name, item, ok := cut(*setEnum, '=')
		if !ok {
			fatal("-set-enum wants NAME=ITEM")
		}
		m, err := alsa.OpenMixer(card)
		if err != nil {
			fatal("opening mixer: %v", err)
		}
		if err := m.SetEnum(name, item); err != nil {
			fatal("%v", err)
		}
		now, _ := m.GetEnum(name)
		fmt.Printf("mixer: %s = %s\n", name, now)
		_ = m.Close()
	}

	if *setInt != "" {
		name, val, ok := cut(*setInt, '=')
		if !ok {
			fatal("-set-int wants NAME=VALUE")
		}
		var v uint32
		if _, err := fmt.Sscanf(val, "%d", &v); err != nil {
			fatal("-set-int value: %v", err)
		}
		m, err := alsa.OpenMixer(card)
		if err != nil {
			fatal("opening mixer: %v", err)
		}
		if err := m.SetInt(name, v); err != nil {
			fatal("%v", err)
		}
		c, _ := m.Find(name)
		now, _ := m.Get(c)
		fmt.Printf("mixer: %s = %v\n", name, now)
		_ = m.Close()
	}

	if *perPeriod {
		playPerPeriod()
		return
	}

	if *concurrent {
		go func() {
			time.Sleep(500 * time.Millisecond)
			playTone(*pbPeriod, *pbPeriods)
		}()
	}

	var recorded []byte
	if !*skipCapture {
		recorded = capture(*seconds, *capPeriod, *capPeriods)
		if *out != "" {
			if err := writeWAV(*out, recorded); err != nil {
				fatal("writing wav: %v", err)
			}
			fmt.Printf("wrote %s (%d bytes)\n", *out, len(recorded))
		}
	}

	if *tone || *loop {
		pb, err := alsa.OpenPlayback(card, playbackDevice, alsa.Config{
			Channels: playbackChannels, Rate: playbackRate, Format: alsa.FormatS16_LE, Bits: playbackBits,
			PeriodSize: *pbPeriod, Periods: *pbPeriods,
		})
		if err != nil {
			fatal("opening playback pcmC%dD%dp: %v", card, playbackDevice, err)
		}
		fmt.Printf("playback open: pcmC%dD%dp %d Hz %d ch S16_LE period %d x %d\n", card, playbackDevice, playbackRate, playbackChannels, *pbPeriod, *pbPeriods)
		var pcm []byte
		if *tone {
			pcm = toneS16Stereo(1000, 1.0, toneLevel)
		} else {
			pcm = upsampleCh0(recorded)
		}
		start := time.Now()
		frameBytes := pb.FrameBytes()
		chunk := *pbPeriod * frameBytes
		var underruns int
		for off := 0; off < len(pcm); off += chunk {
			end := off + chunk
			if end > len(pcm) {
				end = len(pcm)
			}
			if _, err := pb.Write(pcm[off:end]); err != nil {
				if err == alsa.ErrUnderrun {
					underruns++
					continue
				}
				fatal("write: %v", err)
			}
		}
		_ = pb.Drain()
		_ = pb.Close()
		fmt.Printf("played %d frames in %v, underruns %d\n", len(pcm)/frameBytes, time.Since(start).Round(time.Millisecond), underruns)
	}
}

// playPerPeriod reproduces the daemon's play tool exactly: a constant-size buffer that escape
// analysis keeps on the stack, filled one period at a time between writes.
func playPerPeriod() {
	const (
		period  = 768
		periods = 4
	)
	pb, err := alsa.OpenPlayback(card, playbackDevice, alsa.Config{
		Channels: playbackChannels, Rate: playbackRate, Format: alsa.FormatS16_LE, Bits: playbackBits,
		PeriodSize: period, Periods: periods,
	})
	if err != nil {
		fatal("opening playback: %v", err)
	}
	defer pb.Close()
	fmt.Printf("per-period: pcmC%dD%dp ring %d x %d, 440 Hz at 20%%\n", card, playbackDevice, period, periods)
	frames := playbackRate
	buf := make([]byte, period*playbackChannels*playbackBits/8)
	for done := 0; done < frames; {
		n := min(period, frames-done)
		for i := 0; i < n; i++ {
			t := float64(done+i) / playbackRate
			s := int16(0.2 * math.MaxInt16 * math.Sin(2*math.Pi*440*t))
			binary.LittleEndian.PutUint16(buf[i*4:], uint16(s))
			binary.LittleEndian.PutUint16(buf[i*4+2:], uint16(s))
		}
		if _, err := pb.Write(buf[:n*4]); err != nil {
			if err == alsa.ErrUnderrun {
				fmt.Println("underrun")
				continue
			}
			fatal("write: %v", err)
		}
		done += n
	}
	_ = pb.Drain()
	fmt.Println("per-period done")
}

func playTone(period, periods int) {
	pb, err := alsa.OpenPlayback(card, playbackDevice, alsa.Config{
		Channels: playbackChannels, Rate: playbackRate, Format: alsa.FormatS16_LE, Bits: playbackBits,
		PeriodSize: period, Periods: periods,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "audioprobe: concurrent playback: %v\n", err)
		return
	}
	pcm := toneS16Stereo(1000, 1.5, toneLevel)
	chunk := period * pb.FrameBytes()
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if _, err := pb.Write(pcm[off:end]); err != nil && err != alsa.ErrUnderrun {
			fmt.Fprintf(os.Stderr, "audioprobe: concurrent write: %v\n", err)
			break
		}
	}
	_ = pb.Drain()
	_ = pb.Close()
	fmt.Println("concurrent tone done")
}

func cut(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func capture(seconds, period, periods int) []byte {
	c, err := alsa.Open(card, captureDevice, alsa.Config{
		Channels: captureChannels, Rate: captureRate, Format: alsa.FormatS24_3LE, Bits: captureBits,
		PeriodSize: period, Periods: periods,
	})
	if err != nil {
		fatal("opening capture pcmC%dD%dc: %v", card, captureDevice, err)
	}
	defer c.Close()
	fmt.Printf("capture open: pcmC%dD%dc %d Hz %d ch S24_3LE period %d x %d\n", card, captureDevice, captureRate, captureChannels, period, periods)

	frameBytes := c.FrameBytes()
	want := seconds * captureRate * frameBytes
	buf := make([]byte, 0, want)
	chunk := make([]byte, period*frameBytes)
	var overruns int
	start := time.Now()
	for len(buf) < want {
		n, err := c.Read(chunk)
		if err != nil {
			if err == alsa.ErrOverrun {
				overruns++
				continue
			}
			fatal("read: %v", err)
		}
		buf = append(buf, chunk[:n]...)
	}
	fmt.Printf("captured %d frames in %v, overruns %d\n", len(buf)/frameBytes, time.Since(start).Round(time.Millisecond), overruns)
	report(buf)
	return buf
}

// report prints RMS and peak per channel in dBFS so levels can be judged without listening.
func report(pcm []byte) {
	frameBytes := captureChannels * 3
	frames := len(pcm) / frameBytes
	if frames == 0 {
		return
	}
	sumsq := make([]float64, captureChannels)
	peak := make([]float64, captureChannels)
	for f := 0; f < frames; f++ {
		for ch := 0; ch < captureChannels; ch++ {
			s := sample24(pcm[f*frameBytes+ch*3:])
			v := float64(s) / float64(1<<23)
			sumsq[ch] += v * v
			if a := math.Abs(v); a > peak[ch] {
				peak[ch] = a
			}
		}
	}
	for ch := 0; ch < captureChannels; ch++ {
		rms := math.Sqrt(sumsq[ch] / float64(frames))
		fmt.Printf("  ch%d: rms %6.1f dBFS  peak %6.1f dBFS\n", ch, db(rms), db(peak[ch]))
	}
}

func db(v float64) float64 {
	if v <= 0 {
		return -120
	}
	return 20 * math.Log10(v)
}

func sample24(b []byte) int32 {
	v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
	if v&0x800000 != 0 {
		v |= ^0xffffff
	}
	return v
}

// writeWAV stores the capture as 16-bit PCM with all channels, which any editor can open.
func writeWAV(path string, pcm []byte) error {
	frameBytes := captureChannels * 3
	frames := len(pcm) / frameBytes
	data := make([]byte, frames*captureChannels*2)
	for f := 0; f < frames; f++ {
		for ch := 0; ch < captureChannels; ch++ {
			s := sample24(pcm[f*frameBytes+ch*3:]) >> 8
			binary.LittleEndian.PutUint16(data[(f*captureChannels+ch)*2:], uint16(int16(s)))
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(data)))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], uint16(captureChannels))
	binary.LittleEndian.PutUint32(hdr[24:], captureRate)
	binary.LittleEndian.PutUint32(hdr[28:], uint32(captureRate*captureChannels*2))
	binary.LittleEndian.PutUint16(hdr[32:], uint16(captureChannels*2))
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))
	if _, err := f.Write(hdr); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// toneS16Stereo plays hz on the left channel and 1.5×hz on the right, so a stereo loopback
// reference can be told apart from a mono one.
func toneS16Stereo(hz float64, seconds, amplitude float64) []byte {
	n := int(seconds * playbackRate)
	out := make([]byte, n*4)
	for i := 0; i < n; i++ {
		// A short fade at both ends keeps the amp from clicking.
		env := 1.0
		if i < 480 {
			env = float64(i) / 480
		} else if n-i < 480 {
			env = float64(n-i) / 480
		}
		t := float64(i) / playbackRate
		l := int16(amplitude * env * 32767 * math.Sin(2*math.Pi*hz*t))
		r := int16(amplitude * env * 32767 * math.Sin(2*math.Pi*hz*1.5*t))
		binary.LittleEndian.PutUint16(out[i*4:], uint16(l))
		binary.LittleEndian.PutUint16(out[i*4+2:], uint16(r))
	}
	return out
}

// upsampleCh0 turns channel 0 of the 16 kHz capture into 48 kHz stereo by sample repetition,
// which is crude but enough to hear whether the microphones heard the room.
func upsampleCh0(pcm []byte) []byte {
	frameBytes := captureChannels * 3
	frames := len(pcm) / frameBytes
	out := make([]byte, frames*3*4)
	for f := 0; f < frames; f++ {
		s := int16(sample24(pcm[f*frameBytes:]) >> 8)
		for r := 0; r < 3; r++ {
			i := (f*3 + r) * 4
			binary.LittleEndian.PutUint16(out[i:], uint16(s))
			binary.LittleEndian.PutUint16(out[i+2:], uint16(s))
		}
	}
	return out
}

func dumpMixer() {
	m, err := alsa.OpenMixer(card)
	if err != nil {
		fatal("opening mixer: %v", err)
	}
	defer m.Close()
	controls, err := m.Controls()
	if err != nil {
		fatal("listing controls: %v", err)
	}
	for _, c := range controls {
		v, err := m.Get(c)
		if err != nil {
			fmt.Printf("%3d %-45s <%v>\n", c.Numid, c.Name, err)
			continue
		}
		switch c.Type {
		case alsa.TypeEnumerated:
			name := "?"
			if len(v) > 0 && int(v[0]) < len(c.Items) {
				name = c.Items[v[0]]
			}
			fmt.Printf("%3d %-45s enum %s %v\n", c.Numid, c.Name, name, c.Items)
		case alsa.TypeInteger:
			fmt.Printf("%3d %-45s int %v [%d..%d]\n", c.Numid, c.Name, v, c.Min, c.Max)
		default:
			fmt.Printf("%3d %-45s type%d %v\n", c.Numid, c.Name, c.Type, v)
		}
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "audioprobe: "+format+"\n", args...)
	os.Exit(1)
}
